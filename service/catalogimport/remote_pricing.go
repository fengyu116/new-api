package catalogimport

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/setting/task_billing_rules"
)

const maxRemotePricingBytes = 16 << 20

type remotePricingEnvelope struct {
	Success bool              `json:"success"`
	Data    remotePricingData `json:"data"`
}

type remotePricingData struct {
	ModelCompletionRatio map[string]float64     `json:"model_completion_ratio"`
	GroupSpecial         map[string][]string    `json:"group_special"`
	ModelGroup           map[string]remoteGroup `json:"model_group"`
}

type remoteGroup struct {
	GroupRatio float64                     `json:"GroupRatio"`
	ModelPrice map[string]remoteModelPrice `json:"ModelPrice"`
}

type remoteModelPrice struct {
	PriceType int     `json:"priceType"`
	Price     float64 `json:"price"`
}

func AttachVectorRemotePricingValidation(catalog *ProviderCatalog, content []byte) {
	if catalog == nil {
		return
	}
	report, err := ValidateVectorRemotePricing(catalog, content)
	catalog.RemotePricingReport = &report
	if err != nil {
		catalog.ValidationErrors = append(catalog.ValidationErrors, err.Error())
	}
	if catalog.SourceHashes == nil {
		catalog.SourceHashes = map[string]string{}
	}
	catalog.SourceHashes["remote_pricing"] = fmt.Sprintf("%x", sha256.Sum256(content))
}

func AlignVectorCatalogToRemotePricing(catalog *ProviderCatalog, content []byte) error {
	if catalog == nil {
		return fmt.Errorf("catalog 不能为空")
	}
	var envelope remotePricingEnvelope
	if err := json.Unmarshal(content, &envelope); err != nil {
		return fmt.Errorf("远端 /api/pricing 不是有效 JSON: %w", err)
	}
	if !envelope.Success {
		return fmt.Errorf("远端 /api/pricing 返回失败")
	}
	modelGroups := envelope.Data.GroupSpecial
	remoteModelNames := map[string]struct{}{}
	for modelName := range modelGroups {
		remoteModelNames[modelName] = struct{}{}
	}
	for _, group := range envelope.Data.ModelGroup {
		for modelName := range group.ModelPrice {
			remoteModelNames[modelName] = struct{}{}
		}
	}
	kept := make([]CatalogModel, 0, len(catalog.Models))
	keptModels := map[string]struct{}{}
	for _, item := range catalog.Models {
		if _, ok := remoteModelNames[item.Name]; !ok {
			catalog.SkippedSpecialModels = append(catalog.SkippedSpecialModels, item.Name)
			continue
		}
		remoteGroups := modelGroups[item.Name]
		if len(remoteGroups) == 0 {
			for groupName, group := range envelope.Data.ModelGroup {
				if _, ok := group.ModelPrice[item.Name]; ok {
					remoteGroups = append(remoteGroups, groupName)
				}
			}
		}
		item.EnableGroups = intersectStringSet(item.EnableGroups, remoteGroups)
		if len(item.EnableGroups) == 0 {
			catalog.SkippedSpecialModels = append(catalog.SkippedSpecialModels, item.Name)
			continue
		}
		price, ok := firstRemotePrice(envelope.Data.ModelGroup, item.Name, item.EnableGroups)
		if ok {
			item.QuotaType = price.PriceType
			if price.PriceType == 0 {
				item.ModelRatio = price.Price
				item.ModelPrice = 0
			} else {
				item.ModelPrice = price.Price
				item.ModelRatio = 0
			}
		}
		if completion, ok := envelope.Data.ModelCompletionRatio[item.Name]; ok {
			item.CompletionRatio = completion
		}
		kept = append(kept, item)
		keptModels[item.Name] = struct{}{}
	}
	catalog.Models = kept
	catalog.SpecialPricing = filterSpecialPricingModels(catalog.SpecialPricing, keptModels)
	catalog.TaskBillingRules = filterTaskBillingRules(catalog.TaskBillingRules, keptModels)
	catalog.BillingModes = filterStringMap(catalog.BillingModes, keptModels)
	catalog.BillingExprs = filterStringMap(catalog.BillingExprs, keptModels)
	catalog.SkippedSpecialModels = uniqueStrings(catalog.SkippedSpecialModels)
	sort.Strings(catalog.SkippedSpecialModels)
	return nil
}

