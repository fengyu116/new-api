package controller

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/service/catalogimport"
	"github.com/gin-gonic/gin"
)

func TestBuildProviderCatalogImportRequestAcceptsBundleFilesFromLegacyRuleType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("provider_code", "vector")
	_ = writer.WriteField("provider_name", "向量")
	_ = writer.WriteField("rule_type", catalogimport.RuleTypeVectorNormal)
	_ = writer.WriteField("base_url", "https://q.aibaotui.com")
	writeMultipartFile(t, writer, "normal_rule_file", "normal.json", []byte(`{
		"auto_groups":["default"],
		"vendors":[{"id":1,"name":"向量"}],
		"groups":{"default":"默认"},
		"data":[{
			"model_name":"grok-video-3-10s",
			"description":"grok fixed video",
			"model_type":"视频",
			"vendor_id":1,
			"quota_type":1,
			"model_price":0.6,
			"enable_groups":["default"],
			"supported_endpoint_types":["grok视频"]
		}]
	}`))
	writeMultipartFile(t, writer, "special_rule_file", "special.json", []byte(`{
		"version":"1",
		"models":{}
	}`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/provider-catalog/preview", &body)
	ctx.Request.Header.Set("Content-Type", writer.FormDataContentType())

	req, err := buildProviderCatalogImportRequest(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if req.Catalog == nil || len(req.Catalog.TaskBillingRules) == 0 {
		t.Fatalf("expected vector bundle parser to create task billing rules, got %+v", req.Catalog)
	}
	if _, ok := req.Catalog.TaskBillingRules["grok-video-3-10s"]; !ok {
		t.Fatalf("expected grok-video-3-10s task billing rule, got %+v", req.Catalog.TaskBillingRules)
	}
}

func writeMultipartFile(t *testing.T, writer *multipart.Writer, field, filename string, content []byte) {
	t.Helper()
	part, err := writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
}
