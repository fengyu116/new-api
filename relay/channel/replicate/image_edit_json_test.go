package replicate

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestConvertImageEditJSONDataURLUploadsReference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()

	imageBytes := []byte("replicate-reference")
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes)
	rawImage, err := json.Marshal(dataURL)
	require.NoError(t, err)

	var uploaded []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/files", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		require.NoError(t, r.ParseMultipartForm(8<<20))
		file, _, err := r.FormFile("content")
		require.NoError(t, err)
		defer file.Close()
		uploaded, err = io.ReadAll(file)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"urls":{"get":"https://files.example/reference.png"}}`))
	}))
	defer server.Close()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(nil))
	c.Request.Header.Set("Content-Type", "application/json")

	converted, err := (&Adaptor{}).ConvertImageRequest(c, &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesEdits,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "black-forest-labs/flux-kontext-pro",
			ChannelBaseUrl:    server.URL,
			ApiKey:            "test-key",
		},
	}, dto.ImageRequest{
		Model:  "black-forest-labs/flux-kontext-pro",
		Prompt: "edit the image",
		Image:  rawImage,
	})
	require.NoError(t, err)
	require.Equal(t, imageBytes, uploaded)

	payload := converted.(map[string]any)
	input := payload["input"].(map[string]any)
	require.Equal(t, "https://files.example/reference.png", input["image_prompt"])
}
