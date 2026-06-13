package sora

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBuildRequestBodyJSONDataURLInputReferenceConvertsToMultipart(t *testing.T) {
	gin.SetMode(gin.TestMode)

	imageBytes := []byte("sora-reference-image")
	body := []byte(`{
		"model":"sora-2",
		"prompt":"animate this",
		"seconds":"4",
		"size":"1280x720",
		"input_reference":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(imageBytes) + `"
	}`)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	storage, err := common.CreateBodyStorage(body)
	require.NoError(t, err)
	defer storage.Close()
	c.Set(common.KeyBodyStorage, storage)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "sora-2"},
	}

	requestBody, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	require.NoError(t, err)

	payload, err := io.ReadAll(requestBody)
	require.NoError(t, err)
	replayed := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(payload))
	replayed.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	require.NoError(t, replayed.ParseMultipartForm(32<<20))
	require.Equal(t, "sora-2", replayed.FormValue("model"))
	require.Equal(t, "animate this", replayed.FormValue("prompt"))
	require.Equal(t, "4", replayed.FormValue("seconds"))
	require.Equal(t, "1280x720", replayed.FormValue("size"))

	files := replayed.MultipartForm.File["input_reference"]
	require.Len(t, files, 1)
	require.Equal(t, "image/png", files[0].Header.Get("Content-Type"))
	file, err := files[0].Open()
	require.NoError(t, err)
	defer file.Close()
	actual, err := io.ReadAll(file)
	require.NoError(t, err)
	require.Equal(t, imageBytes, actual)
}

func TestBuildRequestBodyMultipartInputReferenceKeepsOriginalForm(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "client-model"))
	require.NoError(t, writer.WriteField("prompt", "animate multipart"))
	part, err := writer.CreateFormFile("input_reference", "reference.jpg")
	require.NoError(t, err)
	_, err = part.Write([]byte("multipart-image"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	storage, err := common.CreateBodyStorage(body.Bytes())
	require.NoError(t, err)
	defer storage.Close()
	c.Set(common.KeyBodyStorage, storage)

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "sora-2"},
	}

	requestBody, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	require.NoError(t, err)
	payload, err := io.ReadAll(requestBody)
	require.NoError(t, err)

	replayed := httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(payload))
	replayed.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	require.NoError(t, replayed.ParseMultipartForm(32<<20))
	require.Equal(t, "sora-2", replayed.FormValue("model"))
	require.Equal(t, "animate multipart", replayed.FormValue("prompt"))
	require.Len(t, replayed.MultipartForm.File["input_reference"], 1)
}
