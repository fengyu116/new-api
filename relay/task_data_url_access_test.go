package relay

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
)

func TestValidateTaskDataURLAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	globalSettings := model_setting.GetGlobalSettings()
	originalWhitelist := append([]int(nil), globalSettings.ImageEditDataURLUserWhitelist...)
	originalBlacklist := append([]int(nil), globalSettings.ImageEditDataURLUserBlacklist...)
	globalSettings.ImageEditDataURLUserWhitelist = nil
	globalSettings.ImageEditDataURLUserBlacklist = []int{9}
	defer func() {
		globalSettings.ImageEditDataURLUserWhitelist = originalWhitelist
		globalSettings.ImageEditDataURLUserBlacklist = originalBlacklist
	}()

	newJSONContext := func(body []byte) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		storage, err := common.CreateBodyStorage(body)
		if err != nil {
			t.Fatal(err)
		}
		c.Set(common.KeyBodyStorage, storage)
		t.Cleanup(func() { storage.Close() })
		return c
	}

	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("image"))

	t.Run("blocked user cannot use JSON Data URL task references", func(t *testing.T) {
		body := []byte(`{"model":"sora-2","prompt":"animate","input_reference":"` + dataURL + `"}`)
		err := validateTaskDataURLAccess(newJSONContext(body), &relaycommon.RelayInfo{UserId: 9})
		if err == nil {
			t.Fatalf("expected access error")
		}
	})

	t.Run("multipart keeps original behavior", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
		c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=test")
		err := validateTaskDataURLAccess(c, &relaycommon.RelayInfo{UserId: 9})
		if err != nil {
			t.Fatalf("multipart request should not be restricted: %v", err)
		}
	})

	t.Run("remote URL references do not use this policy", func(t *testing.T) {
		body := []byte(`{"model":"sora-2","prompt":"animate","input_reference":"https://example.com/image.png"}`)
		err := validateTaskDataURLAccess(newJSONContext(body), &relaycommon.RelayInfo{UserId: 9})
		if err != nil {
			t.Fatalf("remote URL should not be restricted: %v", err)
		}
	})

	t.Run("allowed user can use JSON Data URL task references", func(t *testing.T) {
		globalSettings.ImageEditDataURLUserWhitelist = []int{7}
		globalSettings.ImageEditDataURLUserBlacklist = nil
		body := []byte(`{"model":"sora-2","prompt":"animate","images":["` + dataURL + `"]}`)
		err := validateTaskDataURLAccess(newJSONContext(body), &relaycommon.RelayInfo{UserId: 7})
		if err != nil {
			t.Fatalf("allowed user should not be restricted: %v", err)
		}
	})

	t.Run("policy still delegates to user-level setting", func(t *testing.T) {
		globalSettings.ImageEditDataURLUserWhitelist = []int{7}
		globalSettings.ImageEditDataURLUserBlacklist = nil
		if model_setting.IsImageEditDataURLConversionAllowed(8) {
			t.Fatalf("expected user 8 to be rejected by whitelist")
		}
	})
}
