package catalogimport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/special_pricing"
	"github.com/QuantumNous/new-api/setting/task_billing_rules"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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

func TestParseFiveTwoOneNormalCatalog(t *testing.T) {
	input := []byte(`{
		"auto_groups":["default"],
		"vendors":[{"id":1,"name":"521"}],
		"group_ratio":{"default":1,"异步图片系列":0.25},
		"usable_group":{"default":"默认","异步图片系列":"图片"},
		"supported_endpoint":{"openai":{"path":"/v1/chat/completions","method":"POST"}},
		"data":[{
			"model_name":"Banana-pro-4k",
			"quota_type":1,
			"model_price":0.2,
			"enable_groups":["default","异步图片系列"],
			"supported_endpoint_types":["openai"]
		}]
	}`)

	catalog, err := ParseCatalog(ParseRequest{
		ProviderCode: "521",
		ProviderName: "521渠道",
		RuleType:     RuleTypeFiveTwoOneNormal,
		BaseURL:      "https://example.com",
		Content:      input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ProviderCode != "521" || len(catalog.Models) != 1 || len(catalog.Vendors) != 1 {
		t.Fatalf("unexpected catalog: %+v", catalog)
	}
	if catalog.Models[0].Name != "Banana-pro-4k" || catalog.Models[0].ModelPrice != 0.2 {
		t.Fatalf("unexpected model: %+v", catalog.Models[0])
	}
}

func TestParseVectorNormalCompilesStepRatiosToTieredBilling(t *testing.T) {
	input := []byte(`{
		"auto_groups":["default"],
		"group_ratio":{"default":1,"Codex专属":0.8},
		"data":[{
			"model_name":"gpt-5.5",
			"quota_type":0,
			"model_ratio":2.5,
			"completion_ratio":6,
			"cache_ratio":0.1,
			"enable_groups":["default","Codex专属"],
			"supported_endpoint_types":["openai"],
			"step_ratios":[
				{"step_size":272000,"completion_step_size":-1,"prompt_step_ratio":1,"completion_step_ratio":1,"cache_step_ratio":0},
				{"step_size":1000000,"completion_step_size":-1,"prompt_step_ratio":2,"completion_step_ratio":1.5,"cache_step_ratio":0}
			]
		}]
	}`)

	catalog, err := ParseCatalog(ParseRequest{
		ProviderCode: "vector",
		ProviderName: "向量",
		RuleType:     RuleTypeVectorNormal,
		BaseURL:      "https://example.com",
		Content:      input,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := catalog.BillingModes["gpt-5.5"]; got != "tiered_expr" {
		t.Fatalf("billing mode = %q, want tiered_expr", got)
	}
	expr := catalog.BillingExprs["gpt-5.5"]
	for _, want := range []string{
		`len <= 272000`,
		`tier("tier_1"`,
		`p * 5`,
		`c * 30`,
		`cr * 0.5`,
		`tier("tier_2"`,
		`p * 10`,
		`c * 45`,
		`cr * 1`,
	} {
		if !strings.Contains(expr, want) {
			t.Fatalf("billing expression %q does not contain %q", expr, want)
		}
	}
}

func TestParseVectorNormalThinkingStepRatiosDefaultToHigherPrice(t *testing.T) {
	input := []byte(`{
		"data":[{
			"model_name":"qwen-plus",
			"quota_type":0,
			"model_ratio":0.4,
			"completion_ratio":2.5,
			"enable_groups":["default"],
			"supported_endpoint_types":["openai"],
			"step_ratios":[{
				"step_size":128000,
				"completion_step_size":-1,
				"prompt_step_ratio":1,
				"completion_step_ratio":1,
				"cache_step_ratio":0,
				"prompt_thinking_step_ratio":1,
				"completion_thinking_step_ratio":4
			}]
		}]
	}`)

	catalog, err := ParseCatalog(ParseRequest{
		ProviderCode: "vector",
		ProviderName: "向量",
		RuleType:     RuleTypeVectorNormal,
		BaseURL:      "https://example.com",
		Content:      input,
	})
	if err != nil {
		t.Fatal(err)
	}
	expr := catalog.BillingExprs["qwen-plus"]
	if !strings.Contains(expr, `param("enable_thinking") == false`) {
		t.Fatalf("thinking expression must only use non-thinking price for explicit false: %s", expr)
	}
	if !strings.Contains(expr, `c * 8`) {
		t.Fatalf("thinking completion price missing from expression: %s", expr)
	}
	if !strings.Contains(expr, `c * 2`) {
		t.Fatalf("non-thinking completion price missing from expression: %s", expr)
	}
}

func TestValidateVectorRemotePricingRejectsAnyMismatch(t *testing.T) {
	catalog := &ProviderCatalog{
		ProviderCode: "vector",
		BaseURL:      "https://q.aibaotui.com",
		Groups:       map[string]string{"default": "default"},
		GroupRatios:  map[string]float64{"default": 1},
		Models: []CatalogModel{{
			Name:            "gpt-5.5",
			QuotaType:       0,
			ModelRatio:      2.5,
			CompletionRatio: 6,
			EnableGroups:    []string{"default"},
		}},
	}
	remote := []byte(`{"success":true,"data":{
		"model_completion_ratio":{"gpt-5.5":6},
		"group_special":{"gpt-5.5":["default"]},
		"model_group":{"default":{"GroupRatio":1,"ModelPrice":{
			"gpt-5.5":{"priceType":0,"price":9}
		}}}
	}}`)

	report, err := ValidateVectorRemotePricing(catalog, remote)
	if err == nil || !strings.Contains(err.Error(), "gpt-5.5") {
		t.Fatalf("expected mismatch to block import, report=%+v err=%v", report, err)
	}
	if len(report.Mismatches) != 1 {
		t.Fatalf("mismatches = %+v, want one", report.Mismatches)
	}
}

func TestValidateVectorRemotePricingAcceptsMatchingCatalog(t *testing.T) {
	catalog := &ProviderCatalog{
		ProviderCode: "vector",
		BaseURL:      "https://q.aibaotui.com",
		Groups:       map[string]string{"default": "default", "Codex专属": "codex"},
		GroupRatios:  map[string]float64{"default": 1, "Codex专属": 0.8},
		Models: []CatalogModel{{
			Name:            "gpt-5.5",
			QuotaType:       0,
			ModelRatio:      2.5,
			CompletionRatio: 6,
			EnableGroups:    []string{"default", "Codex专属"},
		}},
	}
	remote := []byte(`{"success":true,"data":{
		"model_completion_ratio":{"gpt-5.5":6},
		"group_special":{"gpt-5.5":["Codex专属","default"]},
		"model_group":{
			"default":{"GroupRatio":1,"ModelPrice":{"gpt-5.5":{"priceType":0,"price":2.5}}},
			"Codex专属":{"GroupRatio":0.8,"ModelPrice":{"gpt-5.5":{"priceType":0,"price":2.5}}}
		}
	}}`)

	report, err := ValidateVectorRemotePricing(catalog, remote)
	if err != nil {
		t.Fatal(err)
	}
	if report.CheckedModels != 1 || len(report.Mismatches) != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestValidateVectorRemotePricingAllowsRemoteDefaultGroupOnly(t *testing.T) {
	catalog := &ProviderCatalog{
		ProviderCode: "vector",
		BaseURL:      "https://q.aibaotui.com",
		Groups:       map[string]string{"default": "default", "官转OpenAI": "official"},
		GroupRatios:  map[string]float64{"default": 1, "官转OpenAI": 6},
		Models: []CatalogModel{{
			Name:            "gpt-3.5-turbo",
			QuotaType:       0,
			ModelRatio:      0.25,
			CompletionRatio: 3,
			EnableGroups:    []string{"官转OpenAI"},
		}},
	}
	remote := []byte(`{"success":true,"data":{
		"model_completion_ratio":{"gpt-3.5-turbo":3},
		"group_special":{"gpt-3.5-turbo":["default","官转OpenAI"]},
		"model_group":{
			"default":{"GroupRatio":1,"ModelPrice":{"gpt-3.5-turbo":{"priceType":0,"price":0.25}}},
			"官转OpenAI":{"GroupRatio":6,"ModelPrice":{"gpt-3.5-turbo":{"priceType":0,"price":0.25}}}
		}
	}}`)

	report, err := ValidateVectorRemotePricing(catalog, remote)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Mismatches) != 0 {
		t.Fatalf("default-only group addition should not block: %+v", report.Mismatches)
	}
}

func TestDryRunReturnsStructuredRemotePricingBlockReport(t *testing.T) {
	catalog := &ProviderCatalog{
		ProviderCode: "vector",
		BaseURL:      "https://q.aibaotui.com",
		RemotePricingReport: &RemotePricingReport{
			CheckedModels: 1,
			Mismatches: []RemotePricingMismatch{{
				ModelName: "gpt-5.5",
				Group:     "default",
				Field:     "base_price",
				Local:     2.5,
				Remote:    5,
			}},
		},
		ValidationErrors: []string{"远端价格严格校验失败"},
	}

	report, err := DryRun(ImportRequest{Catalog: catalog, GroupConflictMode: GroupConflictOverwrite})
	if err == nil {
		t.Fatal("expected dry-run to be blocked")
	}
	if len(report.BlockedReasons) != 1 || report.RemotePricingReport == nil {
		t.Fatalf("expected structured blocked report, got %+v", report)
	}
	if len(report.RemotePricingReport.Mismatches) != 1 {
		t.Fatalf("expected remote mismatch details, got %+v", report.RemotePricingReport)
	}
}

func TestBuildOptionValuesPersistsTieredBilling(t *testing.T) {
	catalog := &ProviderCatalog{
		ProviderCode: "vector",
		BaseURL:      "https://example.com",
		Models: []CatalogModel{{
			Name:            "gpt-tiered",
			ModelRatio:      2.5,
			CompletionRatio: 6,
		}},
		BillingModes: map[string]string{"gpt-tiered": "tiered_expr"},
		BillingExprs: map[string]string{"gpt-tiered": `tier("base", p * 5 + c * 30)`},
	}

	values, err := buildOptionValues(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var modes map[string]string
	if err := json.Unmarshal([]byte(values["billing_setting.billing_mode"]), &modes); err != nil {
		t.Fatal(err)
	}
	var expressions map[string]string
	if err := json.Unmarshal([]byte(values["billing_setting.billing_expr"]), &expressions); err != nil {
		t.Fatal(err)
	}
	if modes["gpt-tiered"] != "tiered_expr" || expressions["gpt-tiered"] == "" {
		t.Fatalf("tiered options missing: modes=%+v expressions=%+v", modes, expressions)
	}
}

func TestBuildOptionValuesStoresAsyncTokenPricingAsModelRatio(t *testing.T) {
	catalog := &ProviderCatalog{
		ProviderCode: "vector",
		BaseURL:      "https://example.com",
		Models: []CatalogModel{{
			Name:            "doubao-async-token",
			QuotaType:       3,
			ModelPrice:      16,
			CompletionRatio: 1,
		}},
	}

	values, err := buildOptionValues(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var ratios map[string]float64
	if err := json.Unmarshal([]byte(values["ModelRatio"]), &ratios); err != nil {
		t.Fatal(err)
	}
	var prices map[string]float64
	if err := json.Unmarshal([]byte(values["ModelPrice"]), &prices); err != nil {
		t.Fatal(err)
	}
	if got := ratios["doubao-async-token"]; got != 8 {
		t.Fatalf("async token model ratio = %v, want 8 so runtime price is 16/1M", got)
	}
	if _, ok := prices["doubao-async-token"]; ok {
		t.Fatalf("async token model must not remain in ModelPrice: %+v", prices)
	}
}

func TestFetchVectorRemotePricingUsesBaseURLPricingEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pricing" {
			t.Fatalf("path = %s, want /api/pricing", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer server.Close()

	content, err := FetchVectorRemotePricing(context.Background(), server.Client(), server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"success":true`) {
		t.Fatalf("unexpected response: %s", content)
	}
}

func TestCurrentVectorNormalFixtureCompilesAllTieredPrices(t *testing.T) {
	normal, err := os.ReadFile(filepath.Join("..", "..", "..", "向量普通规则.txt"))
	if err != nil {
		t.Skipf("normal fixture unavailable: %v", err)
	}
	catalog, err := ParseCatalog(ParseRequest{
		ProviderCode: "vector",
		ProviderName: "向量",
		RuleType:     RuleTypeVectorNormal,
		BaseURL:      "https://q.aibaotui.com",
		Content:      normal,
	})
	if err != nil {
		t.Fatal(err)
	}
	var raw vectorNormalFile
	if err := json.Unmarshal(normal, &raw); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != len(raw.Data) {
		t.Fatalf("models = %d, want %d", len(catalog.Models), len(raw.Data))
	}
	if len(catalog.BillingModes) != 45 {
		t.Fatalf("tiered billing models = %d, want 45", len(catalog.BillingModes))
	}
	for modelName, expectedPrices := range map[string][]string{
		"gpt-5.4": {"p * 2.5", "c * 15", "p * 5", "c * 22.5"},
		"gpt-5.5": {"p * 5", "c * 30", "p * 10", "c * 45"},
	} {
		expr := catalog.BillingExprs[modelName]
		for _, expected := range expectedPrices {
			if !strings.Contains(expr, expected) {
				t.Fatalf("%s expression missing %q: %s", modelName, expected, expr)
			}
		}
	}
	gpt55 := catalog.BillingExprs["gpt-5.5"]
	firstTier, _, err := billingexpr.RunExpr(gpt55, billingexpr.TokenParams{
		P: 100, C: 10, Len: 272000, CR: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstTier != 100*5+10*30+20*0.5 {
		t.Fatalf("gpt-5.5 first tier cost = %v", firstTier)
	}
	secondTier, _, err := billingexpr.RunExpr(gpt55, billingexpr.TokenParams{
		P: 100, C: 10, Len: 272001, CR: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if secondTier != 100*10+10*45+20*1 {
		t.Fatalf("gpt-5.5 second tier cost = %v", secondTier)
	}
}

func TestCompiledThinkingPriceDefaultsToHigherTier(t *testing.T) {
	model := CatalogModel{
		Name:            "thinking-model",
		ModelRatio:      0.5,
		CompletionRatio: 2,
		StepRatios: []StepRatio{{
			StepSize:                    1000,
			PromptStepRatio:             1,
			CompletionStepRatio:         1,
			PromptThinkingStepRatio:     2,
			CompletionThinkingStepRatio: 3,
		}},
	}
	expr, err := compileStepRatios(model)
	if err != nil {
		t.Fatal(err)
	}
	missingParamCost, _, err := billingexpr.RunExprWithRequest(
		expr,
		billingexpr.TokenParams{P: 100, C: 10, Len: 100},
		billingexpr.RequestInput{},
	)
	if err != nil {
		t.Fatal(err)
	}
	explicitFalseCost, _, err := billingexpr.RunExprWithRequest(
		expr,
		billingexpr.TokenParams{P: 100, C: 10, Len: 100},
		billingexpr.RequestInput{Body: []byte(`{"enable_thinking":false}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if missingParamCost != 100*2+10*6 {
		t.Fatalf("missing enable_thinking cost = %v, want higher thinking price", missingParamCost)
	}
	if explicitFalseCost != 100*1+10*2 {
		t.Fatalf("explicit false cost = %v, want non-thinking price", explicitFalseCost)
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

func TestParseVectorBundleRequiresSpecialRulesForSpecialModels(t *testing.T) {
	normal := []byte(`{
		"vendors":[],
		"data":[{
			"model_name":"viduq2",
			"quota_type":1,
			"model_price":0.016,
			"enable_groups":["default"],
			"supported_endpoint_types":["vidu文生视频"]
		}]
	}`)

	_, err := ParseVectorBundle(VectorBundleRequest{
		ProviderCode:  "vector",
		ProviderName:  "向量",
		BaseURL:       "https://example.com",
		NormalContent: normal,
	})
	if err == nil || !strings.Contains(err.Error(), "特殊规则") {
		t.Fatalf("expected missing special rules to block bundle, got %v", err)
	}
}

func TestParseVectorBundleCleansJavaScriptSpecialRules(t *testing.T) {
	normal := []byte(`{
		"vendors":[],
		"data":[{
			"model_name":"viduq2",
			"quota_type":1,
			"model_price":0.016,
			"enable_groups":["default"],
			"supported_endpoint_types":["vidu文生视频"]
		}]
	}`)
	specialJS := []byte(`const model = "viduq2"; if (model === "viduq2") { console.log("special"); }`)

	catalog, err := ParseVectorBundle(VectorBundleRequest{
		ProviderCode:   "vector",
		ProviderName:   "向量",
		BaseURL:        "https://example.com",
		NormalContent:  normal,
		SpecialContent: specialJS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := specialPricingModels(catalog.SpecialPricing)["viduq2"]; !ok {
		t.Fatalf("expected viduq2 special pricing after JS clean: %+v", catalog.SpecialPricing)
	}
	rule, ok := catalog.TaskBillingRules["viduq2"]
	if !ok || rule.Mode != task_billing_rules.ModeSpecial {
		t.Fatalf("expected special task billing rule, got %+v", catalog.TaskBillingRules)
	}
}

func TestParseVectorBundleAlignsCatalogToRemotePricing(t *testing.T) {
	normal := []byte(`{
		"group_ratio":{"default":1,"stale":0.5},
		"usable_group":{"default":"默认","stale":"过期"},
		"data":[{
			"model_name":"gpt-live",
			"quota_type":0,
			"model_ratio":0.1,
			"completion_ratio":2,
			"enable_groups":["default","stale"],
			"supported_endpoint_types":["openai"]
		},{
			"model_name":"gpt-removed",
			"quota_type":0,
			"model_ratio":0.2,
			"completion_ratio":2,
			"enable_groups":["default"],
			"supported_endpoint_types":["openai"]
		}]
	}`)
	remote := []byte(`{"success":true,"data":{
		"model_completion_ratio":{"gpt-live":3},
		"group_special":{"gpt-live":["default"]},
		"model_group":{"default":{"GroupRatio":1,"ModelPrice":{
			"gpt-live":{"priceType":0,"price":0.25}
		}}}
	}}`)

	catalog, err := ParseVectorBundle(VectorBundleRequest{
		ProviderCode:         "vector",
		ProviderName:         "向量",
		BaseURL:              "https://q.aibaotui.com",
		NormalContent:        normal,
		SpecialContent:       []byte(`{"version":"1","models":{}}`),
		RemotePricingContent: remote,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.ValidationErrors) != 0 {
		t.Fatalf("remote-aligned catalog should validate cleanly: %+v", catalog.ValidationErrors)
	}
	if len(catalog.Models) != 1 || catalog.Models[0].Name != "gpt-live" {
		t.Fatalf("expected removed model to be pruned, got %+v", catalog.Models)
	}
	if got := catalog.Models[0].EnableGroups; len(got) != 1 || got[0] != "default" {
		t.Fatalf("expected stale group to be pruned, got %+v", got)
	}
	if catalog.Models[0].ModelRatio != 0.25 || catalog.Models[0].CompletionRatio != 3 {
		t.Fatalf("expected remote price and completion ratio, got %+v", catalog.Models[0])
	}
}

func TestParseVectorBundleRequiresSpecialRuleForEveryDurationPricedModel(t *testing.T) {
	normal := []byte(`{
		"data":[{
			"model_name":"future-provider-video",
			"quota_type":4,
			"model_price":0.1,
			"enable_groups":["default"],
			"supported_endpoint_types":["openai"]
		}]
	}`)

	_, err := ParseVectorBundle(VectorBundleRequest{
		ProviderCode:  "vector",
		ProviderName:  "向量",
		BaseURL:       "https://example.com",
		NormalContent: normal,
	})
	if err == nil || !strings.Contains(err.Error(), "必须同时上传特殊规则") {
		t.Fatalf("quota_type=4 without special pricing must block import, got %v", err)
	}
}

func TestParseVectorBundleRejectsSpecialModelMissingFromNormalCatalog(t *testing.T) {
	normal := []byte(`{
		"vendors":[],
		"data":[{
			"model_name":"grok-video-3-10s",
			"quota_type":1,
			"model_price":0.6,
			"enable_groups":["default"],
			"supported_endpoint_types":["grok视频"]
		}]
	}`)
	special := []byte(`{
		"version":"1",
		"models":{
			"viduq2":{
				"type":"fixed",
				"billing_enabled":true,
				"credit_unit_price":0.05
			}
		}
	}`)

	_, err := ParseVectorBundle(VectorBundleRequest{
		ProviderCode:   "vector",
		ProviderName:   "向量",
		BaseURL:        "https://example.com",
		NormalContent:  normal,
		SpecialContent: special,
	})
	if err == nil || !strings.Contains(err.Error(), "不存在于普通规则") {
		t.Fatalf("expected special/normal mismatch to block bundle, got %v", err)
	}
}

func TestParseVectorBundleGeneratesTaskBillingRules(t *testing.T) {
	normal := []byte(`{
		"vendors":[],
		"data":[{
			"model_name":"grok-video-3-10s",
			"quota_type":1,
			"model_price":0.6,
			"enable_groups":["default"],
			"supported_endpoint_types":["grok视频"]
		}]
	}`)
	special := []byte(`{"version":"1","models":{}}`)

	catalog, err := ParseVectorBundle(VectorBundleRequest{
		ProviderCode:   "vector",
		ProviderName:   "向量",
		BaseURL:        "https://example.com",
		NormalContent:  normal,
		SpecialContent: special,
	})
	if err != nil {
		t.Fatal(err)
	}
	rule, ok := catalog.TaskBillingRules["grok-video-3-10s"]
	if !ok {
		t.Fatal("expected fixed-duration task billing rule")
	}
	if rule.Mode != "per_call" || rule.FixedDuration != 10 || len(rule.RatioKeys) != 0 {
		t.Fatalf("unexpected task billing rule: %+v", rule)
	}
}

func TestParseVectorBundleGeneratesFixedPriceTaskBillingRules(t *testing.T) {
	normal := []byte(`{
		"vendors":[],
		"data":[
			{
				"model_name":"supplier-video-fixed-vip",
				"description":"供应商固定价视频模型（时长8秒）",
				"quota_type":1,
				"model_price":1.7,
				"enable_groups":["default"],
				"supported_endpoint_types":["openAI视频格式"]
			},
			{
				"model_name":"another-video-model",
				"description":"另一个固定价视频模型，6S。",
				"quota_type":1,
				"model_price":1.6,
				"enable_groups":["default"],
				"supported_endpoint_types":["openAI视频格式"]
			}
		]
	}`)
	special := []byte(`{"version":"1","models":{}}`)

	catalog, err := ParseVectorBundle(VectorBundleRequest{
		ProviderCode:   "vector",
		ProviderName:   "向量",
		BaseURL:        "https://example.com",
		NormalContent:  normal,
		SpecialContent: special,
	})
	if err != nil {
		t.Fatal(err)
	}
	for modelName, fixedDuration := range map[string]int{
		"supplier-video-fixed-vip": 8,
		"another-video-model":      6,
	} {
		rule, ok := catalog.TaskBillingRules[modelName]
		if !ok {
			t.Fatalf("expected fixed-duration task billing rule for %s", modelName)
		}
		if rule.Mode != task_billing_rules.ModePerCall || rule.FixedDuration != fixedDuration || len(rule.RatioKeys) != 0 {
			t.Fatalf("unexpected task billing rule for %s: %+v", modelName, rule)
		}
	}
}

func TestCurrentVectorFixturesBuildCompleteBundle(t *testing.T) {
	normalPath := filepath.Join("..", "..", "..", "向量普通规则.txt")
	specialPath := filepath.Join("..", "..", "tmp", "special-clean", "SpecialModelPricing.cleaned.json")
	normal, err := os.ReadFile(normalPath)
	if err != nil {
		t.Skipf("normal fixture unavailable: %v", err)
	}
	special, err := os.ReadFile(specialPath)
	if err != nil {
		t.Skipf("cleaned special fixture unavailable: %v", err)
	}
	catalog, err := ParseVectorBundle(VectorBundleRequest{
		ProviderCode:   "vector",
		ProviderName:   "向量",
		BaseURL:        "https://q.aibaotui.com",
		NormalContent:  normal,
		SpecialContent: special,
	})
	if err != nil {
		t.Skipf("local vector fixture pair is inconsistent: %v", err)
	}
	rule := catalog.TaskBillingRules["grok-video-3-10s"]
	if rule.Mode != "per_call" || rule.FixedDuration != 10 {
		t.Fatalf("unexpected grok fixed-price rule: %+v", rule)
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
	report, err := DryRun(ImportRequest{Catalog: catalog, GroupConflictMode: GroupConflictOverwrite})
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

func TestDryRunWithGroupKeysFiltersCatalogToProvidedGroups(t *testing.T) {
	catalog := &ProviderCatalog{
		ProviderCode: "521",
		ProviderName: "521渠道",
		BaseURL:      "https://example.com",
		AutoGroups:   []string{"default", "画图"},
		Groups: map[string]string{
			"default": "默认",
			"画图":      "图片",
			"Claude":  "Claude",
		},
		GroupRatios: map[string]float64{
			"default": 1,
			"画图":      0.25,
			"Claude":  4,
		},
		Models: []CatalogModel{{
			Name:                   "image-model",
			QuotaType:              1,
			ModelPrice:             0.2,
			EnableGroups:           []string{"default", "画图"},
			SupportedEndpointTypes: []string{"openai"},
		}, {
			Name:                   "claude-model",
			QuotaType:              0,
			ModelRatio:             1,
			CompletionRatio:        2,
			EnableGroups:           []string{"Claude"},
			SupportedEndpointTypes: []string{"anthropic"},
		}},
	}

	report, err := DryRun(ImportRequest{
		Catalog: catalog,
		GroupKeys: []TokenRow{{
			RowNumber: 2,
			Name:      "画图",
			Status:    "已启用",
			Group:     "画图",
			Key:       "sk-test",
			Models:    "无限制",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Groups != 1 || report.Models != 1 || report.ChannelsToCreate != 1 {
		t.Fatalf("unexpected filtered report: %+v", report)
	}
	if _, ok := catalog.Groups["default"]; !ok {
		t.Fatalf("dry-run should not mutate original catalog: %+v", catalog.Groups)
	}
	if len(report.MissingKeyGroups) != 0 {
		t.Fatalf("provided key group should not be missing: %+v", report.MissingKeyGroups)
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
	}, nil)
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

func setupCatalogImportTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/catalog-import-test.db"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Vendor{}, &model.Model{}); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func setupCatalogApplyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/catalog-apply-test.db"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.Vendor{},
		&model.Model{},
		&model.Channel{},
		&model.Ability{},
		&model.Option{},
	); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	model.InitOptionMap()
	return db
}

func TestUpsertModelsUsesActualVendorID(t *testing.T) {
	db := setupCatalogImportTestDB(t)
	requireNoError := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	requireNoError(db.Create(&model.Vendor{
		Id:          10,
		Name:        "OpenAI",
		Description: "old",
		Status:      1,
	}).Error)

	catalog := &ProviderCatalog{
		Vendors: []CatalogVendor{{
			ID:          1,
			Name:        "OpenAI",
			Description: "new",
			Icon:        "OpenAI.Color",
		}},
		Models: []CatalogModel{{
			Name:        "gpt-test",
			Description: "desc",
			VendorID:    1,
		}},
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		vendorIDMap, err := upsertVendorsTx(tx, catalog)
		if err != nil {
			return err
		}
		return upsertModelsTx(tx, catalog, vendorIDMap)
	})
	requireNoError(err)

	var saved model.Model
	requireNoError(db.Where("model_name = ?", "gpt-test").First(&saved).Error)
	if saved.VendorID != 10 {
		t.Fatalf("expected model vendor_id to use existing DB vendor id 10, got %d", saved.VendorID)
	}
}

func TestApplyReplacesOnlySameProviderAndBaseURLChannels(t *testing.T) {
	db := setupCatalogApplyTestDB(t)
	key := "sk-preserved"
	priority := int64(0)
	weight := uint(0)
	baseURL := "https://example.com"
	otherBaseURL := "https://other.example.com"
	manualTag := "manual"
	vectorTag := ProviderGroupTag("vector", "default", constant.ChannelTypeOpenAI, "openai", nil, nil)
	otherBaseTag := ProviderGroupTag("vector", "default", constant.ChannelTypeOpenAI, "openai", nil, nil)
	otherProviderTag := ProviderGroupTag("other", "default", constant.ChannelTypeOpenAI, "openai", nil, nil)

	existing := []model.Channel{
		{Type: constant.ChannelTypeOpenAI, Key: key, Status: common.ChannelStatusEnabled, Name: "old same base", BaseURL: &baseURL, Models: "old-model", Group: "default", Tag: &vectorTag, Priority: &priority, Weight: &weight},
		{Type: constant.ChannelTypeOpenAI, Key: "sk-other-base", Status: common.ChannelStatusEnabled, Name: "old other base", BaseURL: &otherBaseURL, Models: "other-base-model", Group: "default", Tag: &otherBaseTag, Priority: &priority, Weight: &weight},
		{Type: constant.ChannelTypeOpenAI, Key: "sk-other-provider", Status: common.ChannelStatusEnabled, Name: "old other provider", BaseURL: &baseURL, Models: "other-provider-model", Group: "default", Tag: &otherProviderTag, Priority: &priority, Weight: &weight},
		{Type: constant.ChannelTypeOpenAI, Key: "sk-manual", Status: common.ChannelStatusEnabled, Name: "manual", BaseURL: &baseURL, Models: "manual-model", Group: "default", Tag: &manualTag, Priority: &priority, Weight: &weight},
	}
	for i := range existing {
		if err := db.Create(&existing[i]).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.Ability{Group: "default", Model: existing[i].Models, ChannelId: existing[i].Id, Enabled: true, Tag: existing[i].Tag}).Error; err != nil {
			t.Fatal(err)
		}
	}

	report, err := Apply(ImportRequest{Catalog: &ProviderCatalog{
		ProviderCode: "vector",
		ProviderName: "向量",
		BaseURL:      baseURL,
		Models: []CatalogModel{{
			Name:                   "new-model",
			ModelRatio:             1,
			EnableGroups:           []string{"default"},
			SupportedEndpointTypes: []string{"openai"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Applied || report.ChannelsToReplace != 1 {
		var debug []model.Channel
		_ = db.Find(&debug).Error
		for _, channel := range debug {
			t.Logf("channel id=%d tag=%s base=%s models=%s", channel.Id, stringValue(channel.Tag), stringValue(channel.BaseURL), channel.Models)
		}
		t.Fatalf("unexpected apply report: %+v", report)
	}
	var channels []model.Channel
	if err := db.Order("name").Find(&channels).Error; err != nil {
		t.Fatal(err)
	}
	if len(channels) != 4 {
		t.Fatalf("expected only same-base provider channel replaced, got %d channels: %+v", len(channels), channels)
	}
	var newChannel model.Channel
	if err := db.Where("tag LIKE ? AND base_url = ?", ProviderTagPrefix("vector")+"%", baseURL).First(&newChannel).Error; err != nil {
		t.Fatal(err)
	}
	if newChannel.Models != "new-model" || newChannel.Key != key {
		t.Fatalf("expected rebuilt channel with preserved key, got %+v", newChannel)
	}
	var oldAbilityCount int64
	if err := db.Model(&model.Ability{}).Where("model = ?", "old-model").Count(&oldAbilityCount).Error; err != nil {
		t.Fatal(err)
	}
	if oldAbilityCount != 0 {
		t.Fatalf("old same-base ability was not removed")
	}
	var protectedCount int64
	if err := db.Model(&model.Channel{}).
		Where("models IN ?", []string{"other-base-model", "other-provider-model", "manual-model"}).
		Count(&protectedCount).Error; err != nil {
		t.Fatal(err)
	}
	if protectedCount != 3 {
		t.Fatalf("unrelated channels were modified, remaining protected=%d", protectedCount)
	}
}

func TestDryRunMarksConflictingGroupsAndKeepsKeyBinding(t *testing.T) {
	catalog := &ProviderCatalog{
		ProviderCode: "521",
		ProviderName: "521渠道",
		BaseURL:      "https://example.com",
		Groups:       map[string]string{"default": "供应商默认", "图片": "图片分组"},
		GroupRatios:  map[string]float64{"default": 2, "图片": 0.5},
		AutoGroups:   []string{"default", "图片"},
		Models: []CatalogModel{{
			Name:                   "image-model",
			ModelPrice:             0.2,
			EnableGroups:           []string{"default", "图片"},
			SupportedEndpointTypes: []string{"openai"},
		}},
	}

	report, err := DryRun(ImportRequest{
		Catalog: catalog,
		GroupKeys: []TokenRow{{
			RowNumber: 2,
			Name:      "521 default",
			Status:    "已启用",
			Group:     "default",
			Key:       "sk-default",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.GroupConflictMode != GroupConflictMark {
		t.Fatalf("expected mark mode by default, got %s", report.GroupConflictMode)
	}
	if report.GroupNameMappings["default"] != "521渠道:default" {
		t.Fatalf("expected default group to be marked, got %+v", report.GroupNameMappings)
	}
	if len(report.MissingKeyGroups) != 0 {
		t.Fatalf("marked group should keep original key binding, missing=%+v", report.MissingKeyGroups)
	}
	if _, ok := catalog.Groups["521渠道:default"]; ok {
		t.Fatal("dry-run mutated original catalog")
	}
}

func TestDryRunOverwriteKeepsConflictingGroupName(t *testing.T) {
	catalog := &ProviderCatalog{
		ProviderCode: "521",
		ProviderName: "521渠道",
		BaseURL:      "https://example.com",
		Groups:       map[string]string{"default": "供应商默认"},
		GroupRatios:  map[string]float64{"default": 2},
		Models: []CatalogModel{{
			Name:                   "image-model",
			ModelPrice:             0.2,
			EnableGroups:           []string{"default"},
			SupportedEndpointTypes: []string{"openai"},
		}},
	}

	report, err := DryRun(ImportRequest{
		Catalog:           catalog,
		GroupConflictMode: GroupConflictOverwrite,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.GroupConflictMode != GroupConflictOverwrite {
		t.Fatalf("expected overwrite mode, got %s", report.GroupConflictMode)
	}
	if len(report.GroupNameMappings) != 0 {
		t.Fatalf("overwrite mode should not mark groups, got %+v", report.GroupNameMappings)
	}
	if len(report.MissingKeyGroups) != 1 || report.MissingKeyGroups[0] != "default" {
		t.Fatalf("expected missing key for original default group, got %+v", report.MissingKeyGroups)
	}
}

func TestApplyMarkedGroupPreservesExistingProviderKey(t *testing.T) {
	db := setupCatalogApplyTestDB(t)
	key := "sk-existing"
	priority := int64(0)
	weight := uint(0)
	baseURL := "https://example.com"
	oldTag := ProviderGroupTag("521", "default", constant.ChannelTypeOpenAI, "openai", nil, nil)
	if err := db.Create(&model.Channel{
		Type:        constant.ChannelTypeOpenAI,
		Key:         key,
		Status:      common.ChannelStatusEnabled,
		Name:        "old 521 default",
		BaseURL:     &baseURL,
		Models:      "old-model",
		Group:       "default",
		Tag:         &oldTag,
		Priority:    &priority,
		Weight:      &weight,
		CreatedTime: common.GetTimestamp(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	report, err := Apply(ImportRequest{Catalog: &ProviderCatalog{
		ProviderCode: "521",
		ProviderName: "521渠道",
		BaseURL:      baseURL,
		Groups:       map[string]string{"default": "供应商默认"},
		GroupRatios:  map[string]float64{"default": 2},
		Models: []CatalogModel{{
			Name:                   "new-model",
			ModelPrice:             0.2,
			EnableGroups:           []string{"default"},
			SupportedEndpointTypes: []string{"openai"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.GroupNameMappings["default"] != "521渠道:default" {
		t.Fatalf("expected default to be marked, got %+v", report.GroupNameMappings)
	}
	var channel model.Channel
	if err := db.Where("models = ?", "new-model").First(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if channel.Group != "521渠道:default" || channel.Key != key {
		t.Fatalf("expected marked group with preserved key, got group=%s key=%s", channel.Group, channel.Key)
	}
	var ability model.Ability
	if err := db.Where("model = ?", "new-model").First(&ability).Error; err != nil {
		t.Fatal(err)
	}
	if ability.Group != "521渠道:default" {
		t.Fatalf("expected ability to use marked group, got %s", ability.Group)
	}
}

func TestClearManagedCatalogDryRunAndApply(t *testing.T) {
	db := setupCatalogApplyTestDB(t)
	priority := int64(0)
	weight := uint(0)
	baseURL := "https://example.com"
	managedTag := ProviderGroupTag("521", "521渠道:default", constant.ChannelTypeOpenAI, "openai", nil, nil)
	manualTag := "manual"
	managed := model.Channel{
		Type:     constant.ChannelTypeOpenAI,
		Key:      "sk-managed",
		Status:   common.ChannelStatusEnabled,
		Name:     "managed",
		BaseURL:  &baseURL,
		Models:   "managed-model",
		Group:    "521渠道:default",
		Tag:      &managedTag,
		Priority: &priority,
		Weight:   &weight,
	}
	manual := model.Channel{
		Type:     constant.ChannelTypeOpenAI,
		Key:      "sk-manual",
		Status:   common.ChannelStatusEnabled,
		Name:     "manual",
		BaseURL:  &baseURL,
		Models:   "manual-model",
		Group:    "default",
		Tag:      &manualTag,
		Priority: &priority,
		Weight:   &weight,
	}
	if err := db.Create(&managed).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Ability{Group: managed.Group, Model: "managed-model", ChannelId: managed.Id, Enabled: true, Tag: &managedTag}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Ability{Group: manual.Group, Model: "manual-model", ChannelId: manual.Id, Enabled: true, Tag: &manualTag}).Error; err != nil {
		t.Fatal(err)
	}
	common.OptionMap["ModelPrice"] = `{"managed-model":0.2,"manual-model":0.3}`
	common.OptionMap["ModelRatio"] = `{"managed-model":1,"manual-model":2}`
	common.OptionMap["CompletionRatio"] = `{"managed-model":2,"manual-model":3}`
	common.OptionMap["CacheRatio"] = `{"managed-model":0.1,"manual-model":0.2}`
	common.OptionMap["CreateCacheRatio"] = `{"managed-model":0.1,"manual-model":0.2}`
	common.OptionMap["AudioCompletionRatio"] = `{"managed-model":1.5,"manual-model":2.5}`
	common.OptionMap["GroupRatio"] = `{"521渠道:default":2,"default":1}`
	common.OptionMap["UserUsableGroups"] = `{"521渠道:default":"供应商默认","default":"默认"}`
	common.OptionMap[special_pricing.OptionKey] = `{"version":"1","models":{"managed-model":{"type":"fixed"},"manual-model":{"type":"fixed"}}}`
	common.OptionMap[task_billing_rules.OptionKey] = `{"managed-model":{"mode":"per_call"},"manual-model":{"mode":"per_call"}}`
	common.OptionMap["billing_setting.billing_mode"] = `{"managed-model":"tiered_expr","manual-model":"tiered_expr"}`
	common.OptionMap["billing_setting.billing_expr"] = `{"managed-model":"1","manual-model":"1"}`
	common.OptionMap[ProviderImportStateOptionKey("521", baseURL)] = `{
		"special_pricing_models":["managed-model"],
		"task_billing_models":["managed-model"],
		"tiered_billing_models":["managed-model"]
	}`
	t.Cleanup(func() {
		for _, key := range []string{
			"ModelPrice", "ModelRatio", "CompletionRatio", "CacheRatio", "CreateCacheRatio",
			"AudioCompletionRatio", "GroupRatio", "UserUsableGroups", special_pricing.OptionKey,
			task_billing_rules.OptionKey, "billing_setting.billing_mode", "billing_setting.billing_expr",
			ProviderImportStateOptionKey("521", baseURL),
		} {
			delete(common.OptionMap, key)
		}
	})

	preview, err := ClearManagedCatalog(ClearRequest{ProviderCode: "521", BaseURL: baseURL})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Applied || preview.ChannelsToDelete != 1 || preview.AbilitiesToDelete != 1 {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	var channelCount int64
	if err := db.Model(&model.Channel{}).Count(&channelCount).Error; err != nil {
		t.Fatal(err)
	}
	if channelCount != 2 {
		t.Fatalf("dry-run modified channels, count=%d", channelCount)
	}

	applied, err := ClearManagedCatalog(ClearRequest{ProviderCode: "521", BaseURL: baseURL, Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Applied || applied.ChannelsToDelete != 1 || applied.GroupsToDelete != 1 {
		t.Fatalf("unexpected apply report: %+v", applied)
	}
	var remaining []model.Channel
	if err := db.Find(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].Name != "manual" {
		t.Fatalf("manual channel should remain only, got %+v", remaining)
	}
	var abilities int64
	if err := db.Model(&model.Ability{}).Count(&abilities).Error; err != nil {
		t.Fatal(err)
	}
	if abilities != 1 {
		t.Fatalf("manual ability should remain only, got %d", abilities)
	}
	values := common.OptionMap
	if strings.Contains(values["ModelPrice"], "managed-model") || !strings.Contains(values["ModelPrice"], "manual-model") {
		t.Fatalf("model price cleanup incorrect: %s", values["ModelPrice"])
	}
	if strings.Contains(values["UserUsableGroups"], "521渠道:default") || !strings.Contains(values["UserUsableGroups"], "default") {
		t.Fatalf("user groups cleanup incorrect: %s", values["UserUsableGroups"])
	}
	if strings.Contains(values[special_pricing.OptionKey], "managed-model") || !strings.Contains(values[special_pricing.OptionKey], "manual-model") {
		t.Fatalf("special pricing cleanup incorrect: %s", values[special_pricing.OptionKey])
	}
}

func TestPostgresVendorSequenceIsSyncedBeforeImport(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("set TEST_POSTGRES_DSN to run postgres sequence regression test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	requireNoError := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	requireNoError(db.Exec("DROP TABLE IF EXISTS models").Error)
	requireNoError(db.Exec("DROP TABLE IF EXISTS vendors").Error)
	requireNoError(db.AutoMigrate(&model.Vendor{}, &model.Model{}))
	requireNoError(db.Create(&model.Vendor{Id: 100, Name: "Existing", Status: 1}).Error)
	requireNoError(db.Exec("SELECT setval(pg_get_serial_sequence('vendors', 'id'), 1, false)").Error)

	catalog := &ProviderCatalog{
		Vendors: []CatalogVendor{{ID: 1, Name: "NewVendor"}},
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		_, err := upsertVendorsTx(tx, catalog)
		return err
	})
	requireNoError(err)

	var saved model.Vendor
	requireNoError(db.Where("name = ?", "NewVendor").First(&saved).Error)
	if saved.Id <= 100 {
		t.Fatalf("expected synced sequence to allocate id > 100, got %d", saved.Id)
	}
}

func TestPostgresVectorBundleApplyWritesCatalogAndOptionsTogether(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("set TEST_POSTGRES_DSN to run postgres bundle apply test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	for _, table := range []string{"abilities", "channels", "models", "vendors", "options"} {
		if err := db.Exec("DROP TABLE IF EXISTS " + table + " CASCADE").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AutoMigrate(
		&model.Vendor{},
		&model.Model{},
		&model.Channel{},
		&model.Ability{},
		&model.Option{},
	); err != nil {
		t.Fatal(err)
	}
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	model.InitOptionMap()

	catalog, err := ParseVectorBundle(VectorBundleRequest{
		ProviderCode: "vector",
		ProviderName: "向量",
		BaseURL:      "https://example.com",
		NormalContent: []byte(`{
			"auto_groups":["default"],
			"vendors":[{"id":1,"name":"Grok"}],
			"group_ratio":{"default":1},
			"usable_group":{"default":"默认"},
			"supported_endpoint":{"grok视频":{"path":"/v1/video/create","method":"POST"}},
			"data":[{
				"model_name":"grok-video-3-10s",
				"vendor_id":1,
				"quota_type":1,
				"model_price":0.6,
				"enable_groups":["default"],
				"supported_endpoint_types":["grok视频"]
			}]
		}`),
		SpecialContent: []byte(`{"version":"1","models":{}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := Apply(ImportRequest{Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Applied {
		t.Fatalf("expected applied report: %+v", report)
	}
	var channelCount int64
	if err := db.Model(&model.Channel{}).
		Where("tag LIKE ?", "catalog:vector:%").
		Count(&channelCount).Error; err != nil {
		t.Fatal(err)
	}
	if channelCount != 1 {
		t.Fatalf("expected one managed channel, got %d", channelCount)
	}
	var option model.Option
	if err := db.Where("key = ?", "TaskBillingRules").First(&option).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(option.Value, `"grok-video-3-10s"`) {
		t.Fatalf("task billing option missing model: %s", option.Value)
	}
}

func TestPostgresVectorBundleApplyRollsBackCatalogWhenOptionWriteFails(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("set TEST_POSTGRES_DSN to run postgres bundle rollback test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	for _, table := range []string{"abilities", "channels", "models", "vendors", "options"} {
		if err := db.Exec("DROP TABLE IF EXISTS " + table + " CASCADE").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AutoMigrate(
		&model.Vendor{},
		&model.Model{},
		&model.Channel{},
		&model.Ability{},
		&model.Option{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DROP TABLE options").Error; err != nil {
		t.Fatal(err)
	}
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	model.InitOptionMap()

	catalog, err := ParseVectorBundle(VectorBundleRequest{
		ProviderCode: "vector",
		ProviderName: "向量",
		BaseURL:      "https://example.com",
		NormalContent: []byte(`{
			"vendors":[{"id":1,"name":"Grok"}],
			"group_ratio":{"default":1},
			"usable_group":{"default":"默认"},
			"supported_endpoint":{"grok视频":{"path":"/v1/video/create","method":"POST"}},
			"data":[{
				"model_name":"grok-video-3-10s",
				"vendor_id":1,
				"quota_type":1,
				"model_price":0.6,
				"enable_groups":["default"],
				"supported_endpoint_types":["grok视频"]
			}]
		}`),
		SpecialContent: []byte(`{"version":"1","models":{}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ImportRequest{Catalog: catalog}); err == nil {
		t.Fatal("expected option write failure")
	}
	var modelCount, channelCount int64
	if err := db.Model(&model.Model{}).Count(&modelCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Channel{}).Count(&channelCount).Error; err != nil {
		t.Fatal(err)
	}
	if modelCount != 0 || channelCount != 0 {
		t.Fatalf("catalog transaction was not rolled back: models=%d channels=%d", modelCount, channelCount)
	}
}

func TestBuildOptionValuesReplacesPreviousProviderBillingRules(t *testing.T) {
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	originalSpecial := special_pricing.ToJSONString()
	originalTaskRules := task_billing_rules.ToJSONString()
	t.Cleanup(func() {
		_ = special_pricing.UpdateByJSONString(originalSpecial)
		_ = task_billing_rules.UpdateByJSONString(originalTaskRules)
	})
	if err := special_pricing.UpdateByJSONString(`{
		"version":"1",
		"models":{
			"old-vector-special":{"billing_enabled":true},
			"other-provider-special":{"billing_enabled":true}
		}
	}`); err != nil {
		t.Fatal(err)
	}
	if err := task_billing_rules.UpdateByJSONString(`{
		"old-vector-task":{"mode":"per_call"},
		"other-provider-task":{"mode":"per_call"}
	}`); err != nil {
		t.Fatal(err)
	}
	common.OptionMap[ProviderImportStateOptionKey("vector", "https://example.com")] = `{
		"special_pricing_models":["old-vector-special"],
		"task_billing_models":["old-vector-task"]
	}`
	t.Cleanup(func() { delete(common.OptionMap, ProviderImportStateOptionKey("vector", "https://example.com")) })

	values, err := buildOptionValues(&ProviderCatalog{
		ProviderCode: "vector",
		BaseURL:      "https://example.com",
		SpecialPricing: map[string]any{
			"version": "1",
			"models": map[string]any{
				"new-vector-special": map[string]any{"billing_enabled": true},
			},
		},
		TaskBillingRules: map[string]task_billing_rules.Rule{
			"new-vector-task": {Mode: task_billing_rules.ModePerCall},
		},
		SourceHashes: map[string]string{"normal": "new-normal"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var special map[string]any
	if err := json.Unmarshal([]byte(values[special_pricing.OptionKey]), &special); err != nil {
		t.Fatal(err)
	}
	models := specialPricingModels(special)
	if _, ok := models["old-vector-special"]; ok {
		t.Fatal("old provider special rule was not removed")
	}
	if _, ok := models["other-provider-special"]; !ok {
		t.Fatal("other provider special rule must be preserved")
	}
	if _, ok := models["new-vector-special"]; !ok {
		t.Fatal("new provider special rule was not added")
	}

	var taskRules map[string]task_billing_rules.Rule
	if err := json.Unmarshal([]byte(values[task_billing_rules.OptionKey]), &taskRules); err != nil {
		t.Fatal(err)
	}
	if _, ok := taskRules["old-vector-task"]; ok {
		t.Fatal("old provider task rule was not removed")
	}
	if _, ok := taskRules["other-provider-task"]; !ok {
		t.Fatal("other provider task rule must be preserved")
	}
	if _, ok := taskRules["new-vector-task"]; !ok {
		t.Fatal("new provider task rule was not added")
	}
	if _, ok := values[ProviderImportStateOptionKey("vector", "https://example.com")]; !ok {
		t.Fatal("provider import state must be persisted atomically")
	}
}

func TestBuildChannelPlansSplitsModelsByEndpoint(t *testing.T) {
	catalog := &ProviderCatalog{
		ProviderCode: "vector",
		ProviderName: "向量",
		BaseURL:      "https://example.com",
		Models: []CatalogModel{{
			Name:                   "gemini-3.5-flash",
			EnableGroups:           []string{"优质gemini"},
			SupportedEndpointTypes: []string{"gemini", "openai", "anthropic"},
		}},
	}
	plans := buildChannelPlans(catalog, nil)
	if len(plans) != 3 {
		t.Fatalf("expected 3 endpoint channel plans, got %d: %+v", len(plans), plans)
	}
	byType := map[int]channelPlan{}
	for _, plan := range plans {
		byType[plan.Type] = plan
		if len(plan.Models) != 1 || plan.Models[0] != "gemini-3.5-flash" {
			t.Fatalf("unexpected models in plan: %+v", plan)
		}
	}
	if byType[24].Endpoint != "gemini" {
		t.Fatalf("expected Gemini endpoint plan, got %+v", byType[24])
	}
	if byType[1].Endpoint != "openai" {
		t.Fatalf("expected OpenAI endpoint plan, got %+v", byType[1])
	}
	if byType[14].Endpoint != "anthropic" {
		t.Fatalf("expected Anthropic endpoint plan, got %+v", byType[14])
	}
}

func TestEndpointResolverUsesEndpointPathBeforeLabel(t *testing.T) {
	catalog := &ProviderCatalog{
		ProviderCode: "vector",
		ProviderName: "向量",
		BaseURL:      "https://example.com",
		Models: []CatalogModel{
			{
				Name:                   "qwen-image-2.0",
				ModelType:              "图像",
				EnableGroups:           []string{"default"},
				SupportedEndpointTypes: []string{"images-generations"},
				EndpointMap: map[string]any{
					"images-generations": map[string]any{"path": "/v1/images/generations", "method": "POST"},
				},
			},
			{
				Name:                   "mj_imagine",
				EnableGroups:           []string{"default"},
				SupportedEndpointTypes: []string{"mj想象模式"},
				EndpointMap: map[string]any{
					"mj想象模式": map[string]any{"path": "/mj/submit/imagine", "method": "POST"},
				},
			},
			{
				Name:                   "pixverse-video",
				ModelType:              "音视频",
				EnableGroups:           []string{"default"},
				SupportedEndpointTypes: []string{"pix文生视频"},
				EndpointMap: map[string]any{
					"pix文生视频": map[string]any{"path": "/openapi/v2/video/text/generate", "method": "POST"},
				},
			},
			{
				Name:                   "happyhorse-1.0-i2v",
				ModelType:              "音视频",
				EnableGroups:           []string{"default"},
				SupportedEndpointTypes: []string{"happyhorse视频"},
				EndpointMap: map[string]any{
					"happyhorse视频": map[string]any{"path": "/alibailian/api/v1/services/aigc/video-generation/video-synthesis", "method": "POST"},
				},
			},
		},
	}

	plans := buildChannelPlans(catalog, nil)
	byModel := map[string]channelPlan{}
	for _, plan := range plans {
		for _, modelName := range plan.Models {
			byModel[modelName] = plan
		}
	}
	if byModel["qwen-image-2.0"].Type != constant.ChannelTypeOpenAI {
		t.Fatalf("expected OpenAI image channel, got %+v", byModel["qwen-image-2.0"])
	}
	if byModel["mj_imagine"].Type != constant.ChannelTypeMidjourney {
		t.Fatalf("expected Midjourney channel, got %+v", byModel["mj_imagine"])
	}
	if byModel["pixverse-video"].Type != constant.ChannelTypeCustom {
		t.Fatalf("expected Custom channel for PixVerse proxy path, got %+v", byModel["pixverse-video"])
	}
	if byModel["happyhorse-1.0-i2v"].Type != constant.ChannelTypeAli {
		t.Fatalf("expected Ali channel from alibailian path, got %+v", byModel["happyhorse-1.0-i2v"])
	}
}

func TestDryRunReportsResolvedAndUnresolvedEndpoints(t *testing.T) {
	catalog := &ProviderCatalog{
		ProviderCode: "vector",
		ProviderName: "向量",
		BaseURL:      "https://example.com",
		Models: []CatalogModel{
			{
				Name:                   "text-embedding-3-small",
				EnableGroups:           []string{"default"},
				SupportedEndpointTypes: []string{"嵌入"},
				EndpointMap: map[string]any{
					"嵌入": map[string]any{"path": "/v1/embeddings", "method": "POST"},
				},
			},
			{
				Name:                   "unknown-endpoint-model",
				EnableGroups:           []string{"default"},
				SupportedEndpointTypes: []string{"not-a-real-endpoint"},
			},
		},
	}

	report, err := DryRun(ImportRequest{Catalog: catalog})
	if err == nil || !strings.Contains(err.Error(), "没有可识别 endpoint") {
		t.Fatalf("expected unknown endpoint to block import, got %v", err)
	}
	if len(report.ResolvedEndpoints) == 0 {
		t.Fatalf("expected resolved endpoint report: %+v", report)
	}
	if len(report.UnresolvedEndpoints) != 1 || report.UnresolvedEndpoints[0].ModelName != "unknown-endpoint-model" {
		t.Fatalf("expected unresolved endpoint report, got %+v", report.UnresolvedEndpoints)
	}
}

func TestDryRunRejectsUnknownEndpoint(t *testing.T) {
	catalog := &ProviderCatalog{
		ProviderCode: "vector",
		ProviderName: "向量",
		BaseURL:      "https://example.com",
		Models: []CatalogModel{{
			Name:                   "unknown-endpoint-model",
			EnableGroups:           []string{"default"},
			SupportedEndpointTypes: []string{"not-a-real-endpoint"},
		}},
	}
	_, err := DryRun(ImportRequest{Catalog: catalog})
	if err == nil || !strings.Contains(err.Error(), "没有可识别 endpoint") {
		t.Fatalf("expected unknown endpoint to block import, got %v", err)
	}
}

func TestPostgresCurrentVectorFixturesDryRunAndApply(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("set TEST_POSTGRES_DSN to run full vector fixture test")
	}
	normal, err := os.ReadFile(filepath.Join("..", "..", "..", "向量普通规则.txt"))
	if err != nil {
		t.Skipf("normal fixture unavailable: %v", err)
	}
	special, err := os.ReadFile(filepath.Join("..", "..", "tmp", "special-clean", "SpecialModelPricing.cleaned.json"))
	if err != nil {
		t.Skipf("cleaned special fixture unavailable: %v", err)
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"abilities", "channels", "models", "vendors", "options"} {
		if err := db.Exec("DROP TABLE IF EXISTS " + table + " CASCADE").Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AutoMigrate(
		&model.Vendor{},
		&model.Model{},
		&model.Channel{},
		&model.Ability{},
		&model.Option{},
	); err != nil {
		t.Fatal(err)
	}
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	model.InitOptionMap()

	catalog, err := ParseVectorBundle(VectorBundleRequest{
		ProviderCode:   "vector",
		ProviderName:   "向量",
		BaseURL:        "https://q.aibaotui.com",
		NormalContent:  normal,
		SpecialContent: special,
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := DryRun(ImportRequest{Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if preview.SpecialPricingModels == 0 || preview.TaskBillingRules == 0 || len(preview.SourceHashes) != 2 {
		t.Fatalf("incomplete preview: %+v", preview)
	}
	applied, err := Apply(ImportRequest{Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Applied || applied.ChannelsToCreate == 0 {
		t.Fatalf("incomplete apply report: %+v", applied)
	}
	var option model.Option
	if err := db.Where("key = ?", "TaskBillingRules").First(&option).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(option.Value, `"grok-video-3-10s"`) {
		t.Fatalf("grok task rule missing from full fixture apply")
	}
}

func TestCurrentVectorFixturesMatchLivePricing(t *testing.T) {
	if os.Getenv("RUN_VECTOR_LIVE_PRICING_TEST") != "1" {
		t.Skip("set RUN_VECTOR_LIVE_PRICING_TEST=1 to validate current vector fixtures")
	}
	normal, err := os.ReadFile(filepath.Join("..", "..", "..", "向量普通规则.txt"))
	if err != nil {
		t.Fatal(err)
	}
	special, err := os.ReadFile(filepath.Join("..", "..", "tmp", "special-clean", "SpecialModelPricing.cleaned.json"))
	if err != nil {
		t.Fatal(err)
	}
	remote, err := FetchVectorRemotePricing(context.Background(), http.DefaultClient, "https://q.aibaotui.com")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := ParseVectorBundle(VectorBundleRequest{
		ProviderCode:         "vector",
		ProviderName:         "向量",
		BaseURL:              "https://q.aibaotui.com",
		NormalContent:        normal,
		SpecialContent:       special,
		RemotePricingContent: remote,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 539 {
		t.Fatalf("models = %d, want 539", len(catalog.Models))
	}
	if len(catalog.BillingModes) != 45 {
		t.Fatalf("tiered billing models = %d, want 45", len(catalog.BillingModes))
	}
	if catalog.RemotePricingReport == nil || len(catalog.RemotePricingReport.Mismatches) != 0 {
		t.Fatalf("remote pricing mismatch: %+v", catalog.RemotePricingReport)
	}
	for _, modelName := range []string{
		"happyhorse-1.0-i2v",
		"happyhorse-1.0-r2v",
		"happyhorse-1.0-t2v",
		"happyhorse-1.0-video-edit",
		"wan2.5-i2v-preview",
		"wan2.6-i2v",
		"wan2.6-i2v-flash",
		"grok-imagine-1.0-video",
		"kling-motion-control",
	} {
		rule, ok := specialPricingModels(catalog.SpecialPricing)[modelName].(map[string]any)
		if !ok || rule["billing_enabled"] != true {
			t.Fatalf("quota_type=4 model %s lacks enabled special pricing: %+v", modelName, rule)
		}
	}
	gpt54 := catalog.BillingExprs["gpt-5.4"]
	for _, expected := range []string{"p * 2.5", "c * 15", "p * 5", "c * 22.5"} {
		if !strings.Contains(gpt54, expected) {
			t.Fatalf("gpt-5.4 expression missing %q: %s", expected, gpt54)
		}
	}
	gpt55 := catalog.BillingExprs["gpt-5.5"]
	for _, expected := range []string{"p * 5", "c * 30", "p * 10", "c * 45"} {
		if !strings.Contains(gpt55, expected) {
			t.Fatalf("gpt-5.5 expression missing %q: %s", expected, gpt55)
		}
	}
}

func TestVectorFixturesFromEnvMatchLivePricing(t *testing.T) {
	normalPath := strings.TrimSpace(os.Getenv("VECTOR_NORMAL_FIXTURE"))
	specialPath := strings.TrimSpace(os.Getenv("VECTOR_SPECIAL_FIXTURE"))
	if normalPath == "" || specialPath == "" {
		t.Skip("set VECTOR_NORMAL_FIXTURE and VECTOR_SPECIAL_FIXTURE")
	}
	normal, err := os.ReadFile(normalPath)
	if err != nil {
		t.Fatal(err)
	}
	special, err := os.ReadFile(specialPath)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := FetchVectorRemotePricing(
		context.Background(),
		http.DefaultClient,
		"https://q.aibaotui.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := ParseVectorBundle(VectorBundleRequest{
		ProviderCode:         "vector",
		ProviderName:         "向量",
		BaseURL:              "https://q.aibaotui.com",
		NormalContent:        normal,
		SpecialContent:       special,
		RemotePricingContent: remote,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.ValidationErrors) > 0 {
		t.Fatalf("strict validation blocked: %+v", catalog.ValidationErrors)
	}
	if catalog.RemotePricingReport == nil ||
		len(catalog.RemotePricingReport.Mismatches) != 0 {
		t.Fatalf("remote pricing mismatch: %+v", catalog.RemotePricingReport)
	}
	report, err := DryRun(ImportRequest{Catalog: catalog})
	if err != nil {
		t.Fatalf("dry-run failed: %v report=%+v", err, report)
	}
	if report.Models == 0 || report.TieredBillingModels == 0 ||
		report.SpecialPricingModels == 0 {
		t.Fatalf("incomplete import report: %+v", report)
	}
}
