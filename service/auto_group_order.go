package service

import (
	"fmt"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

// 本文件为二开新增：auto 分组候选顺序解析（全局策略 + 令牌级覆盖）。
// 默认（策略为 order 且令牌未配置）输出与 GetUserAutoGroup 完全一致。

const (
	maxTokenAutoGroupItems = 32
	maxTokenAutoGroupBytes = 1024
)

// ResolveAutoGroupOrder 解析最终的 auto 候选分组顺序。
// tokenOrder 为令牌级自定义顺序（可为 nil）；非法项静默过滤，
// 过滤后为空则回退全局策略，保证令牌不因配置漂移而不可用。
func ResolveAutoGroupOrder(userGroup string, tokenOrder []string) []string {
	base := GetUserAutoGroup(userGroup)
	if len(tokenOrder) > 0 {
		allowed := make(map[string]struct{}, len(base))
		for _, g := range base {
			allowed[g] = struct{}{}
		}
		ordered := make([]string, 0, len(tokenOrder))
		for _, g := range tokenOrder {
			if _, ok := allowed[g]; ok {
				ordered = append(ordered, g)
			}
		}
		if len(ordered) > 0 {
			return ordered
		}
	}
	if setting.GetAutoGroupStrategy() == setting.AutoGroupStrategyCheapest {
		sort.SliceStable(base, func(i, j int) bool {
			return GetUserGroupRatio(userGroup, base[i]) < GetUserGroupRatio(userGroup, base[j])
		})
	}
	return base
}

// ResolveAutoGroupOrderFromContext 从请求上下文取令牌级顺序后解析。
func ResolveAutoGroupOrderFromContext(c *gin.Context, userGroup string) []string {
	var tokenOrder []string
	if v, exists := common.GetContextKey(c, constant.ContextKeyTokenAutoGroups); exists {
		if order, ok := v.([]string); ok {
			tokenOrder = order
		}
	}
	return ResolveAutoGroupOrder(userGroup, tokenOrder)
}

// ValidateTokenAutoGroups 校验令牌保存时提交的自定义 auto 分组顺序（JSON 数组字符串）。
// 空串视为未配置，直接通过。
func ValidateTokenAutoGroups(userGroup string, rawJSON string) error {
	if rawJSON == "" {
		return nil
	}
	if len(rawJSON) > maxTokenAutoGroupBytes {
		return fmt.Errorf("自定义自动分组配置过长")
	}
	var groups []string
	if err := common.UnmarshalJsonStr(rawJSON, &groups); err != nil {
		return fmt.Errorf("自定义自动分组格式错误，应为 JSON 字符串数组")
	}
	if len(groups) > maxTokenAutoGroupItems {
		return fmt.Errorf("自定义自动分组数量不能超过 %d 个", maxTokenAutoGroupItems)
	}
	allowed := GetUserAutoGroup(userGroup)
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, g := range allowed {
		allowedSet[g] = struct{}{}
	}
	seen := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		if g == "" {
			return fmt.Errorf("自定义自动分组包含空分组名")
		}
		if _, dup := seen[g]; dup {
			return fmt.Errorf("自定义自动分组包含重复分组：%s", g)
		}
		seen[g] = struct{}{}
		if _, ok := allowedSet[g]; !ok {
			return fmt.Errorf("分组 %s 不在可用的自动分组中", g)
		}
	}
	return nil
}
