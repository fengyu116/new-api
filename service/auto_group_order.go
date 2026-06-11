package service

import (
	"fmt"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

// 本文件为二开新增：auto 分组候选顺序解析（站点策略 + 令牌级策略与顺序覆盖）。
// 默认（策略为 order 且令牌未配置）输出与 GetUserAutoGroup 完全一致。

const (
	maxTokenAutoGroupItems = 32
	maxTokenAutoGroupBytes = 1024
)

// ResolveAutoGroupOrder 解析最终的 auto 候选分组顺序。
//   - tokenOrder：令牌级自定义优先级（可为 nil）。候选范围为用户可选分组
//     （不限于全局 AutoGroups），非法项静默过滤；过滤后为空回退站点链路。
//   - tokenStrategy：令牌级策略（order/cheapest），空为跟随站点策略；
//     但令牌已显式给出优先级列表时，站点策略不覆盖该手动顺序，
//     仅令牌自身选择 cheapest 才会重排自己的列表。
//   - cheapest 策略按有效倍率（GroupGroupRatio 优先）从低到高稳定排序。
func ResolveAutoGroupOrder(userGroup string, tokenOrder []string, tokenStrategy string) []string {
	if len(tokenOrder) > 0 {
		usable := GetUserUsableGroups(userGroup)
		ordered := make([]string, 0, len(tokenOrder))
		seen := make(map[string]struct{}, len(tokenOrder))
		for _, g := range tokenOrder {
			if g == "" || g == "auto" {
				continue
			}
			if _, dup := seen[g]; dup {
				continue
			}
			if _, ok := usable[g]; ok {
				seen[g] = struct{}{}
				ordered = append(ordered, g)
			}
		}
		if len(ordered) > 0 {
			if tokenStrategy == setting.AutoGroupStrategyCheapest {
				sortGroupsByEffectiveRatio(userGroup, ordered)
			}
			return ordered
		}
	}

	strategy := setting.GetAutoGroupStrategy()
	if tokenStrategy == setting.AutoGroupStrategyOrder || tokenStrategy == setting.AutoGroupStrategyCheapest {
		strategy = tokenStrategy
	}
	base := GetUserAutoGroup(userGroup)
	if strategy == setting.AutoGroupStrategyCheapest {
		sortGroupsByEffectiveRatio(userGroup, base)
	}
	return base
}

func sortGroupsByEffectiveRatio(userGroup string, groups []string) {
	sort.SliceStable(groups, func(i, j int) bool {
		return GetUserGroupRatio(userGroup, groups[i]) < GetUserGroupRatio(userGroup, groups[j])
	})
}

// ResolveAutoGroupOrderFromContext 从请求上下文取令牌级顺序与策略后解析。
func ResolveAutoGroupOrderFromContext(c *gin.Context, userGroup string) []string {
	var tokenOrder []string
	if v, exists := common.GetContextKey(c, constant.ContextKeyTokenAutoGroups); exists {
		if order, ok := v.([]string); ok {
			tokenOrder = order
		}
	}
	tokenStrategy := common.GetContextKeyString(c, constant.ContextKeyTokenAutoGroupStrategy)
	return ResolveAutoGroupOrder(userGroup, tokenOrder, tokenStrategy)
}

// NormalizeTokenAutoGroups 校验并规范化令牌保存时的自定义分组优先级配置。
// 令牌分组不是 auto 时强制清空；校验失败返回错误。
func NormalizeTokenAutoGroups(userGroup, tokenGroup, rawJSON string) (string, error) {
	if tokenGroup != "auto" || rawJSON == "" {
		return "", nil
	}
	if err := ValidateTokenAutoGroups(userGroup, rawJSON); err != nil {
		return "", err
	}
	return rawJSON, nil
}

// NormalizeTokenAutoGroupStrategy 校验并规范化令牌保存时的策略覆盖。
// 令牌分组不是 auto 时强制清空；空值表示跟随站点策略。
func NormalizeTokenAutoGroupStrategy(tokenGroup, strategy string) (string, error) {
	if tokenGroup != "auto" || strategy == "" {
		return "", nil
	}
	switch strategy {
	case setting.AutoGroupStrategyOrder, setting.AutoGroupStrategyCheapest:
		return strategy, nil
	default:
		return "", fmt.Errorf("无效的选组策略：%s", strategy)
	}
}

// ValidateTokenAutoGroups 校验令牌保存时提交的分组优先级（JSON 数组字符串）。
// 候选范围为用户可选分组；空串视为未配置，直接通过。
func ValidateTokenAutoGroups(userGroup string, rawJSON string) error {
	if rawJSON == "" {
		return nil
	}
	if len(rawJSON) > maxTokenAutoGroupBytes {
		return fmt.Errorf("分组优先级配置过长")
	}
	var groups []string
	if err := common.UnmarshalJsonStr(rawJSON, &groups); err != nil {
		return fmt.Errorf("分组优先级格式错误，应为 JSON 字符串数组")
	}
	if len(groups) > maxTokenAutoGroupItems {
		return fmt.Errorf("分组优先级数量不能超过 %d 个", maxTokenAutoGroupItems)
	}
	usable := GetUserUsableGroups(userGroup)
	seen := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		if g == "" || g == "auto" {
			return fmt.Errorf("分组优先级包含非法分组名")
		}
		if _, dup := seen[g]; dup {
			return fmt.Errorf("分组优先级包含重复分组：%s", g)
		}
		seen[g] = struct{}{}
		if _, ok := usable[g]; !ok {
			return fmt.Errorf("分组 %s 不在可选分组中", g)
		}
	}
	return nil
}
