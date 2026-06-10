package catalogimport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/special_pricing"
)

func TestParseVectorNormalCatalog(t *testing.T) {
	input := []byte(`{
		"auto_groups":["default"],
		"vendors":[{"id":1,"name":"OpenAI","icon":"OpenAI.Color"}],
		"data":[{
			"model_name":"gpt-test",
			"description":"desc",
			"tags":"对话",
			"model_type":"文本",
			"vendor_id":1,
			"quota_type":0,
			"model_ratio":1.25,
			"model_price":0,
			"completion_ratio":2,
			"cache_ratio":0.1,
			"enable_groups":["default","vip"],
			"supported_endpoint_types":["openai"],
			"sort_order":10
		}]
	}`)

	catalog, err := ParseCatalog(ParseRequest{
		ProviderCode: "vector",
		ProviderName: "向量",
		RuleType:     "vector_normal",
		BaseURL:      "https://example.com",
		Content:      input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ProviderCode != "vector" || len(catalog.Models) != 1 || len(catalog.Vendors) != 1 {
		t.Fatalf("unexpected catalog: %+v", catalog)
	}
	if catalog.Models[0].Name != "gpt-test" || catalog.Models[0].ModelRatio != 1.25 {
		t.Fatalf("unexpected model: %+v", catalog.Models[0])
	}
	if len(catalog.AutoGroups) != 1 || catalog.AutoGroups[0] != "default" {
		t.Fatalf("unexpected auto groups: %+v", catalog.AutoGroups)
	}
}

func TestParseShenggeNormalCatalog(t *testing.T) {
	input := []byte(`[
		{"group":"Claude Max","path":"/claude","multiplier":7,"models":["claude-sonnet-test"]},
		{"group":"画图","path":"/openai","multiplier":1,"models":[{"name":"nano-banana","base_model":"gemini-image","resolutions":["1K"]}]}
	]`)

	catalog, err := ParseCatalog(ParseRequest{
		ProviderCode: "shengge",
		ProviderName: "胜哥",
		RuleType:     "shengge_normal",
		BaseURL:      "https://example.com",
		Content:      input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 2 {
		t.Fatalf("expected two models, got %d", len(catalog.Models))
	}
	if catalog.Models[0].EnableGroups[0] != "Claude Max" || catalog.Models[0].ModelRatio != 7 {
		t.Fatalf("unexpected first model: %+v", catalog.Models[0])
	}
	if catalog.Models[1].ModelMapping["nano-banana"] != "gemini-image" {
		t.Fatalf("expected base model mapping, got %+v", catalog.Models[1].ModelMapping)
	}
}

func TestParseVectorSpecialRejectsJavaScriptBundle(t *testing.T) {
	_, err := ParseCatalog(ParseRequest{
		ProviderCode: "vector",
		ProviderName: "向量",
		RuleType:     "vector_special",
		BaseURL:      "https://example.com",
		Content:      []byte(`import {x} from "./bundle.js"; const a = 1;`),
	})
	if err == nil || !strings.Contains(err.Error(), "标准 JSON") {
		t.Fatalf("expected standard JSON error, got %v", err)
	}
}

func TestParseGroupKeysMasksSecrets(t *testing.T) {
	rows, err := ParseTokenRowsFromSheetRows([][]string{
		{"名称", "状态", "分组", "密钥（sk-前缀）", "可用模型"},
		{"向量 default", "已启用", "default", "sk-abcdefghijklmnopqrstuvwxyz", "gpt-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := BuildGroupKeyReport("vector", "https://example.com", rows)
	if err != nil {
		t.Fatal(err)
	}
	if report.ValidRows != 1 || report.Groups[0].KeyMask == rows[0].Key {
		t.Fatalf("expected masked key report, got %+v", report)
	}
}

func TestDryRunReportsProviderScope(t *testing.T) {
	catalog := &ProviderCatalog{
		ProviderCode: "vector",
		ProviderName: "向量",
		BaseURL:      "https://example.com",
		Groups:       map[string]string{"default": "默认分组"},
		Models: []CatalogModel{{
			Name:         "gpt-test",
			ModelRatio:   1,
			EnableGroups: []string{"default"},
			ChannelType:  1,
		}},
	}
	report, err := DryRun(ImportRequest{Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if report.Applied || report.ChannelsToCreate != 1 {
		t.Fatalf("unexpected dry-run report: %+v", report)
	}
	if report.ManagedTagPrefix != "catalog:vector:" {
		t.Fatalf("unexpected managed tag prefix: %s", report.ManagedTagPrefix)
	}
	if len(report.MissingKeyGroups) != 1 || report.MissingKeyGroups[0] != "default" {
		t.Fatalf("expected default missing key, got %+v", report.MissingKeyGroups)
	}
}

func TestMergeSpecialPricingKeepsExistingModels(t *testing.T) {
	t.Cleanup(func() {
		_ = special_pricing.UpdateByJSONString(`{"version":"1","models":{}}`)
	})
	if err := special_pricing.UpdateByJSONString(`{
		"version":"1",
		"models":{
			"old-model":{"type":"fixed","billing_enabled":true,"credit_unit_price":0.05}
		}
	}`); err != nil {
		t.Fatal(err)
	}
	merged, err := mergeSpecialPricingOption(map[string]any{
		"models": map[string]any{
			"new-model": map[string]any{
				"type":              "fixed",
				"billing_enabled":   true,
				"credit_unit_price": 0.05,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Models map[string]any `json:"models"`
	}
	if err := json.Unmarshal([]byte(merged), &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed.Models["old-model"]; !ok {
		t.Fatalf("existing special pricing model was removed: %s", merged)
	}
	if _, ok := parsed.Models["new-model"]; !ok {
		t.Fatalf("incoming special pricing model was not added: %s", merged)
	}
}
