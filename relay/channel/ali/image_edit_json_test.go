package ali

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestConvertImageEditJSONDataURLs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dataURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("reference-image"))
	images, err := json.Marshal([]string{dataURL})
	require.NoError(t, err)

	newContext := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(nil))
		c.Request.Header.Set("Content-Type", "application/json")
		return c
	}

	t.Run("qwen image edit keeps data URL in native messages", func(t *testing.T) {
		request := dto.ImageRequest{
			Model:  "qwen-image-edit",
			Prompt: "edit the image",
			Images: images,
		}
		info := &relaycommon.RelayInfo{
			RelayMode:       relayconstant.RelayModeImagesEdits,
			OriginModelName: request.Model,
			ChannelMeta: &relaycommon.ChannelMeta{
				UpstreamModelName: request.Model,
			},
		}

		converted, err := (&Adaptor{}).ConvertImageRequest(newContext(), info, request)
		require.NoError(t, err)

		aliRequest := converted.(*AliImageRequest)
		input := aliRequest.Input.(AliImageInput)
		require.Len(t, input.Messages, 1)
		content := input.Messages[0].Content.([]AliMediaContent)
		require.Equal(t, dataURL, content[0].Image)
		require.Equal(t, request.Prompt, content[1].Text)
	})

	t.Run("old wan image edit keeps data URL array", func(t *testing.T) {
		request := dto.ImageRequest{
			Model:  "wanx2.1-imageedit",
			Prompt: "edit the image",
			Images: images,
		}
		info := &relaycommon.RelayInfo{
			RelayMode:       relayconstant.RelayModeImagesEdits,
			OriginModelName: request.Model,
			ChannelMeta: &relaycommon.ChannelMeta{
				UpstreamModelName: request.Model,
			},
		}

		converted, err := (&Adaptor{}).ConvertImageRequest(newContext(), info, request)
		require.NoError(t, err)

		aliRequest := converted.(*AliImageRequest)
		input := aliRequest.Input.(WanImageInput)
		require.Equal(t, []string{dataURL}, input.Images)
		require.Equal(t, request.Prompt, input.Prompt)
	})
}
