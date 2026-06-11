package service

import (
	"reflect"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

// setupAutoGroupOrderTest 配置全局态并在测试结束后恢复，避免污染同包其他测试。
func setupAutoGroupOrderTest(t *testing.T, autoGroupsJSON, usableGroupsJSON, groupRatioJSON, groupGroupRatioJSON, strategy string) {
	t.Helper()

	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalGroupRatio := ratio_setting.GroupRatio2JSONString()
	originalGroupGroupRatio := ratio_setting.GroupGroupRatio2JSONString()
	originalStrategy := setting.GetAutoGroupStrategy()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatio))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(originalGroupGroupRatio))
		require.NoError(t, setting.UpdateAutoGroupStrategy(originalStrategy))
	})

	require.NoError(t, setting.UpdateAutoGroupsByJsonString(autoGroupsJSON))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(usableGroupsJSON))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groupRatioJSON))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(groupGroupRatioJSON))
	require.NoError(t, setting.UpdateAutoGroupStrategy(strategy))
}

func TestResolveAutoGroupOrderDefaultMatchesGetUserAutoGroup(t *testing.T) {
	setupAutoGroupOrderTest(t,
		`["g-b","g-a","g-c"]`,
		`{"default":"默认","g-a":"A","g-b":"B","g-c":"C"}`,
		`{"default":1,"g-a":3,"g-b":2,"g-c":1}`,
		`{}`,
		setting.AutoGroupStrategyOrder)

	require.Equal(t, GetUserAutoGroup("default"), ResolveAutoGroupOrder("default", nil, ""))
	require.Equal(t, []string{"g-b", "g-a", "g-c"}, ResolveAutoGroupOrder("default", nil, ""))
}

func TestResolveAutoGroupOrderCheapestSortsByEffectiveRatio(t *testing.T) {
	setupAutoGroupOrderTest(t,
		`["g-b","g-a","g-c"]`,
		`{"default":"默认","g-a":"A","g-b":"B","g-c":"C"}`,
		`{"default":1,"g-a":3,"g-b":2,"g-c":1}`,
		`{}`,
		setting.AutoGroupStrategyCheapest)

	require.Equal(t, []string{"g-c", "g-b", "g-a"}, ResolveAutoGroupOrder("default", nil, ""))
}

func TestResolveAutoGroupOrderCheapestUsesGroupGroupRatio(t *testing.T) {
	// 用户专属倍率把 g-a 反转为最便宜
	setupAutoGroupOrderTest(t,
		`["g-b","g-a","g-c"]`,
		`{"default":"默认","g-a":"A","g-b":"B","g-c":"C"}`,
		`{"default":1,"g-a":3,"g-b":2,"g-c":1}`,
		`{"default":{"g-a":0.1}}`,
		setting.AutoGroupStrategyCheapest)

	require.Equal(t, []string{"g-a", "g-c", "g-b"}, ResolveAutoGroupOrder("default", nil, ""))
}

func TestResolveAutoGroupOrderCheapestStableOnEqualRatio(t *testing.T) {
	setupAutoGroupOrderTest(t,
		`["g-b","g-a","g-c"]`,
		`{"default":"默认","g-a":"A","g-b":"B","g-c":"C"}`,
		`{"default":1,"g-a":1,"g-b":1,"g-c":1}`,
		`{}`,
		setting.AutoGroupStrategyCheapest)

	// 倍率全等时保持配置顺序
	require.Equal(t, []string{"g-b", "g-a", "g-c"}, ResolveAutoGroupOrder("default", nil, ""))
}

func TestResolveAutoGroupOrderTokenOverride(t *testing.T) {
	setupAutoGroupOrderTest(t,
		`["g-b","g-a","g-c"]`,
		`{"default":"默认","g-a":"A","g-b":"B","g-c":"C"}`,
		`{"default":1,"g-a":3,"g-b":2,"g-c":1}`,
		`{}`,
		setting.AutoGroupStrategyCheapest)

	// 令牌显式列表优先于站点策略：即使站点为 cheapest 也保持手动顺序
	require.Equal(t, []string{"g-a", "g-b"}, ResolveAutoGroupOrder("default", []string{"g-a", "g-b"}, ""))
	// 令牌自身选择 cheapest 时重排自己的列表（g-b=2 < g-a=3）
	require.Equal(t, []string{"g-b", "g-a"}, ResolveAutoGroupOrder("default", []string{"g-a", "g-b"}, setting.AutoGroupStrategyCheapest))
	// 非法项静默过滤
	require.Equal(t, []string{"g-c"}, ResolveAutoGroupOrder("default", []string{"not-exist", "g-c"}, ""))
	// 全部失效回退站点链路（cheapest）
	require.Equal(t, []string{"g-c", "g-b", "g-a"}, ResolveAutoGroupOrder("default", []string{"not-exist"}, ""))
}

func TestResolveAutoGroupOrderTokenCandidatesAreUsableGroups(t *testing.T) {
	// g-x 是用户可选分组但不在全局 AutoGroups 中，令牌列表仍可使用
	setupAutoGroupOrderTest(t,
		`["g-a"]`,
		`{"default":"默认","g-a":"A","g-x":"X"}`,
		`{"default":1,"g-a":2,"g-x":1}`,
		`{}`,
		setting.AutoGroupStrategyOrder)

	require.Equal(t, []string{"g-x", "g-a"}, ResolveAutoGroupOrder("default", []string{"g-x", "g-a"}, ""))
	// 站点链路（无令牌列表）仍只含全局 AutoGroups
	require.Equal(t, []string{"g-a"}, ResolveAutoGroupOrder("default", nil, ""))
}

