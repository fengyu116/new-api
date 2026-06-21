package catalogimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/special_pricing"
	"github.com/QuantumNous/new-api/setting/task_billing_rules"

	"gorm.io/gorm"
)

type ImportRequest struct {
	Catalog   *ProviderCatalog
	GroupKeys []TokenRow
	Apply     bool
}

type ImportReport struct {
	Mode                 string                `json:"mode"`
	ProviderCode         string                `json:"provider_code"`
	ProviderName         string                `json:"provider_name"`
	BaseURL              string                `json:"base_url"`
	Models               int                   `json:"models"`
	Vendors              int                   `json:"vendors"`
	Groups               int                   `json:"groups"`
	ChannelsToCreate     int                   `json:"channels_to_create"`
	ChannelsToReplace    int64                 `json:"channels_to_replace"`
	ChannelTypeModels    map[string]int        `json:"channel_type_models,omitempty"`
	ResolvedEndpoints    []EndpointResolution  `json:"resolved_endpoints,omitempty"`
	UnresolvedEndpoints  []EndpointResolution  `json:"unresolved_endpoints,omitempty"`
	MissingKeyGroups     []string              `json:"missing_key_groups"`
	InvalidRows          []GroupKeyReportError `json:"invalid_rows,omitempty"`
	SpecialPricingModels int                   `json:"special_pricing_models"`
	TaskBillingRules     int                   `json:"task_billing_rules"`
	TieredBillingModels  int                   `json:"tiered_billing_models"`
	RemotePricingReport  *RemotePricingReport  `json:"remote_pricing_report,omitempty"`
	SourceHashes         map[string]string     `json:"source_hashes,omitempty"`
	PreviousSourceHashes map[string]string     `json:"previous_source_hashes,omitempty"`
	SourceHashesChanged  bool                  `json:"source_hashes_changed"`
	SkippedSpecialModels []string              `json:"skipped_special_models,omitempty"`
	BlockedReasons       []string              `json:"blocked_reasons,omitempty"`
	ChangedOptionKeys    []string              `json:"changed_option_keys"`
	ManagedTagPrefix     string                `json:"managed_tag_prefix"`
	Applied              bool                  `json:"applied"`
}

type EndpointResolution struct {
	ModelName   string `json:"model_name,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	Path        string `json:"path,omitempty"`
	Method      string `json:"method,omitempty"`
	ChannelType string `json:"channel_type,omitempty"`
	Source      string `json:"source,omitempty"`
	Models      int    `json:"models,omitempty"`
	ModelType   string `json:"model_type,omitempty"`
	Tags        string `json:"tags,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type providerImportState struct {
	SourceHashes         map[string]string `json:"source_hashes,omitempty"`
	SpecialPricingModels []string          `json:"special_pricing_models,omitempty"`
	TaskBillingModels    []string          `json:"task_billing_models,omitempty"`
	TieredBillingModels  []string          `json:"tiered_billing_models,omitempty"`
	AppliedAt            int64             `json:"applied_at"`
}

type channelPlan struct {
	Type             int
	Endpoint         string
	EndpointPath     string
	EndpointMethod   string
	ResolutionSource string
	Group            string
	Models           []string
	ModelMapping     map[string]string
	ParamOverride    map[string]any
	Tag              string
	Key              string
	Priority         int64
	Weight           uint
}

type endpointSpec struct {
	Name        string
	ChannelType int
	Path        string
	Method      string
	Source      string
}

func DryRun(req ImportRequest) (ImportReport, error) {
	return buildReport(req, loadPreservedKeys(req.Catalog))
}

func Apply(req ImportRequest) (ImportReport, error) {
	if req.Catalog == nil {
		return ImportReport{}, fmt.Errorf("catalog 不能为空")
	}
	report, err := buildReport(req, loadPreservedKeys(req.Catalog))
	if err != nil {
		return report, err
	}
	if len(report.InvalidRows) > 0 {
		return report, fmt.Errorf("分组 key 文件存在校验错误")
	}

	plans := buildChannelPlans(req.Catalog, keyMapFromRows(req.GroupKeys))
	optionValues, err := buildOptionValues(req.Catalog)
	if err != nil {
		return report, err
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if !req.Catalog.SpecialOnly {
			if err := applyCatalogTx(tx, req.Catalog, plans); err != nil {
				return err
			}
		}
		return model.SaveOptionsTx(tx, optionValues)
	}); err != nil {
		return report, err
	}
	if err := model.ApplyOptionValuesToMemory(optionValues); err != nil {
		return report, err
	}
	model.InitChannelCache()
	report.Applied = true
	report.Mode = "apply"
	return report, nil
}

func loadPreservedKeys(catalog *ProviderCatalog) map[string]string {
	preserved := map[string]string{}
	if catalog == nil || model.DB == nil {
		return preserved
	}
	var existing []model.Channel
	if err := model.DB.
		Where("tag LIKE ?", ProviderTagPrefix(catalog.ProviderCode)+"%").
		Where("base_url = ?", strings.TrimRight(catalog.BaseURL, "/")).
		Find(&existing).Error; err != nil {
		return preserved
	}
	for _, channel := range existing {
		if channel.Group != "" && channel.Key != "" {
			endpoint := endpointFromTag(stringValue(channel.Tag))
			preserved[preservedKey(channel.Group, channel.Type, endpoint)] = channel.Key
			preserved[preservedKey(channel.Group, channel.Type, "")] = channel.Key
			preserved[preservedKey(channel.Group, 0, "")] = channel.Key
		}
	}
	return preserved
}