func ValidateVectorRemotePricing(catalog *ProviderCatalog, content []byte) (RemotePricingReport, error) {
	report := RemotePricingReport{}
	if catalog == nil {
		return report, fmt.Errorf("catalog 不能为空")
	}
	var envelope remotePricingEnvelope
	if err := json.Unmarshal(content, &envelope); err != nil {
		return report, fmt.Errorf("远端 /api/pricing 不是有效 JSON: %w", err)
	}
	if !envelope.Success {
		return report, fmt.Errorf("远端 /api/pricing 返回失败")
	}

	localModels := make(map[string]struct{}, len(catalog.Models))
	for _, item := range catalog.Models {
		localModels[item.Name] = struct{}{}
		report.CheckedModels++
		remoteGroups, ok := envelope.Data.GroupSpecial[item.Name]
		if !ok {
			report.addMismatch(item.Name, "", "model", "存在", "缺失")
			continue
		}
		if !sameStringSetAllowRemoteDefault(item.EnableGroups, remoteGroups) {
			report.addMismatch(item.Name, "", "enable_groups", sortedStrings(item.EnableGroups), sortedStrings(remoteGroups))
		}
		for _, groupName := range item.EnableGroups {
			group, ok := envelope.Data.ModelGroup[groupName]
			if !ok {
				report.addMismatch(item.Name, groupName, "group", "存在", "缺失")
				continue
			}
			localRatio, hasLocalRatio := catalog.GroupRatios[groupName]
			if !hasLocalRatio {
				report.addMismatch(item.Name, groupName, "group_ratio", "缺失", group.GroupRatio)
			} else if !almostEqual(localRatio, group.GroupRatio) {
				report.addMismatch(item.Name, groupName, "group_ratio", localRatio, group.GroupRatio)
			}
			price, ok := group.ModelPrice[item.Name]
			if !ok {
				report.addMismatch(item.Name, groupName, "price", localBasePrice(item), "缺失")
				continue
			}
			if price.PriceType != item.QuotaType {
				report.addMismatch(item.Name, groupName, "price_type", item.QuotaType, price.PriceType)
			}
			if !almostEqual(localBasePrice(item), price.Price) {
				report.addMismatch(item.Name, groupName, "base_price", localBasePrice(item), price.Price)
			}
		}
		if item.QuotaType == 0 {
			remoteCompletion, ok := envelope.Data.ModelCompletionRatio[item.Name]
			if !ok {
				report.addMismatch(item.Name, "", "completion_ratio", item.CompletionRatio, "缺失")
			} else if !almostEqual(item.CompletionRatio, remoteCompletion) {
				report.addMismatch(item.Name, "", "completion_ratio", item.CompletionRatio, remoteCompletion)
			}
		}
	}
	for _, group := range envelope.Data.ModelGroup {
		for modelName := range group.ModelPrice {
			if _, ok := localModels[modelName]; !ok {
				report.RemoteOnly = append(report.RemoteOnly, modelName)
			}
		}
	}
	report.RemoteOnly = uniqueStrings(report.RemoteOnly)
	sort.Strings(report.RemoteOnly)
	if len(report.Mismatches) > 0 {
		first := report.Mismatches[0]
		return report, fmt.Errorf(
			"远端价格严格校验失败，共 %d 项；首项模型 %s 分组 %s 字段 %s",
			len(report.Mismatches), first.ModelName, first.Group, first.Field,
		)
	}
	return report, nil
}

func intersectStringSet(local, remote []string) []string {
	remoteSet := make(map[string]struct{}, len(remote))
	for _, value := range remote {
		value = strings.TrimSpace(value)
		if value != "" {
			remoteSet[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(local))
	for _, value := range local {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := remoteSet[value]; ok {
			out = append(out, value)
		}
	}
	return uniqueStrings(out)
}

func firstRemotePrice(groups map[string]remoteGroup, modelName string, groupNames []string) (remoteModelPrice, bool) {
	for _, groupName := range groupNames {
		group, ok := groups[groupName]
		if !ok {
			continue
		}
		price, ok := group.ModelPrice[modelName]
		if ok {
			return price, true
		}
	}
	return remoteModelPrice{}, false
}

func filterSpecialPricingModels(value map[string]any, keep map[string]struct{}) map[string]any {
	if len(value) == 0 {
		return value
	}
	models := specialPricingModels(value)
	filteredModels := make(map[string]any, len(models))
	for modelName, rule := range models {
		if _, ok := keep[modelName]; ok {
			filteredModels[modelName] = rule
		}
	}
	filtered := make(map[string]any, len(value))
	for key, raw := range value {
		filtered[key] = raw
	}
	filtered["models"] = filteredModels
	return filtered
}

func filterTaskBillingRules(value map[string]task_billing_rules.Rule, keep map[string]struct{}) map[string]task_billing_rules.Rule {
	if len(value) == 0 {
		return value
	}
	out := make(map[string]task_billing_rules.Rule, len(value))
	for modelName, rule := range value {
		if _, ok := keep[modelName]; ok {
			out[modelName] = rule
		}
	}
	return out
}

func filterStringMap(value map[string]string, keep map[string]struct{}) map[string]string {
	if len(value) == 0 {
		return value
	}
	out := make(map[string]string, len(value))
	for modelName, raw := range value {
		if _, ok := keep[modelName]; ok {
			out[modelName] = raw
		}
	}
	return out
}

func FetchVectorRemotePricing(ctx context.Context, client *http.Client, baseURL string) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, remotePricingURL(baseURL), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("获取远端价格失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("远端价格接口返回 HTTP %d", response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxRemotePricingBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxRemotePricingBytes {
		return nil, fmt.Errorf("远端价格响应超过 %d 字节", maxRemotePricingBytes)
	}
	return content, nil
}

func (report *RemotePricingReport) addMismatch(modelName, group, field string, local, remote any) {
	report.Mismatches = append(report.Mismatches, RemotePricingMismatch{
		ModelName: modelName,
		Group:     group,
		Field:     field,
		Local:     local,
		Remote:    remote,
	})
}

func localBasePrice(item CatalogModel) float64 {
	if item.QuotaType == 0 {
		return item.ModelRatio
	}
	return item.ModelPrice
}

func sameStringSet(left, right []string) bool {
	a := sortedStrings(left)
	b := sortedStrings(right)
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func sameStringSetAllowRemoteDefault(local, remote []string) bool {
	if sameStringSet(local, remote) {
		return true
	}
	localSet := make(map[string]struct{}, len(local))
	for _, value := range local {
		value = strings.TrimSpace(value)
		if value != "" {
			localSet[value] = struct{}{}
		}
	}
	extraDefault := false
	for _, value := range remote {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := localSet[value]; ok {
			delete(localSet, value)
			continue
		}
		if value == "default" && !extraDefault {
			extraDefault = true
			continue
		}
		return false
	}
	return len(localSet) == 0
}

func sortedStrings(values []string) []string {
	result := uniqueStrings(values)
	sort.Strings(result)
	return result
}

func almostEqual(left, right float64) bool {
	return math.Abs(left-right) <= 1e-9*math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
}

func remotePricingURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/api/pricing"
}
