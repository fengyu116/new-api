package catalogimport

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/special_pricing"
	"github.com/QuantumNous/new-api/setting/task_billing_rules"

	"gorm.io/gorm"
)

type ClearRequest struct {
	ProviderCode string
	BaseURL      string
	Apply        bool
}

type ClearReport struct {
	Mode                      string   `json:"mode"`
	ProviderCode              string   `json:"provider_code"`
	BaseURL                   string   `json:"base_url"`
	ManagedTagPrefix          string   `json:"managed_tag_prefix"`
	ChannelsToDelete          int64    `json:"channels_to_delete"`
	AbilitiesToDelete         int64    `json:"abilities_to_delete"`
	GroupsToDelete            int      `json:"groups_to_delete"`
	ModelPricingItemsToDelete int      `json:"model_pricing_items_to_delete"`
	SpecialPricingToDelete    int      `json:"special_pricing_to_delete"`
	TaskBillingRulesToDelete  int      `json:"task_billing_rules_to_delete"`
	TieredBillingToDelete     int      `json:"tiered_billing_to_delete"`
	Groups                    []string `json:"groups,omitempty"`
	Models                    []string `json:"models,omitempty"`
	ChangedOptionKeys         []string `json:"changed_option_keys,omitempty"`
	Applied                   bool     `json:"applied"`
}

func ClearManagedCatalog(req ClearRequest) (ClearReport, error) {
	providerCode := strings.TrimSpace(req.ProviderCode)
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if providerCode == "" || baseURL == "" {
		return ClearReport{}, fmt.Errorf("provider_code、base_url 均不能为空")
	}
	if model.DB == nil {
		return ClearReport{}, fmt.Errorf("数据库未初始化")
	}
	plan, err := buildClearPlan(providerCode, baseURL)
	if err != nil {
		return ClearReport{}, err
	}
	report := clearReportFromPlan(plan, req.Apply)
	if !req.Apply {
		return report, nil
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if len(plan.ChannelIDs) > 0 {
			if err := tx.Where("channel_id IN ?", plan.ChannelIDs).Delete(&model.Ability{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", plan.ChannelIDs).Delete(&model.Channel{}).Error; err != nil {
				return err
			}
		}
		return model.SaveOptionsTx(tx, plan.OptionValues)
	}); err != nil {
		return report, err
	}
	if err := model.ApplyOptionValuesToMemory(plan.OptionValues); err != nil {
		return report, err
	}
	model.InitChannelCache()
	report.Applied = true
	report.Mode = "apply"
	return report, nil
}

type clearPlan struct {
	ProviderCode              string
	BaseURL                   string
	ManagedTagPrefix          string
	ChannelIDs                []int
	AbilitiesToDelete         int64
	Groups                    []string
	Models                    []string
	ChangedOptionKeys         []string
	OptionValues              map[string]string
	ModelPricingItemsToDelete int
	SpecialPricingToDelete    int
	TaskBillingRulesToDelete  int
	TieredBillingToDelete     int
}