func buildReport(req ImportRequest, preservedKeys map[string]string) (ImportReport, error) {
	if req.Catalog == nil {
		return ImportReport{}, fmt.Errorf("catalog 不能为空")
	}
	prefix := ProviderTagPrefix(req.Catalog.ProviderCode)
	previousState := loadProviderImportState(req.Catalog.ProviderCode, req.Catalog.BaseURL)
	keyRows := keyMapFromRows(req.GroupKeys)
	if len(req.GroupKeys) > 0 {
		restrictCatalogToKeyGroups(req.Catalog, keyRows)
		if len(keyRows) > 0 && (len(req.Catalog.Groups) == 0 || len(req.Catalog.Models) == 0) {
			req.Catalog.ValidationErrors = append(req.Catalog.ValidationErrors, "分组 key 文件中的分组与当前目录没有可导入交集")
		}
	}
	keyReport, keyErr := BuildGroupKeyReport(req.Catalog.ProviderCode, req.Catalog.BaseURL, req.GroupKeys)
	report := ImportReport{
		Mode:                 "dry-run",
		ProviderCode:         req.Catalog.ProviderCode,
		ProviderName:         req.Catalog.ProviderName,
		BaseURL:              strings.TrimRight(req.Catalog.BaseURL, "/"),
		Models:               len(req.Catalog.Models),
		Vendors:              len(req.Catalog.Vendors),
		Groups:               len(req.Catalog.Groups),
		ManagedTagPrefix:     prefix,
		ChangedOptionKeys:    changedOptionKeys(req.Catalog),
		SourceHashes:         req.Catalog.SourceHashes,
		RemotePricingReport:  req.Catalog.RemotePricingReport,
		PreviousSourceHashes: previousState.SourceHashes,
		SourceHashesChanged:  hashesChanged(previousState.SourceHashes, req.Catalog.SourceHashes),
		SkippedSpecialModels: append([]string(nil), req.Catalog.SkippedSpecialModels...),
		BlockedReasons:       append([]string(nil), req.Catalog.ValidationErrors...),
	}
	if len(report.BlockedReasons) > 0 {
		return report, fmt.Errorf("%s", strings.Join(report.BlockedReasons, "; "))
	}
	if keyErr != nil {
		report.InvalidRows = keyReport.InvalidRows
	}
	var count int64
	if model.DB != nil && !req.Catalog.SpecialOnly {
		_ = model.DB.Model(&model.Channel{}).
			Where("tag LIKE ?", prefix+"%").
			Where("base_url = ?", strings.TrimRight(req.Catalog.BaseURL, "/")).
			Count(&count).Error
	}
	report.ChannelsToReplace = count
	plans := buildChannelPlans(req.Catalog, keyRows)
	if err := validateChannelPlans(req.Catalog, plans); err != nil {
		report.ResolvedEndpoints = resolvedEndpointReports(plans)
		report.UnresolvedEndpoints = unresolvedEndpointReports(req.Catalog)
		return report, err
	}
	report.ChannelsToCreate = len(plans)
	report.ChannelTypeModels = channelTypeModelCounts(plans)
	report.ResolvedEndpoints = resolvedEndpointReports(plans)
	report.UnresolvedEndpoints = unresolvedEndpointReports(req.Catalog)
	missing := make(map[string]struct{})
	for _, plan := range plans {
		if plan.Key == "" {
			plan.Key = lookupPreservedKey(preservedKeys, plan)
		}
		if plan.Key == "" {
			missing[plan.Group] = struct{}{}
		}
	}
	report.MissingKeyGroups = sortedStructKeys(missing)
	report.SpecialPricingModels = countSpecialPricingModels(req.Catalog.SpecialPricing)
	report.TaskBillingRules = len(req.Catalog.TaskBillingRules)
	report.TieredBillingModels = len(req.Catalog.BillingModes)
	if req.Catalog.SpecialOnly {
		if err := validateSpecialOnlyProviderScope(req.Catalog); err != nil {
			return report, err
		}
	}
	return report, nil
}

func restrictCatalogToKeyGroups(catalog *ProviderCatalog, keys map[string]string) {
	if catalog == nil || len(keys) == 0 {
		return
	}
	allowed := make(map[string]struct{}, len(keys))
	for group := range keys {
		group = strings.TrimSpace(group)
		if group != "" {
			allowed[group] = struct{}{}
		}
	}
	filterGroups := func(values []string) []string {
		out := make([]string, 0, len(values))
		for _, group := range values {
			group = strings.TrimSpace(group)
			if _, ok := allowed[group]; ok {
				out = append(out, group)
			}
		}
		return uniqueStrings(out)
	}
	if len(catalog.Groups) > 0 {
		next := map[string]string{}
		for group, desc := range catalog.Groups {
			if _, ok := allowed[group]; ok {
				next[group] = desc
			}
		}
		catalog.Groups = next
	}
	if len(catalog.GroupRatios) > 0 {
		next := map[string]float64{}
		for group, ratio := range catalog.GroupRatios {
			if _, ok := allowed[group]; ok {
				next[group] = ratio
			}
		}
		catalog.GroupRatios = next
	}
	catalog.AutoGroups = filterGroups(catalog.AutoGroups)
	models := make([]CatalogModel, 0, len(catalog.Models))
	kept := map[string]struct{}{}
	for _, item := range catalog.Models {
		item.EnableGroups = filterGroups(item.EnableGroups)
		if len(item.EnableGroups) == 0 {
			continue
		}
		models = append(models, item)
		kept[item.Name] = struct{}{}
	}
	catalog.Models = models
	catalog.SpecialPricing = filterSpecialPricingModels(catalog.SpecialPricing, kept)
	catalog.TaskBillingRules = filterTaskBillingRules(catalog.TaskBillingRules, kept)
	catalog.BillingModes = filterStringMap(catalog.BillingModes, kept)
	catalog.BillingExprs = filterStringMap(catalog.BillingExprs, kept)
}

