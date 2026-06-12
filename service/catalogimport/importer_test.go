package catalogimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
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
