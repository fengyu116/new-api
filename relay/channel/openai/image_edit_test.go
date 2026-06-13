package openai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestConvertImageEditRequestMultipart verifies that ConvertImageRequest
// re-serializes multipart image edit requests with all fields (including
// stream) and the file intact, both when the form was already parsed and when
// it must be re-parsed from the reusable body.
func TestConvertImageEditRequestMultipart(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newMultipartContext := func(t *testing.T, prompt string) *gin.Context {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField("model", "gpt-image-1"))
		require.NoError(t, writer.WriteField("prompt", prompt))
		require.NoError(t, writer.WriteField("stream", "true"))
		require.NoError(t, writer.WriteField("partial_images", "3"))
		part, err := writer.CreateFormFile("image", "input.png")
		require.NoError(t, err)
		_, err = part.Write([]byte("fake image"))
		require.NoError(t, err)
		require.NoError(t, writer.Close())

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		return c
	}

	convertAndReplay := func(t *testing.T, c *gin.Context, prompt string) {
		info := &relaycommon.RelayInfo{
			RelayMode: relayconstant.RelayModeImagesEdits,
		}
		request := dto.ImageRequest{
			Model:  "gpt-image-1",
			Prompt: prompt,
			Stream: common.GetPointer(true),
		}

		converted, err := (&Adaptor{}).ConvertImageRequest(c, info, request)
		require.NoError(t, err)
		convertedBody, ok := converted.(*bytes.Buffer)
		require.True(t, ok)

		replayedRequest := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(convertedBody.Bytes()))
		replayedRequest.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
		require.NoError(t, replayedRequest.ParseMultipartForm(32<<20))

		require.Equal(t, "gpt-image-1", replayedRequest.PostForm.Get("model"))
		require.Equal(t, prompt, replayedRequest.PostForm.Get("prompt"))
		require.Equal(t, "true", replayedRequest.PostForm.Get("stream"))
		require.Equal(t, "3", replayedRequest.PostForm.Get("partial_images"))
		require.Len(t, replayedRequest.MultipartForm.File["image"], 1)

		file, err := replayedRequest.MultipartForm.File["image"][0].Open()
		require.NoError(t, err)
		defer file.Close()
		fileBytes, err := io.ReadAll(file)
		require.NoError(t, err)
		require.Equal(t, []byte("fake image"), fileBytes)
	}

	t.Run("with pre-parsed form", func(t *testing.T) {
		prompt := "edit this image"
		c := newMultipartContext(t, prompt)
		require.NoError(t, c.Request.ParseMultipartForm(32<<20))

		convertAndReplay(t, c, prompt)
	})

	t.Run("re-parses reusable body when form is missing", func(t *testing.T) {
		prompt := "edit without pre-parsed form"
		c := newMultipartContext(t, prompt)

		storage, err := common.GetBodyStorage(c)
		require.NoError(t, err)
		c.Request.Body = io.NopCloser(storage)
		c.Request.MultipartForm = nil
		c.Request.PostForm = nil

		convertAndReplay(t, c, prompt)
	})
}