func validateSpecialOnlyProviderScope(catalog *ProviderCatalog) error {
	if model.DB == nil {
		return nil
	}
	var channels []model.Channel
	if err := model.DB.
		Where("tag LIKE ?", ProviderTagPrefix(catalog.ProviderCode)+"%").
		Where("base_url = ?", strings.TrimRight(catalog.BaseURL, "/")).
		Find(&channels).Error; err != nil {
		return err
	}
	managedModels := map[string]struct{}{}
	for _, channel := range channels {
		for _, modelName := range strings.Split(channel.Models, ",") {
			modelName = strings.TrimSpace(modelName)
			if modelName != "" {
				managedModels[modelName] = struct{}{}
			}
		}
	}
	var outside []string
	for modelName := range specialPricingModels(catalog.SpecialPricing) {
		if _, ok := managedModels[modelName]; !ok {
			outside = append(outside, modelName)
		}
	}
	sort.Strings(outside)
	if len(outside) > 0 {
		return fmt.Errorf("特殊规则包含不属于供应商 %s 的模型: %s", catalog.ProviderCode, strings.Join(outside, ", "))
	}
	return nil
}

func applyCatalogTx(tx *gorm.DB, catalog *ProviderCatalog, plans []channelPlan) error {
	preservedKeys := map[string]string{}
	var existing []model.Channel
	if err := tx.
		Where("tag LIKE ?", ProviderTagPrefix(catalog.ProviderCode)+"%").
		Where("base_url = ?", strings.TrimRight(catalog.BaseURL, "/")).
		Find(&existing).Error; err != nil {
		return err
	}
	for _, channel := range existing {
		if channel.Key != "" {
			endpoint := endpointFromTag(stringValue(channel.Tag))
			for _, key := range []string{
				preservedKey(channel.Group, channel.Type, endpoint),
				preservedKey(channel.Group, channel.Type, ""),
				preservedKey(channel.Group, 0, ""),
			} {
				if _, ok := preservedKeys[key]; !ok {
					preservedKeys[key] = channel.Key
				}
			}
		}
	}
	var channelIDs []int
	for _, channel := range existing {
		channelIDs = append(channelIDs, channel.Id)
	}
	if len(channelIDs) > 0 {
		if err := tx.Where("channel_id IN ?", channelIDs).Delete(&model.Ability{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id IN ?", channelIDs).Delete(&model.Channel{}).Error; err != nil {
			return err
		}
	}
	vendorIDMap, err := upsertVendorsTx(tx, catalog)
	if err != nil {
		return err
	}
	if err := upsertModelsTx(tx, catalog, vendorIDMap); err != nil {
		return err
	}
	for _, plan := range plans {
		if plan.Key == "" {
			plan.Key = lookupPreservedKey(preservedKeys, plan)
		}
		if err := insertChannelPlanTx(tx, catalog, plan); err != nil {
			return err
		}
	}
	return nil
}

func upsertVendorsTx(tx *gorm.DB, catalog *ProviderCatalog) (map[int]int, error) {
	now := common.GetTimestamp()
	vendorIDMap := make(map[int]int, len(catalog.Vendors))
	if err := syncVendorIDSequenceTx(tx); err != nil {
		return nil, err
	}
	for _, item := range catalog.Vendors {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		var vendor model.Vendor
		err := tx.Where("name = ?", item.Name).First(&vendor).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
			vendor = model.Vendor{
				Name:        item.Name,
				Description: item.Description,
				Icon:        item.Icon,
				Status:      1,
				CreatedTime: now,
				UpdatedTime: now,
			}
			if err := tx.Create(&vendor).Error; err != nil {
				return nil, err
			}
		}
		if err := tx.Model(&vendor).Updates(map[string]any{
			"description":  item.Description,
			"icon":         item.Icon,
			"status":       1,
			"updated_time": now,
		}).Error; err != nil {
			return nil, err
		}
		if item.ID > 0 {
			vendorIDMap[item.ID] = vendor.Id
		}
	}
	return vendorIDMap, nil
}

func syncVendorIDSequenceTx(tx *gorm.DB) error {
	if tx == nil || tx.Dialector == nil || tx.Dialector.Name() != "postgres" {
		return nil
	}
	return tx.Exec(`
		SELECT setval(
			pg_get_serial_sequence('vendors', 'id'),
			GREATEST(COALESCE((SELECT MAX(id) FROM vendors), 0) + 1, 1),
			false
		)
	`).Error
}