func buildClearPlan(providerCode, baseURL string) (clearPlan, error) {
	prefix := ProviderTagPrefix(providerCode)
	var channels []model.Channel
	if err := model.DB.
		Where("tag LIKE ?", prefix+"%").
		Where("base_url = ?", baseURL).
		Find(&channels).Error; err != nil {
		return clearPlan{}, err
	}
	plan := clearPlan{
		ProviderCode:     providerCode,
		BaseURL:          baseURL,
		ManagedTagPrefix: prefix,
		OptionValues:     map[string]string{},
	}
	models := map[string]struct{}{}
	groups := map[string]struct{}{}
	for _, channel := range channels {
		plan.ChannelIDs = append(plan.ChannelIDs, channel.Id)
		for _, modelName := range strings.Split(channel.Models, ",") {
			modelName = strings.TrimSpace(modelName)
			if modelName != "" {
				models[modelName] = struct{}{}
			}
		}
		group := strings.TrimSpace(channel.Group)
		if isMarkedProviderGroup(group) {
			groups[group] = struct{}{}
		}
	}
	if len(plan.ChannelIDs) > 0 {
		if err := model.DB.Model(&model.Ability{}).Where("channel_id IN ?", plan.ChannelIDs).Count(&plan.AbilitiesToDelete).Error; err != nil {
			return clearPlan{}, err
		}
	}
	state := loadProviderImportState(providerCode, baseURL)
	for _, modelName := range state.SpecialPricingModels {
		modelName = strings.TrimSpace(modelName)
		if modelName != "" {
			models[modelName] = struct{}{}
		}
	}
	for _, modelName := range state.TaskBillingModels {
		modelName = strings.TrimSpace(modelName)
		if modelName != "" {
			models[modelName] = struct{}{}
		}
	}
	for _, modelName := range state.TieredBillingModels {
		modelName = strings.TrimSpace(modelName)
		if modelName != "" {
			models[modelName] = struct{}{}
		}
	}
	plan.Models = sortedStructKeys(models)
	plan.Groups = sortedStructKeys(groups)
	optionValues, counts, err := buildClearOptionValues(plan.Models, plan.Groups, state, providerCode, baseURL)
	if err != nil {
		return clearPlan{}, err
	}
	plan.OptionValues = optionValues
	plan.ChangedOptionKeys = sortedStringMapKeys(optionValues)
	plan.ModelPricingItemsToDelete = counts.ModelPricing
	plan.SpecialPricingToDelete = counts.SpecialPricing
	plan.TaskBillingRulesToDelete = counts.TaskBilling
	plan.TieredBillingToDelete = counts.TieredBilling
	return plan, nil
}

type clearOptionCounts struct {
	ModelPricing   int
	SpecialPricing int
	TaskBilling    int
	TieredBilling  int
}

func buildClearOptionValues(models []string, groups []string, state providerImportState, providerCode, baseURL string) (map[string]string, clearOptionCounts, error) {
	modelSet := sliceSet(models)
	groupSet := sliceSet(groups)
	values := map[string]string{}
	counts := clearOptionCounts{}
	for _, key := range []string{"ModelRatio", "ModelPrice", "CompletionRatio", "CacheRatio", "CreateCacheRatio", "AudioCompletionRatio"} {
		next, removed, err := removeFloatOptionKeys(common.OptionMap[key], modelSet)
		if err != nil {
			return nil, counts, fmt.Errorf("%s 清理失败: %w", key, err)
		}
		if removed > 0 {
			counts.ModelPricing += removed
			values[key] = next
		}
	}
	if next, removed, err := removeFloatOptionKeys(common.OptionMap["GroupRatio"], groupSet); err != nil {
		return nil, counts, fmt.Errorf("GroupRatio 清理失败: %w", err)
	} else if removed > 0 {
		values["GroupRatio"] = next
	}
	if next, removed, err := removeStringOptionKeys(common.OptionMap["UserUsableGroups"], groupSet); err != nil {
		return nil, counts, fmt.Errorf("UserUsableGroups 清理失败: %w", err)
	} else if removed > 0 {
		values["UserUsableGroups"] = next
	}
	if next, removed, err := removeSpecialPricingModels(common.OptionMap[special_pricing.OptionKey], modelSet); err != nil {
		return nil, counts, fmt.Errorf("%s 清理失败: %w", special_pricing.OptionKey, err)
	} else if removed > 0 {
		counts.SpecialPricing = removed
		values[special_pricing.OptionKey] = next
	}
	if next, removed, err := removeTaskBillingRules(common.OptionMap[task_billing_rules.OptionKey], modelSet); err != nil {
		return nil, counts, fmt.Errorf("%s 清理失败: %w", task_billing_rules.OptionKey, err)
	} else if removed > 0 {
		counts.TaskBilling = removed
		values[task_billing_rules.OptionKey] = next
	}
	tieredModels := sliceSet(state.TieredBillingModels)
	for modelName := range modelSet {
		tieredModels[modelName] = struct{}{}
	}
	if next, removed, err := removeStringOptionKeys(common.OptionMap["billing_setting.billing_mode"], tieredModels); err != nil {
		return nil, counts, fmt.Errorf("billing_setting.billing_mode 清理失败: %w", err)
	} else if removed > 0 {
		counts.TieredBilling += removed
		values["billing_setting.billing_mode"] = next
	}
	if next, removed, err := removeStringOptionKeys(common.OptionMap["billing_setting.billing_expr"], tieredModels); err != nil {
		return nil, counts, fmt.Errorf("billing_setting.billing_expr 清理失败: %w", err)
	} else if removed > 0 {
		counts.TieredBilling += removed
		values["billing_setting.billing_expr"] = next
	}
	values[ProviderImportStateOptionKey(providerCode, baseURL)] = mustJSON(providerImportState{})
	return values, counts, nil
}