func TestConvertImageEditRequestJSONDataURLs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newContext := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(nil))
		c.Request.Header.Set("Content-Type", "application/json")
		return c
	}

	dataURL := func(mimeType string, data []byte) string {
		return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
	}

	t.Run("all OpenAI-compatible edit model names use the same conversion", func(t *testing.T) {
		for _, modelName := range []string{
			"gpt-image-1",
			"gpt-image-1-mini",
			"gpt-image-1.5",
			"gpt-image-2",
			"gpt-image-2-vip",
		} {
			t.Run(modelName, func(t *testing.T) {
				c := newContext()
				imageBytes := []byte("model-agnostic-image")
				rawImage, err := json.Marshal(dataURL("image/jpeg", imageBytes))
				require.NoError(t, err)

				converted, err := (&Adaptor{}).ConvertImageRequest(c, &relaycommon.RelayInfo{
					RelayMode: relayconstant.RelayModeImagesEdits,
				}, dto.ImageRequest{
					Model:  modelName,
					Prompt: "edit",
					Image:  rawImage,
				})
				require.NoError(t, err)

				body := converted.(*bytes.Buffer)
				replayed := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
				replayed.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
				require.NoError(t, replayed.ParseMultipartForm(32<<20))
				require.Equal(t, modelName, replayed.PostForm.Get("model"))
				require.Len(t, replayed.MultipartForm.File["image"], 1)
			})
		}
	})

	t.Run("single image keeps fields and bytes", func(t *testing.T) {
		c := newContext()
		imageBytes := []byte("single-image-bytes")
		request := dto.ImageRequest{
			Model:          "gpt-image-2",
			Prompt:         "edit this image",
			N:              common.GetPointer(uint(1)),
			Size:           "1024x1536",
			Quality:        "standard",
			ResponseFormat: "b64_json",
			Image:          json.RawMessage(`"` + dataURL("image/jpeg", imageBytes) + `"`),
			Extra: map[string]json.RawMessage{
				"metadata": json.RawMessage(`{"aspectRatio":"9:16"}`),
			},
		}

		converted, err := (&Adaptor{}).ConvertImageRequest(c, &relaycommon.RelayInfo{
			RelayMode: relayconstant.RelayModeImagesEdits,
		}, request)
		require.NoError(t, err)

		body, ok := converted.(*bytes.Buffer)
		require.True(t, ok)
		replayed := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
		replayed.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
		require.NoError(t, replayed.ParseMultipartForm(32<<20))
		require.Equal(t, "gpt-image-2", replayed.PostForm.Get("model"))
		require.Equal(t, "edit this image", replayed.PostForm.Get("prompt"))
		require.Equal(t, "1024x1536", replayed.PostForm.Get("size"))
		require.Equal(t, "standard", replayed.PostForm.Get("quality"))
		require.Equal(t, `{"aspectRatio":"9:16"}`, replayed.PostForm.Get("metadata"))
		require.Len(t, replayed.MultipartForm.File["image"], 1)

		file, err := replayed.MultipartForm.File["image"][0].Open()
		require.NoError(t, err)
		defer file.Close()
		actual, err := io.ReadAll(file)
		require.NoError(t, err)
		require.Equal(t, imageBytes, actual)
		require.Equal(t, "image/jpeg", replayed.MultipartForm.File["image"][0].Header.Get("Content-Type"))
	})

	t.Run("multiple images preserve order", func(t *testing.T) {
		c := newContext()
		first := []byte("first-image")
		second := []byte("second-image")
		images, err := json.Marshal([]string{
			dataURL("image/png", first),
			dataURL("image/webp", second),
		})
		require.NoError(t, err)
		request := dto.ImageRequest{
			Model:  "gpt-image-2",
			Prompt: "combine references",
			Images: images,
		}

		converted, err := (&Adaptor{}).ConvertImageRequest(c, &relaycommon.RelayInfo{
			RelayMode: relayconstant.RelayModeImagesEdits,
		}, request)
		require.NoError(t, err)

		body := converted.(*bytes.Buffer)
		replayed := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
		replayed.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
		require.NoError(t, replayed.ParseMultipartForm(32<<20))
		files := replayed.MultipartForm.File["image[]"]
		require.Len(t, files, 2)
		for index, expected := range [][]byte{first, second} {
			file, err := files[index].Open()
			require.NoError(t, err)
			actual, err := io.ReadAll(file)
			require.NoError(t, err)
			require.NoError(t, file.Close())
			require.Equal(t, expected, actual)
		}
		require.Equal(t, "image/png", files[0].Header.Get("Content-Type"))
		require.Equal(t, "image/webp", files[1].Header.Get("Content-Type"))
	})

	t.Run("rejects remote URLs and invalid base64", func(t *testing.T) {
		for name, image := range map[string]string{
			"remote URL":     "https://example.com/image.png",
			"invalid base64": "data:image/png;base64,not-valid-***",
		} {
			t.Run(name, func(t *testing.T) {
				c := newContext()
				raw, err := json.Marshal(image)
				require.NoError(t, err)
				_, err = (&Adaptor{}).ConvertImageRequest(c, &relaycommon.RelayInfo{
					RelayMode: relayconstant.RelayModeImagesEdits,
				}, dto.ImageRequest{
					Model:  "gpt-image-2",
					Prompt: "edit",
					Image:  raw,
				})
				require.Error(t, err)
			})
		}
	})

}