func TestResolveAutoGroupOrderTokenStrategyAppliesToSiteChain(t *testing.T) {
	// 无令牌列表时，令牌策略覆盖站点策略
	setupAutoGroupOrderTest(t,
		`["g-a","g-b"]`,
		`{"default":"默认","g-a":"A","g-b":"B"}`,
		`{"default":1,"g-a":2,"g-b":1}`,
		`{}`,
		setting.AutoGroupStrategyOrder)

	require.Equal(t, []string{"g-a", "g-b"}, ResolveAutoGroupOrder("default", nil, ""))
	require.Equal(t, []string{"g-b", "g-a"}, ResolveAutoGroupOrder("default", nil, setting.AutoGroupStrategyCheapest))
}

func TestResolveAutoGroupOrderReturnsFreshSlice(t *testing.T) {
	setupAutoGroupOrderTest(t,
		`["g-a","g-b"]`,
		`{"default":"默认","g-a":"A","g-b":"B"}`,
		`{"default":1,"g-a":2,"g-b":1}`,
		`{}`,
		setting.AutoGroupStrategyCheapest)

	first := ResolveAutoGroupOrder("default", nil, "")
	require.Equal(t, []string{"g-b", "g-a"}, first)
	// cheapest 排序不得改变全局 AutoGroups 配置顺序
	require.Equal(t, []string{"g-a", "g-b"}, setting.GetAutoGroups())
}

func TestNormalizeTokenAutoGroups(t *testing.T) {
	setupAutoGroupOrderTest(t,
		`["g-b","g-a"]`,
		`{"default":"默认","g-a":"A","g-b":"B"}`,
		`{"default":1,"g-a":2,"g-b":1}`,
		`{}`,
		setting.AutoGroupStrategyOrder)

	// 非 auto 分组强制清空
	normalized, err := NormalizeTokenAutoGroups("default", "g-a", `["g-b"]`)
	require.NoError(t, err)
	require.Empty(t, normalized)

	// auto 分组合法配置原样保留
	normalized, err = NormalizeTokenAutoGroups("default", "auto", `["g-b"]`)
	require.NoError(t, err)
	require.Equal(t, `["g-b"]`, normalized)

	// auto 分组空配置通过
	normalized, err = NormalizeTokenAutoGroups("default", "auto", "")
	require.NoError(t, err)
	require.Empty(t, normalized)

	// auto 分组非法配置报错
	_, err = NormalizeTokenAutoGroups("default", "auto", `["not-exist"]`)
	require.Error(t, err)
}

func TestNormalizeTokenAutoGroupStrategy(t *testing.T) {
	// 非 auto 分组强制清空
	normalized, err := NormalizeTokenAutoGroupStrategy("g-a", setting.AutoGroupStrategyCheapest)
	require.NoError(t, err)
	require.Empty(t, normalized)

	// auto 分组合法值保留
	normalized, err = NormalizeTokenAutoGroupStrategy("auto", setting.AutoGroupStrategyCheapest)
	require.NoError(t, err)
	require.Equal(t, setting.AutoGroupStrategyCheapest, normalized)

	normalized, err = NormalizeTokenAutoGroupStrategy("auto", setting.AutoGroupStrategyOrder)
	require.NoError(t, err)
	require.Equal(t, setting.AutoGroupStrategyOrder, normalized)

	// 空值表示跟随站点
	normalized, err = NormalizeTokenAutoGroupStrategy("auto", "")
	require.NoError(t, err)
	require.Empty(t, normalized)

	// 非法值报错
	_, err = NormalizeTokenAutoGroupStrategy("auto", "fastest")
	require.Error(t, err)
}

func TestValidateTokenAutoGroups(t *testing.T) {
	setupAutoGroupOrderTest(t,
		`["g-b"]`,
		`{"default":"默认","g-a":"A","g-b":"B"}`,
		`{"default":1,"g-a":2,"g-b":1}`,
		`{}`,
		setting.AutoGroupStrategyOrder)

	require.NoError(t, ValidateTokenAutoGroups("default", ""))
	require.NoError(t, ValidateTokenAutoGroups("default", `["g-a","g-b"]`))
	// 候选为用户可选分组，不要求在全局 AutoGroups 中（g-a 不在其中）
	require.NoError(t, ValidateTokenAutoGroups("default", `["g-a"]`))

	require.Error(t, ValidateTokenAutoGroups("default", `{"not":"array"}`))
	require.Error(t, ValidateTokenAutoGroups("default", `["g-a","g-a"]`))
	require.Error(t, ValidateTokenAutoGroups("default", `[""]`))
	require.Error(t, ValidateTokenAutoGroups("default", `["auto"]`))
	require.Error(t, ValidateTokenAutoGroups("default", `["not-exist"]`))

	tooLong := `["` + strings.Repeat("a", maxTokenAutoGroupBytes) + `"]`
	require.Error(t, ValidateTokenAutoGroups("default", tooLong))

	manyItems := make([]string, 0, maxTokenAutoGroupItems+1)
	for i := 0; i <= maxTokenAutoGroupItems; i++ {
		manyItems = append(manyItems, "g-a")
	}
	require.Error(t, ValidateTokenAutoGroups("default", `["`+strings.Join(manyItems, `","`)+`"]`))
}

func TestResolveAutoGroupOrderDoesNotMutateBaseOrderAcrossCalls(t *testing.T) {
	setupAutoGroupOrderTest(t,
		`["g-b","g-a"]`,
		`{"default":"默认","g-a":"A","g-b":"B"}`,
		`{"default":1,"g-a":2,"g-b":1}`,
		`{}`,
		setting.AutoGroupStrategyOrder)

	before := ResolveAutoGroupOrder("default", nil, "")
	_ = ResolveAutoGroupOrder("default", []string{"g-a"}, "")
	after := ResolveAutoGroupOrder("default", nil, "")
	require.True(t, reflect.DeepEqual(before, after))
}