func clearReportFromPlan(plan clearPlan, apply bool) ClearReport {
	mode := "dry-run"
	if apply {
		mode = "apply"
	}
	return ClearReport{
		Mode:                      mode,
		ProviderCode:              plan.ProviderCode,
		BaseURL:                   plan.BaseURL,
		ManagedTagPrefix:          plan.ManagedTagPrefix,
		ChannelsToDelete:          int64(len(plan.ChannelIDs)),
		AbilitiesToDelete:         plan.AbilitiesToDelete,
		GroupsToDelete:            len(plan.Groups),
		ModelPricingItemsToDelete: plan.ModelPricingItemsToDelete,
		SpecialPricingToDelete:    plan.SpecialPricingToDelete,
		TaskBillingRulesToDelete:  plan.TaskBillingRulesToDelete,
		TieredBillingToDelete:     plan.TieredBillingToDelete,
		Groups:                    plan.Groups,
		Models:                    plan.Models,
		ChangedOptionKeys:         plan.ChangedOptionKeys,
	}
}

func isMarkedProviderGroup(group string) bool {
	group = strings.TrimSpace(group)
	return group != "" && strings.Contains(group, ":")
}

func sliceSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func removeFloatOptionKeys(raw string, remove map[string]struct{}) (string, int, error) {
	values := map[string]float64{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return "", 0, err
		}
	}
	removed := 0
	for key := range remove {
		if _, ok := values[key]; ok {
			delete(values, key)
			removed++
		}
	}
	return mustJSON(values), removed, nil
}

func removeStringOptionKeys(raw string, remove map[string]struct{}) (string, int, error) {
	values := map[string]string{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return "", 0, err
		}
	}
	removed := 0
	for key := range remove {
		if _, ok := values[key]; ok {
			delete(values, key)
			removed++
		}
	}
	return mustJSON(values), removed, nil
}

func removeSpecialPricingModels(raw string, remove map[string]struct{}) (string, int, error) {
	if strings.TrimSpace(raw) == "" {
		raw = `{"version":"1","models":{}}`
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return "", 0, err
	}
	models, _ := values["models"].(map[string]any)
	if models == nil {
		models = map[string]any{}
		values["models"] = models
	}
	removed := 0
	for key := range remove {
		if _, ok := models[key]; ok {
			delete(models, key)
			removed++
		}
	}
	return mustJSON(values), removed, nil
}

func removeTaskBillingRules(raw string, remove map[string]struct{}) (string, int, error) {
	values := map[string]task_billing_rules.Rule{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			return "", 0, err
		}
	}
	removed := 0
	for key := range remove {
		if _, ok := values[key]; ok {
			delete(values, key)
			removed++
		}
	}
	return mustJSON(values), removed, nil
}

func sortedStringMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
