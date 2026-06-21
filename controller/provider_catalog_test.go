package controller

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/service/catalogimport"
	"github.com/gin-gonic/gin"
)

func TestBuildProviderCatalogImportRequestAcceptsBundleFilesFromLegacyRuleType(t *testing.T) {
	originalFetch := fetchVectorRemotePricing
	fetchVectorRemotePricing = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`{
			"success":true,
			"data":{
				"model_completion_ratio":{},
				"group_special":{"grok-video-3-10s":["default"]},
				"model_group":{
					"default":{
						"GroupRatio":1,
						"ModelPrice":{"grok-video-3-10s":{"priceType":1,"price":0.6}}
					}
				}
			}
		}`), nil
	}
	t.Cleanup(func() {
		fetchVectorRemotePricing = originalFetch
	})

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
	if req.Catalog.RemotePricingReport == nil || req.Catalog.RemotePricingReport.CheckedModels != 1 {
		t.Fatalf("expected remote pricing validation report, got %+v", req.Catalog.RemotePricingReport)
	}
}

func TestBuildProviderCatalogImportRequestBlocksWhenRemotePricingUnavailable(t *testing.T) {
	originalFetch := fetchVectorRemotePricing
	fetchVectorRemotePricing = func(_ context.Context, _ string) ([]byte, error) {
		return nil, fmt.Errorf("pricing unavailable")
	}
	t.Cleanup(func() {
		fetchVectorRemotePricing = originalFetch
	})

	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("provider_code", "vector")
	_ = writer.WriteField("provider_name", "向量")
	_ = writer.WriteField("rule_type", catalogimport.RuleTypeVectorBundle)
	_ = writer.WriteField("base_url", "https://q.aibaotui.com")
	writeMultipartFile(t, writer, "normal_rule_file", "normal.json", []byte(`{
		"auto_groups":["default"],
		"vendors":[{"id":1,"name":"向量"}],
		"group_ratio":{"default":1},
		"usable_group":{"default":"默认"},
		"data":[]
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

	_, err := buildProviderCatalogImportRequest(ctx, false)
	if err == nil || !strings.Contains(err.Error(), "pricing unavailable") {
		t.Fatalf("expected remote pricing fetch error, got %v", err)
	}
}

func TestBuildProviderCatalogImportRequestValidatesVectorNormalAgainstRemotePricing(t *testing.T) {
	originalFetch := fetchVectorRemotePricing
	fetchVectorRemotePricing = func(_ context.Context, _ string) ([]byte, error) {
		return []byte(`{"success":true,"data":{
			"model_completion_ratio":{"gpt-test":2},
			"group_special":{"gpt-test":["default"]},
			"model_group":{"default":{"GroupRatio":1,"ModelPrice":{
				"gpt-test":{"priceType":0,"price":1.25}
			}}}
		}}`), nil
	}
	t.Cleanup(func() {
		fetchVectorRemotePricing = originalFetch
	})

	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("provider_code", "vector")
	_ = writer.WriteField("provider_name", "向量")
	_ = writer.WriteField("rule_type", catalogimport.RuleTypeVectorNormal)
	_ = writer.WriteField("base_url", "https://q.aibaotui.com")
	writeMultipartFile(t, writer, "rule_file", "normal.json", []byte(`{
		"group_ratio":{"default":1},
		"usable_group":{"default":"默认"},
		"data":[{
			"model_name":"gpt-test",
			"quota_type":0,
			"model_ratio":1.25,
			"completion_ratio":2,
			"enable_groups":["default"],
			"supported_endpoint_types":["openai"]
		}]
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
	if req.Catalog.RemotePricingReport == nil || req.Catalog.RemotePricingReport.CheckedModels != 1 {
		t.Fatalf("expected remote validation for vector normal import, got %+v", req.Catalog.RemotePricingReport)
	}
	if req.Catalog.SourceHashes["remote_pricing"] == "" {
		t.Fatal("expected remote pricing source hash")
	}
}

func TestBuildProviderCatalogClearRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("provider_code", "521")
	_ = writer.WriteField("base_url", "https://example.com/")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/provider-catalog/clear/preview", &body)
	ctx.Request.Header.Set("Content-Type", writer.FormDataContentType())

	req := buildProviderCatalogClearRequest(ctx, true)
	if req.ProviderCode != "521" || req.BaseURL != "https://example.com/" || !req.Apply {
		t.Fatalf("unexpected clear request: %+v", req)
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
