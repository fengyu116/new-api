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
		if !sameStringSet(item.EnableGroups, remoteGroups) {
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