func upsertModelsTx(tx *gorm.DB, catalog *ProviderCatalog, vendorIDMap map[int]int) error {
	now := common.GetTimestamp()
	for _, item := range catalog.Models {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		endpoints := "{}"
		if len(item.EndpointMap) > 0 {
			endpoints = mustJSON(item.EndpointMap)
		}
		vendorID := item.VendorID
		if mappedID, ok := vendorIDMap[item.VendorID]; ok {
			vendorID = mappedID
		}
		var meta model.Model
		create := model.Model{
			ModelName:    item.Name,
			Description:  item.Description,
			Tags:         item.Tags,
			VendorID:     vendorID,
			Endpoints:    endpoints,
			Status:       1,
			SyncOfficial: 0,
			CreatedTime:  now,
			UpdatedTime:  now,
		}
		if err := tx.Where("model_name = ?", item.Name).FirstOrCreate(&meta, create).Error; err != nil {
			return err
		}
		if err := tx.Model(&meta).Updates(map[string]any{
			"description":   item.Description,
			"tags":          item.Tags,
			"vendor_id":     vendorID,
			"endpoints":     endpoints,
			"status":        1,
			"sync_official": 0,
			"updated_time":  now,
			"name_rule":     0,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func insertChannelPlanTx(tx *gorm.DB, catalog *ProviderCatalog, plan channelPlan) error {
	now := common.GetTimestamp()
	priority := plan.Priority
	weight := plan.Weight
	modelMapping := mustJSON(plan.ModelMapping)
	paramOverride := ""
	if len(plan.ParamOverride) > 0 {
		paramOverride = mustJSON(plan.ParamOverride)
	}
	baseURL := strings.TrimRight(catalog.BaseURL, "/")
	tag := plan.Tag
	channel := model.Channel{
		Type:          plan.Type,
		Key:           plan.Key,
		Status:        common.ChannelStatusEnabled,
		Name:          fmt.Sprintf("%s %s %s", catalog.ProviderName, plan.Group, channelTypeName(plan.Type, plan.Endpoint)),
		BaseURL:       &baseURL,
		Models:        strings.Join(plan.Models, ","),
		Group:         plan.Group,
		ModelMapping:  &modelMapping,
		Priority:      &priority,
		Weight:        &weight,
		Tag:           &tag,
		ParamOverride: &paramOverride,
		CreatedTime:   now,
	}
	if err := tx.Create(&channel).Error; err != nil {
		return err
	}
	abilities := make([]model.Ability, 0, len(plan.Models))
	for _, modelName := range plan.Models {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		abilities = append(abilities, model.Ability{
			Group:     plan.Group,
			Model:     modelName,
			ChannelId: channel.Id,
			Enabled:   true,
			Priority:  &priority,
			Weight:    weight,
			Tag:       &tag,
		})
	}
	if len(abilities) > 0 {
		return tx.Create(&abilities).Error
	}
	return nil
}

func buildOptionValues(catalog *ProviderCatalog) (map[string]string, error) {
	values := map[string]string{}
	previousState := loadProviderImportState(catalog.ProviderCode, catalog.BaseURL)
	modelRatio := ratio_setting.GetModelRatioCopy()
	modelPrice := ratio_setting.GetModelPriceCopy()
	completionRatio := ratio_setting.GetCompletionRatioCopy()
	cacheRatio := ratio_setting.GetCacheRatioCopy()
	createCacheRatio := ratio_setting.GetCreateCacheRatioCopy()
	audioCompletionRatio := ratio_setting.GetAudioCompletionRatioCopy()
	groupRatio := ratio_setting.GetGroupRatioCopy()
	userGroups := setting.GetUserUsableGroupsCopy()

	for group, desc := range catalog.Groups {
		if _, ok := userGroups[group]; !ok {
			userGroups[group] = desc
		}
		if ratio, ok := catalog.GroupRatios[group]; ok && ratio > 0 {
			groupRatio[group] = ratio
		} else if _, ok := groupRatio[group]; !ok {
			groupRatio[group] = 1
		}
	}
	for _, group := range catalog.AutoGroups {
		if _, ok := userGroups[group]; !ok {
			userGroups[group] = group
		}
		if _, ok := groupRatio[group]; !ok {
			groupRatio[group] = 1
		}
	}
	for _, item := range catalog.Models {
		switch item.QuotaType {
		case 0:
			modelRatio[item.Name] = item.ModelRatio
			delete(modelPrice, item.Name)
		case 3:
			modelRatio[item.Name] = item.ModelPrice / 2
			delete(modelPrice, item.Name)
		case 1, 2, 4:
			modelPrice[item.Name] = item.ModelPrice
			delete(modelRatio, item.Name)
		default:
			if item.ModelPrice > 0 {
				modelPrice[item.Name] = item.ModelPrice
				delete(modelRatio, item.Name)
			} else {
				modelRatio[item.Name] = item.ModelRatio
				delete(modelPrice, item.Name)
			}
		}
		if item.CompletionRatio > 0 {
			completionRatio[item.Name] = item.CompletionRatio
		}
		if item.CacheRatio != nil {
			cacheRatio[item.Name] = *item.CacheRatio
		}
		if item.CreateCacheRatio != nil {
			createCacheRatio[item.Name] = *item.CreateCacheRatio
		}
		if item.AudioCompletionRatio != nil {
			audioCompletionRatio[item.Name] = *item.AudioCompletionRatio
		}
	}
	if len(catalog.Models) > 0 {
		values["ModelRatio"] = mustJSON(modelRatio)
		values["ModelPrice"] = mustJSON(modelPrice)
		values["CompletionRatio"] = mustJSON(completionRatio)
		values["CacheRatio"] = mustJSON(cacheRatio)
		values["CreateCacheRatio"] = mustJSON(createCacheRatio)
		values["AudioCompletionRatio"] = mustJSON(audioCompletionRatio)
	}
	if len(catalog.Groups) > 0 || len(catalog.AutoGroups) > 0 {
		values["GroupRatio"] = mustJSON(groupRatio)
		values["UserUsableGroups"] = mustJSON(userGroups)
	}
	if len(catalog.SpecialPricing) > 0 {
		merged, err := mergeSpecialPricingOption(catalog.SpecialPricing, previousState.SpecialPricingModels)
		if err != nil {
			return nil, err
		}
		values[special_pricing.OptionKey] = merged
	}
	if len(catalog.TaskBillingRules) > 0 {
		removedTaskModels := previousState.TaskBillingModels
		if catalog.SpecialOnly {
			removedTaskModels = previousState.SpecialPricingModels
		}
		merged, err := mergeTaskBillingRulesOption(catalog.TaskBillingRules, removedTaskModels)
		if err != nil {
			return nil, err
		}
		values[task_billing_rules.OptionKey] = merged
	}
	if len(catalog.BillingModes) > 0 || len(previousState.TieredBillingModels) > 0 {
		modes := billing_setting.GetBillingModeCopy()
		expressions := billing_setting.GetBillingExprCopy()
		for _, modelName := range previousState.TieredBillingModels {
			delete(modes, modelName)
			delete(expressions, modelName)
		}
		for modelName, mode := range catalog.BillingModes {
			modes[modelName] = mode
		}
		for modelName, expr := range catalog.BillingExprs {
			if err := billing_setting.SmokeTestExpr(expr); err != nil {
				return nil, fmt.Errorf("模型 %s 阶梯表达式校验失败: %w", modelName, err)
			}
			expressions[modelName] = expr
		}
		values["billing_setting.billing_mode"] = mustJSON(modes)
		values["billing_setting.billing_expr"] = mustJSON(expressions)
	}
	state := buildProviderImportState(catalog, previousState)
	values[ProviderImportStateOptionKey(catalog.ProviderCode, catalog.BaseURL)] = mustJSON(state)
	return values, nil
}

func mergeTaskBillingRulesOption(incoming map[string]task_billing_rules.Rule, removeModels []string) (string, error) {
	current := map[string]task_billing_rules.Rule{}
	if err := json.Unmarshal([]byte(task_billing_rules.ToJSONString()), &current); err != nil {
		return "", err
	}
	for _, modelName := range removeModels {
		delete(current, modelName)
	}
	for modelName, rule := range incoming {
		current[modelName] = rule
	}
	return mustJSON(current), nil
}

func mergeSpecialPricingOption(incoming map[string]any, removeModels []string) (string, error) {
	var current map[string]any
	if err := json.Unmarshal([]byte(special_pricing.ToJSONString()), &current); err != nil {
		return "", err
	}
	if current == nil {
		current = map[string]any{}
	}
	currentModels, _ := current["models"].(map[string]any)
	if currentModels == nil {
		currentModels = map[string]any{}
	}
	for _, modelName := range removeModels {
		delete(currentModels, modelName)
	}

	incomingModels, _ := incoming["models"].(map[string]any)
	if incomingModels == nil {
		incomingModels = incoming
	}
	for modelName, rule := range incomingModels {
		if strings.TrimSpace(modelName) == "" {
			continue
		}
		currentModels[modelName] = rule
	}
	if version, ok := incoming["version"].(string); ok && strings.TrimSpace(version) != "" {
		current["version"] = version
	} else if _, ok := current["version"]; !ok {
		current["version"] = "1"
	}
	current["models"] = currentModels
	return mustJSON(current), nil
}

func buildProviderImportState(catalog *ProviderCatalog, previous providerImportState) providerImportState {
	state := providerImportState{
		SourceHashes:         catalog.SourceHashes,
		SpecialPricingModels: sortedAnyKeys(specialPricingModels(catalog.SpecialPricing)),
		TaskBillingModels:    sortedTaskRuleKeys(catalog.TaskBillingRules),
		TieredBillingModels:  sortedStringKeys(catalog.BillingModes),
		AppliedAt:            time.Now().Unix(),
	}
	if catalog.SpecialOnly {
		nonSpecialTaskModels := make(map[string]struct{}, len(previous.TaskBillingModels))
		previousSpecial := make(map[string]struct{}, len(previous.SpecialPricingModels))
		for _, modelName := range previous.SpecialPricingModels {
			previousSpecial[modelName] = struct{}{}
		}
		for _, modelName := range previous.TaskBillingModels {
			if _, wasSpecial := previousSpecial[modelName]; !wasSpecial {
				nonSpecialTaskModels[modelName] = struct{}{}
			}
		}
		for modelName := range catalog.TaskBillingRules {
			nonSpecialTaskModels[modelName] = struct{}{}
		}
		state.TaskBillingModels = sortedStructKeys(nonSpecialTaskModels)
		if len(catalog.SourceHashes) == 0 {
			state.SourceHashes = previous.SourceHashes
		}
	}
	return state
}

func sortedStringKeys(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func loadProviderImportState(providerCode, baseURL string) providerImportState {
	raw := strings.TrimSpace(common.OptionMap[ProviderImportStateOptionKey(providerCode, baseURL)])
	if raw == "" {
		raw = strings.TrimSpace(common.OptionMap[LegacyProviderImportStateOptionKey(providerCode)])
	}
	if raw == "" {
		return providerImportState{}
	}
	var state providerImportState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return providerImportState{}
	}
	return state
}

func hashesChanged(previous, current map[string]string) bool {
	if len(previous) == 0 {
		return false
	}
	if len(previous) != len(current) {
		return true
	}
	for name, hash := range previous {
		if current[name] != hash {
			return true
		}
	}
	return false
}

func sortedAnyKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedTaskRuleKeys(values map[string]task_billing_rules.Rule) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func buildChannelPlans(catalog *ProviderCatalog, keys map[string]string) []channelPlan {
	grouped := map[string]*channelPlan{}
	for _, item := range catalog.Models {
		endpoints := endpointSpecsForModel(item)
		groups := item.EnableGroups
		if len(groups) == 0 {
			groups = []string{"default"}
		}
		for _, group := range groups {
			group = strings.TrimSpace(group)
			if group == "" {
				continue
			}
			for _, endpoint := range endpoints {
				tag := ProviderGroupTag(catalog.ProviderCode, group, endpoint.ChannelType, endpoint.Name, item.ModelMapping, item.ParamOverride)
				plan, ok := grouped[tag]
				if !ok {
					plan = &channelPlan{
						Type:             endpoint.ChannelType,
						Endpoint:         endpoint.Name,
						EndpointPath:     endpoint.Path,
						EndpointMethod:   endpoint.Method,
						ResolutionSource: endpoint.Source,
						Group:            group,
						ModelMapping:     map[string]string{},
						ParamOverride:    item.ParamOverride,
						Tag:              tag,
						Key:              keys[group],
					}
					grouped[tag] = plan
				}
				plan.Models = append(plan.Models, item.Name)
				for k, v := range item.ModelMapping {
					plan.ModelMapping[k] = v
				}
			}
		}
	}
	out := make([]channelPlan, 0, len(grouped))
	for _, plan := range grouped {
		sort.Strings(plan.Models)
		out = append(out, *plan)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out
}

func validateChannelPlans(catalog *ProviderCatalog, plans []channelPlan) error {
	unresolved := unresolvedEndpointReports(catalog)
	if len(unresolved) > 0 {
		missing := make([]string, 0, len(unresolved))
		for _, item := range unresolved {
			missing = append(missing, item.ModelName)
		}
		sort.Strings(missing)
		return fmt.Errorf("以下模型没有可识别 endpoint，已阻止导入: %s", strings.Join(missing, ", "))
	}
	for _, plan := range plans {
		if plan.Type == constant.ChannelTypeUnknown {
			return fmt.Errorf("渠道端点与类型不一致: endpoint=%s type=%d", plan.Endpoint, plan.Type)
		}
	}
	return nil
}

func endpointSpecsForModel(item CatalogModel) []endpointSpec {
	endpoints := item.SupportedEndpointTypes
	if len(endpoints) == 0 && item.ChannelType > 0 {
		endpoint := endpointNameForChannelType(item.ChannelType)
		if endpoint != "" {
			endpoints = []string{endpoint}
		}
	}
	out := make([]endpointSpec, 0, len(endpoints))
	seen := map[string]struct{}{}
	for _, raw := range endpoints {
		label := strings.TrimSpace(raw)
		if label == "" {
			continue
		}
		path, method := endpointPathAndMethod(item.EndpointMap[label])
		spec := resolveEndpointSpec(item, label, path, method)
		if spec.ChannelType == constant.ChannelTypeUnknown {
			continue
		}
		key := fmt.Sprintf("%d:%s", spec.ChannelType, strings.ToLower(spec.Name))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, spec)
	}
	return out
}

func channelTypeForEndpoint(endpoint string) int {
	return resolveEndpointSpec(CatalogModel{}, endpoint, "", "").ChannelType
}

func resolveEndpointSpec(item CatalogModel, label string, path string, method string) endpointSpec {
	label = strings.TrimSpace(label)
	path = strings.TrimSpace(path)
	method = strings.ToUpper(strings.TrimSpace(method))
	if channelType, endpoint := resolveEndpointByPath(path); channelType != constant.ChannelTypeUnknown {
		return endpointSpec{Name: endpoint, ChannelType: channelType, Path: path, Method: method, Source: "path"}
	}
	if channelType, endpoint := resolveEndpointByLabel(label); channelType != constant.ChannelTypeUnknown {
		return endpointSpec{Name: endpoint, ChannelType: channelType, Path: path, Method: method, Source: "label"}
	}
	if channelType, endpoint := resolveEndpointByMetadata(item); channelType != constant.ChannelTypeUnknown {
		return endpointSpec{Name: endpoint, ChannelType: channelType, Path: path, Method: method, Source: "metadata"}
	}
	return endpointSpec{Name: label, ChannelType: constant.ChannelTypeUnknown, Path: path, Method: method, Source: "unresolved"}
}

func resolveEndpointByPath(path string) (int, string) {
	value := normalizeEndpointText(path)
	switch {
	case value == "":
		return constant.ChannelTypeUnknown, ""
	case strings.Contains(value, "/v1/images/generations"):
		return constant.ChannelTypeOpenAI, "images-generations"
	case strings.Contains(value, "/v1/audio/speech"):
		return constant.ChannelTypeOpenAI, "audio-speech"
	case strings.Contains(value, "/v1/audio/transcriptions"):
		return constant.ChannelTypeOpenAI, "audio-transcriptions"
	case strings.Contains(value, "/v1/audio/translations"):
		return constant.ChannelTypeOpenAI, "audio-translations"
	case strings.Contains(value, "/v1/embeddings"):
		return constant.ChannelTypeOpenAI, "embeddings"
	case strings.Contains(value, "/v1/rerank"):
		return constant.ChannelTypeOpenAI, "rerank"
	case strings.Contains(value, "/v1/moderations"):
		return constant.ChannelTypeOpenAI, "moderations"
	case strings.Contains(value, "/v1/realtime"):
		return constant.ChannelTypeOpenAI, "realtime"
	case strings.Contains(value, "/mj/submit/"):
		return constant.ChannelTypeMidjourney, "mj-" + lastPathPart(value)
	case strings.Contains(value, "/kling/") || strings.HasPrefix(strings.TrimPrefix(value, "/"), "kling/"):
		return constant.ChannelTypeKling, "kling-" + lastPathPart(value)
	case strings.Contains(value, "/minimax/v1/video_generation"):
		return constant.ChannelTypeMiniMax, "minimax-video"
	case strings.Contains(value, "/minimax/v1/t2a"):
		return constant.ChannelTypeMiniMax, "minimax-audio"
	case strings.Contains(value, "/volc/v1/contents/generations/tasks"):
		return constant.ChannelTypeDoubaoVideo, "volc-generation-task"
	case strings.Contains(value, "/alibailian/") && strings.Contains(value, "video-synthesis"):
		return constant.ChannelTypeAli, "alibailian-video-synthesis"
	case strings.Contains(value, "/openapi/v2/"):
		return constant.ChannelTypeCustom, "pixverse-" + lastPathPart(value)
	default:
		return constant.ChannelTypeUnknown, ""
	}
}

func resolveEndpointByLabel(label string) (int, string) {
	value := normalizeEndpointText(label)
	switch {
	case value == "":
		return constant.ChannelTypeUnknown, ""
	case strings.Contains(value, "openai"):
		return constant.ChannelTypeOpenAI, "openai"
	case strings.Contains(value, "anthropic") || strings.Contains(value, "claude"):
		return constant.ChannelTypeAnthropic, "anthropic"
	case strings.Contains(value, "gemini") || strings.Contains(value, "geminitts"):
		return constant.ChannelTypeGemini, "gemini"
	case strings.Contains(value, "images-generations") || strings.Contains(value, "image-generation") || strings.Contains(value, "dall-e") || strings.Contains(value, "文本转语音") || strings.Contains(value, "语音转文字") || strings.Contains(value, "嵌入") || strings.Contains(value, "embedding") || strings.Contains(value, "rerank") || strings.Contains(value, "moderation") || strings.Contains(value, "realtime"):
		return constant.ChannelTypeOpenAI, slug(label)
	case strings.Contains(value, "mj"):
		return constant.ChannelTypeMidjourney, slug(label)
	case strings.Contains(value, "vidu"):
		return constant.ChannelTypeVidu, slug(label)
	case strings.Contains(value, "kling") || strings.Contains(value, "对口型") || strings.Contains(value, "文生音效") || strings.Contains(value, "视频生音效") || strings.Contains(value, "语音合成") || strings.Contains(value, "数字人") || strings.Contains(value, "视频特效") || strings.Contains(value, "动作控制") || strings.Contains(value, "omni-") || strings.Contains(value, "多模态视频编辑") || strings.Contains(value, "视频延长") || strings.Contains(value, "多图参考生视频"):
		return constant.ChannelTypeKling, slug(label)
	case strings.Contains(value, "sora"):
		return constant.ChannelTypeSora, "sora"
	case strings.Contains(value, "doubao") || strings.Contains(value, "seedance") || strings.Contains(value, "豆包"):
		return constant.ChannelTypeDoubaoVideo, slug(label)
	case strings.Contains(value, "grok") || strings.Contains(value, "xai"):
		return constant.ChannelTypeXai, slug(label)
	case strings.Contains(value, "ali") || strings.Contains(value, "dashscope") || strings.Contains(value, "wan") || strings.Contains(value, "万相"):
		return constant.ChannelTypeAli, slug(label)
	case strings.Contains(value, "suno"):
		return constant.ChannelTypeSunoAPI, "suno"
	case strings.Contains(value, "minimax") || strings.Contains(value, "hailuo") || strings.Contains(value, "海螺") || strings.Contains(value, "同步语音") || strings.Contains(value, "异步语音"):
		return constant.ChannelTypeMiniMax, slug(label)
	case strings.Contains(value, "pix") || strings.Contains(value, "图片模板") || strings.Contains(value, "主体替换") || strings.Contains(value, "动作模仿") || strings.Contains(value, "视频编辑") || strings.Contains(value, "重绘视频") || strings.Contains(value, "多帧") || strings.Contains(value, "音效"):
		return constant.ChannelTypeCustom, slug(label)
	default:
		return constant.ChannelTypeUnknown, ""
	}
}

func resolveEndpointByMetadata(item CatalogModel) (int, string) {
	value := normalizeEndpointText(strings.Join([]string{item.Name, item.ModelType, item.Tags, item.Description}, " "))
	switch {
	case value == "":
		return constant.ChannelTypeUnknown, ""
	case strings.Contains(value, "embedding") || strings.Contains(value, "rerank") || strings.Contains(value, "检索") || strings.Contains(value, "tts") || strings.Contains(value, "whisper") || strings.Contains(value, "transcribe") || strings.Contains(value, "realtime") || strings.Contains(value, "moderation") || strings.Contains(value, "gpt-image") || strings.Contains(value, "qwen-image") || strings.Contains(value, "flux") || strings.Contains(value, "z-image") || strings.Contains(value, "grok-imagine") || strings.Contains(value, "图像"):
		return constant.ChannelTypeOpenAI, "openai-compatible"
	case strings.HasPrefix(strings.ToLower(item.Name), "mj_"):
		return constant.ChannelTypeMidjourney, "midjourney"
	case strings.Contains(value, "kling"):
		return constant.ChannelTypeKling, "kling"
	case strings.Contains(value, "pixverse"):
		return constant.ChannelTypeCustom, "pixverse"
	case strings.Contains(value, "hailuo") || strings.Contains(value, "minimax") || strings.Contains(value, "speech-"):
		return constant.ChannelTypeMiniMax, "minimax"
	case strings.Contains(value, "doubao-seedance") || strings.Contains(value, "doubao-seedream"):
		return constant.ChannelTypeDoubaoVideo, "doubao"
	case strings.Contains(value, "wan") || strings.Contains(value, "happyhorse"):
		return constant.ChannelTypeAli, "ali-video"
	case strings.Contains(value, "vidu"):
		return constant.ChannelTypeVidu, "vidu"
	default:
		return constant.ChannelTypeUnknown, ""
	}
}

func normalizeEndpointText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "\\", "/")
	return value
}

func lastPathPart(path string) string {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return "endpoint"
	}
	parts := strings.Split(path, "/")
	return slug(parts[len(parts)-1])
}

func endpointPathAndMethod(value any) (string, string) {
	if value == nil {
		return "", ""
	}
	if object, ok := value.(map[string]any); ok {
		return anyString(object["path"]), anyString(object["method"])
	}
	return "", ""
}

func anyString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func endpointNameForChannelType(channelType int) string {
	switch channelType {
	case constant.ChannelTypeOpenAI:
		return "openai"
	case constant.ChannelTypeAnthropic:
		return "anthropic"
	case constant.ChannelTypeGemini:
		return "gemini"
	case constant.ChannelTypeVidu:
		return "vidu"
	case constant.ChannelTypeKling:
		return "kling"
	case constant.ChannelTypeSora:
		return "sora"
	case constant.ChannelTypeDoubaoVideo:
		return "doubao"
	case constant.ChannelTypeXai:
		return "xai"
	case constant.ChannelTypeAli:
		return "ali"
	case constant.ChannelTypeSunoAPI:
		return "suno"
	case constant.ChannelTypeMiniMax:
		return "minimax"
	default:
		return ""
	}
}

func channelTypeName(channelType int, endpoint string) string {
	name := constant.GetChannelTypeName(channelType)
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" || strings.EqualFold(endpoint, name) {
		return name
	}
	return fmt.Sprintf("%s/%s", name, endpoint)
}

