package catalogimport

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/special_pricing"

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
	MissingKeyGroups     []string              `json:"missing_key_groups"`
	InvalidRows          []GroupKeyReportError `json:"invalid_rows,omitempty"`
	SpecialPricingModels int                   `json:"special_pricing_models"`
	SkippedSpecialModels []string              `json:"skipped_special_models,omitempty"`
	ChangedOptionKeys    []string              `json:"changed_option_keys"`
	ManagedTagPrefix     string                `json:"managed_tag_prefix"`
	Applied              bool                  `json:"applied"`
}

type channelPlan struct {
	Type          int
	Group         string
	Models        []string
	ModelMapping  map[string]string
	ParamOverride map[string]any
	Tag           string
	Key           string
	Priority      int64
	Weight        uint
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
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		return applyCatalogTx(tx, req.Catalog, plans)
	}); err != nil {
		return report, err
	}
	if err := updateOptions(req.Catalog); err != nil {
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
	if err := model.DB.Where("tag LIKE ?", ProviderTagPrefix(catalog.ProviderCode)+"%").Find(&existing).Error; err != nil {
		return preserved
	}
	for _, channel := range existing {
		if channel.Group != "" && channel.Key != "" {
			preserved[channel.Group] = channel.Key
		}
	}
	return preserved
}

func buildReport(req ImportRequest, preservedKeys map[string]string) (ImportReport, error) {
	if req.Catalog == nil {
		return ImportReport{}, fmt.Errorf("catalog 不能为空")
	}
	prefix := ProviderTagPrefix(req.Catalog.ProviderCode)
	keyRows := keyMapFromRows(req.GroupKeys)
	keyReport, keyErr := BuildGroupKeyReport(req.Catalog.ProviderCode, req.Catalog.BaseURL, req.GroupKeys)
	report := ImportReport{
		Mode:              "dry-run",
		ProviderCode:      req.Catalog.ProviderCode,
		ProviderName:      req.Catalog.ProviderName,
		BaseURL:           strings.TrimRight(req.Catalog.BaseURL, "/"),
		Models:            len(req.Catalog.Models),
		Vendors:           len(req.Catalog.Vendors),
		Groups:            len(req.Catalog.Groups),
		ManagedTagPrefix:  prefix,
		ChangedOptionKeys: changedOptionKeys(req.Catalog),
	}
	if keyErr != nil {
		report.InvalidRows = keyReport.InvalidRows
	}
	var count int64
	if model.DB != nil {
		_ = model.DB.Model(&model.Channel{}).Where("tag LIKE ?", prefix+"%").Count(&count).Error
	}
	report.ChannelsToReplace = count
	plans := buildChannelPlans(req.Catalog, keyRows)
	report.ChannelsToCreate = len(plans)
	missing := make(map[string]struct{})
	for _, plan := range plans {
		if plan.Key == "" && preservedKeys != nil {
			plan.Key = preservedKeys[plan.Group]
		}
		if plan.Key == "" {
			missing[plan.Group] = struct{}{}
		}
	}
	report.MissingKeyGroups = sortedStructKeys(missing)
	report.SpecialPricingModels = countSpecialPricingModels(req.Catalog.SpecialPricing)
	return report, nil
}

func applyCatalogTx(tx *gorm.DB, catalog *ProviderCatalog, plans []channelPlan) error {
	preservedKeys := map[string]string{}
	var existing []model.Channel
	if err := tx.Where("tag LIKE ?", ProviderTagPrefix(catalog.ProviderCode)+"%").Find(&existing).Error; err != nil {
		return err
	}
	for _, channel := range existing {
		if _, ok := preservedKeys[channel.Group]; !ok && channel.Key != "" {
			preservedKeys[channel.Group] = channel.Key
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
	if err := upsertVendorsTx(tx, catalog); err != nil {
		return err
	}
	if err := upsertModelsTx(tx, catalog); err != nil {
		return err
	}
	for _, plan := range plans {
		if plan.Key == "" {
			plan.Key = preservedKeys[plan.Group]
		}
		if err := insertChannelPlanTx(tx, catalog, plan); err != nil {
			return err
		}
	}
	return nil
}

func upsertVendorsTx(tx *gorm.DB, catalog *ProviderCatalog) error {
	now := common.GetTimestamp()
	for _, item := range catalog.Vendors {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		var vendor model.Vendor
		if err := tx.Where("name = ?", item.Name).FirstOrCreate(&vendor, model.Vendor{
			Name:        item.Name,
			Description: item.Description,
			Icon:        item.Icon,
			Status:      1,
			CreatedTime: now,
			UpdatedTime: now,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&vendor).Updates(map[string]any{
			"description":  item.Description,
			"icon":         item.Icon,
			"status":       1,
			"updated_time": now,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func upsertModelsTx(tx *gorm.DB, catalog *ProviderCatalog) error {
	now := common.GetTimestamp()
	for _, item := range catalog.Models {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		endpoints := "{}"
		if len(item.EndpointMap) > 0 {
			endpoints = mustJSON(item.EndpointMap)
		}
		var meta model.Model
		create := model.Model{
			ModelName:    item.Name,
			Description:  item.Description,
			Tags:         item.Tags,
			VendorID:     item.VendorID,
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
			"vendor_id":     item.VendorID,
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
		Name:          fmt.Sprintf("%s %s", catalog.ProviderName, plan.Group),
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

func updateOptions(catalog *ProviderCatalog) error {
	values := map[string]string{}
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
		if item.ModelPrice > 0 {
			modelPrice[item.Name] = item.ModelPrice
			delete(modelRatio, item.Name)
		} else {
			modelRatio[item.Name] = item.ModelRatio
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
		merged, err := mergeSpecialPricingOption(catalog.SpecialPricing)
		if err != nil {
			return err
		}
		values[special_pricing.OptionKey] = merged
	}
	return model.UpdateOptionsBulk(values)
}

func mergeSpecialPricingOption(incoming map[string]any) (string, error) {
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

func buildChannelPlans(catalog *ProviderCatalog, keys map[string]string) []channelPlan {
	grouped := map[string]*channelPlan{}
	for _, item := range catalog.Models {
		groups := item.EnableGroups
		if len(groups) == 0 {
			groups = []string{"default"}
		}
		for _, group := range groups {
			group = strings.TrimSpace(group)
			if group == "" {
				continue
			}
			tag := ProviderGroupTag(catalog.ProviderCode, group, item.ChannelType, item.ModelMapping, item.ParamOverride)
			plan, ok := grouped[tag]
			if !ok {
				plan = &channelPlan{
					Type:          item.ChannelType,
					Group:         group,
					ModelMapping:  map[string]string{},
					ParamOverride: item.ParamOverride,
					Tag:           tag,
					Key:           keys[group],
				}
				grouped[tag] = plan
			}
			plan.Models = append(plan.Models, item.Name)
			for k, v := range item.ModelMapping {
				plan.ModelMapping[k] = v
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

func ProviderGroupTag(providerCode, group string, channelType int, mapping map[string]string, override map[string]any) string {
	suffix := slug(group)
	if len(mapping) > 0 || len(override) > 0 {
		suffix += ":" + shortDigest(mustJSON(map[string]any{"m": mapping, "p": override}))
	}
	return fmt.Sprintf("%sgroup:%d:%s", ProviderTagPrefix(providerCode), channelType, suffix)
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
