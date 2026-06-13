package relay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
)

func TestValidateImageEditDataURLAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := model_setting.GetGlobalSettings()
	originalWhitelist := append([]int(nil), settings.ImageEditDataURLUserWhitelist...)
	originalBlacklist := append([]int(nil), settings.ImageEditDataURLUserBlacklist...)
	settings.ImageEditDataURLUserWhitelist = nil
	settings.ImageEditDataURLUserBlacklist = []int{9}
	t.Cleanup(func() {
		settings.ImageEditDataURLUserWhitelist = originalWhitelist
		settings.ImageEditDataURLUserBlacklist = originalBlacklist
	})

	rawImage, err := json.Marshal("data:image/png;base64,aW1hZ2U=")
	if err != nil {
		t.Fatal(err)
	}
	request := dto.ImageRequest{
		Model: "gpt-image-2",
		Image: rawImage,
	}

	newContext := func(contentType string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(nil))
		c.Request.Header.Set("Content-Type", contentType)
		return c
	}

	t.Run("blocked user cannot use JSON conversion", func(t *testing.T) {
		err := validateImageEditDataURLAccess(newContext("application/json"), &relaycommon.RelayInfo{
			UserId:    9,
			RelayMode: relayconstant.RelayModeImagesEdits,
		}, request)
		if err == nil || err.StatusCode != http.StatusForbidden {
			t.Fatalf("expected forbidden error, got %#v", err)
		}
	})

	t.Run("multipart keeps original behavior", func(t *testing.T) {
		err := validateImageEditDataURLAccess(newContext("multipart/form-data; boundary=test"), &relaycommon.RelayInfo{
			UserId:    9,
			RelayMode: relayconstant.RelayModeImagesEdits,
		}, request)
		if err != nil {
			t.Fatalf("multipart request should not be restricted: %v", err)
		}
	})

	t.Run("other image modes are unaffected", func(t *testing.T) {
		err := validateImageEditDataURLAccess(newContext("application/json"), &relaycommon.RelayInfo{
			UserId:    9,
			RelayMode: relayconstant.RelayModeImagesGenerations,
		}, request)
		if err != nil {
			t.Fatalf("image generation should not be restricted: %v", err)
		}
	})

	t.Run("remote URL JSON references do not use this conversion policy", func(t *testing.T) {
		remoteImage, marshalErr := json.Marshal("https://example.com/reference.png")
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		request.Image = remoteImage
		err := validateImageEditDataURLAccess(newContext("application/json"), &relaycommon.RelayInfo{
			UserId:    9,
			RelayMode: relayconstant.RelayModeImagesEdits,
		}, request)
		if err != nil {
			t.Fatalf("remote URL references should remain provider-specific: %v", err)
		}
		request.Image = rawImage
	})

	t.Run("allowed user can use every model", func(t *testing.T) {
		for _, model := range []string{"gpt-image-2", "qwen-image-edit", "flux-kontext-pro"} {
			request.Model = model
			err := validateImageEditDataURLAccess(newContext("application/json"), &relaycommon.RelayInfo{
				UserId:    10,
				RelayMode: relayconstant.RelayModeImagesEdits,
			}, request)
			if err != nil {
				t.Fatalf("model %s should be allowed for user: %v", model, err)
			}
		}
	})
}
