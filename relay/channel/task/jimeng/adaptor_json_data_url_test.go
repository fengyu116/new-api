package jimeng

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBuildRequestBodyJSONDataURLImagesUsesRawBase64(t *testing.T) {
	gin.SetMode(gin.TestMode)

	imageBytes := []byte("jimeng-reference-image")
	encoded := base64.StdEncoding.EncodeToString(imageBytes)
	body := []byte(`{
		"model":"jimeng_v30",
		"prompt":"animate this",
		"images":["data:image/jpeg;base64,` + encoded + `"]
	}`)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	storage, err := common.CreateBodyStorage(body)
	require.NoError(t, err)
	defer storage.Close()
	c.Set(common.KeyBodyStorage, storage)

	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionGenerate},
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "jimeng_v30",
		},
	}
	require.Nil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info))

	requestBody, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	require.NoError(t, err)
	payload, err := io.ReadAll(requestBody)
	require.NoError(t, err)

	var upstream requestPayload
	require.NoError(t, common.Unmarshal(payload, &upstream))
	require.Equal(t, []string{encoded}, upstream.BinaryDataBase64)
	require.Empty(t, upstream.ImageUrls)
}