func channelTypeModelCounts(plans []channelPlan) map[string]int {
	modelsByType := map[string]map[string]struct{}{}
	for _, plan := range plans {
		name := constant.GetChannelTypeName(plan.Type)
		if _, ok := modelsByType[name]; !ok {
			modelsByType[name] = map[string]struct{}{}
		}
		for _, modelName := range plan.Models {
			modelsByType[name][modelName] = struct{}{}
		}
	}
	out := make(map[string]int, len(modelsByType))
	for name, models := range modelsByType {
		out[name] = len(models)
	}
	return out
}

func resolvedEndpointReports(plans []channelPlan) []EndpointResolution {
	type bucket struct {
		report EndpointResolution
		models map[string]struct{}
	}
	buckets := map[string]*bucket{}
	for _, plan := range plans {
		key := fmt.Sprintf("%d|%s|%s|%s", plan.Type, plan.Endpoint, plan.EndpointPath, plan.ResolutionSource)
		item, ok := buckets[key]
		if !ok {
			item = &bucket{
				report: EndpointResolution{
					Endpoint:    plan.Endpoint,
					Path:        plan.EndpointPath,
					Method:      plan.EndpointMethod,
					ChannelType: constant.GetChannelTypeName(plan.Type),
					Source:      plan.ResolutionSource,
				},
				models: map[string]struct{}{},
			}
			buckets[key] = item
		}
		for _, modelName := range plan.Models {
			item.models[modelName] = struct{}{}
		}
	}
	out := make([]EndpointResolution, 0, len(buckets))
	for _, item := range buckets {
		item.report.Models = len(item.models)
		out = append(out, item.report)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ChannelType != out[j].ChannelType {
			return out[i].ChannelType < out[j].ChannelType
		}
		if out[i].Endpoint != out[j].Endpoint {
			return out[i].Endpoint < out[j].Endpoint
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func unresolvedEndpointReports(catalog *ProviderCatalog) []EndpointResolution {
	if catalog == nil {
		return nil
	}
	out := []EndpointResolution{}
	for _, item := range catalog.Models {
		if strings.TrimSpace(item.Name) == "" || len(endpointSpecsForModel(item)) > 0 {
			continue
		}
		endpoints := item.SupportedEndpointTypes
		if len(endpoints) == 0 {
			out = append(out, EndpointResolution{
				ModelName: item.Name,
				ModelType: item.ModelType,
				Tags:      item.Tags,
				Reason:    "模型没有 supported_endpoint_types，也没有 channel_type",
			})
			continue
		}
		for _, endpoint := range endpoints {
			path, method := endpointPathAndMethod(item.EndpointMap[endpoint])
			out = append(out, EndpointResolution{
				ModelName: item.Name,
				Endpoint:  endpoint,
				Path:      path,
				Method:    method,
				ModelType: item.ModelType,
				Tags:      item.Tags,
				Reason:    "endpoint label、path 和模型元数据都无法映射到渠道类型",
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ModelName != out[j].ModelName {
			return out[i].ModelName < out[j].ModelName
		}
		return out[i].Endpoint < out[j].Endpoint
	})
	return out
}

func preservedKey(group string, channelType int, endpoint string) string {
	return fmt.Sprintf("%s|%d|%s", strings.TrimSpace(group), channelType, strings.ToLower(strings.TrimSpace(endpoint)))
}

func lookupPreservedKey(preserved map[string]string, plan channelPlan) string {
	if preserved == nil {
		return ""
	}
	for _, key := range []string{
		preservedKey(plan.Group, plan.Type, plan.Endpoint),
		preservedKey(plan.Group, plan.Type, ""),
		preservedKey(plan.Group, 0, ""),
	} {
		if value := preserved[key]; value != "" {
			return value
		}
	}
	return ""
}

func endpointFromTag(tag string) string {
	parts := strings.Split(tag, ":")
	for i, part := range parts {
		if part != "group" {
			continue
		}
		if len(parts) >= i+4 {
			return parts[i+2]
		}
		return ""
	}
	return ""
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func keyMapFromRows(rows []TokenRow) map[string]string {
	keys := map[string]string{}
	for _, row := range rows {
		if row.Status == "已启用" && row.Group != "" && strings.HasPrefix(row.Key, "sk-") {
			keys[row.Group] = row.Key
		}
	}
	return keys
}

func changedOptionKeys(catalog *ProviderCatalog) []string {
	keys := []string{}
	if len(catalog.Models) > 0 {
		keys = append(keys, "ModelRatio", "ModelPrice", "CompletionRatio", "CacheRatio", "CreateCacheRatio", "AudioCompletionRatio")
	}
	if len(catalog.Groups) > 0 || len(catalog.AutoGroups) > 0 {
		keys = append(keys, "GroupRatio", "UserUsableGroups")
	}
	if len(catalog.SpecialPricing) > 0 {
		keys = append(keys, special_pricing.OptionKey)
	}
	if len(catalog.TaskBillingRules) > 0 {
		keys = append(keys, task_billing_rules.OptionKey)
	}
	if len(catalog.BillingModes) > 0 {
		keys = append(keys, "billing_setting.billing_mode", "billing_setting.billing_expr")
	}
	keys = append(keys, ProviderImportStateOptionKey(catalog.ProviderCode, catalog.BaseURL))
	return keys
}

func countSpecialPricingModels(value map[string]any) int {
	if len(value) == 0 {
		return 0
	}
	if models, ok := value["models"].(map[string]any); ok {
		return len(models)
	}
	return len(value)
}

func ProviderTagPrefix(providerCode string) string {
	return "catalog:" + providerCode + ":"
}

func ProviderGroupTag(providerCode, group string, channelType int, endpoint string, mapping map[string]string, override map[string]any) string {
	suffix := slug(group)
	if len(mapping) > 0 || len(override) > 0 {
		suffix += ":" + shortDigest(mustJSON(map[string]any{"m": mapping, "p": override}))
	}
	return fmt.Sprintf("%sgroup:%d:%s:%s", ProviderTagPrefix(providerCode), channelType, slug(endpoint), suffix)
}

func slug(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	replacer := strings.NewReplacer(" ", "-", "/", "-", "\\", "-", ":", "-", "|", "-")
	return replacer.Replace(value)
}

func shortDigest(value string) string {
	hash := uint32(2166136261)
	for _, b := range []byte(value) {
		hash ^= uint32(b)
		hash *= 16777619
	}
	return fmt.Sprintf("%08x", hash)
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func sortedStructKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func LastImportReportOptionKey(providerCode string) string {
	return "ProviderCatalogImportReport:" + providerCode
}

func ProviderImportStateOptionKey(providerCode, baseURL string) string {
	return "ProviderCatalogImportState:" + providerCode + ":" + shortDigest(strings.TrimRight(baseURL, "/"))
}

func LegacyProviderImportStateOptionKey(providerCode string) string {
	return "ProviderCatalogImportState:" + providerCode
}

func SaveLastReport(report ImportReport) error {
	report.Mode = strings.TrimSpace(report.Mode)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return model.UpdateOption(LastImportReportOptionKey(report.ProviderCode), string(data))
}

func ReportTimestamp() int64 {
	return time.Now().Unix()
}
