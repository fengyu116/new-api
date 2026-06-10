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

	require.Equal(t, GetUserAutoGroup("default"), ResolveAutoGroupOrder("default", nil))
	require.Equal(t, []string{"g-b", "g-a", "g-c"}, ResolveAutoGroupOrder("default", nil))
}

func TestResolveAutoGroupOrderCheapestSortsByEffectiveRatio(t *testing.T) {
	setupAutoGroupOrderTest(t,
		`["g-b","g-a","g-c"]`,
		`{"default":"默认","g-a":"A","g-b":"B","g-c":"C"}`,
		`{"default":1,"g-a":3,"g-b":2,"g-c":1}`,
		`{}`,
		setting.AutoGroupStrategyCheapest)

	require.Equal(t, []string{"g-c", "g-b", "g-a"}, ResolveAutoGroupOrder("default", nil))
}

func TestResolveAutoGroupOrderCheapestUsesGroupGroupRatio(t *testing.T) {
	// 用户专属倍率把 g-a 反转为最便宜
	setupAutoGroupOrderTest(t,
		`["g-b","g-a","g-c"]`,
		`{"default":"默认","g-a":"A","g-b":"B","g-c":"C"}`,
		`{"default":1,"g-a":3,"g-b":2,"g-c":1}`,
		`{"default":{"g-a":0.1}}`,
		setting.AutoGroupStrategyCheapest)

	require.Equal(t, []string{"g-a", "g-c", "g-b"}, ResolveAutoGroupOrder("default", nil))
}

func TestResolveAutoGroupOrderCheapestStableOnEqualRatio(t *testing.T) {
	setupAutoGroupOrderTest(t,
		`["g-b","g-a","g-c"]`,
		`{"default":"默认","g-a":"A","g-b":"B","g-c":"C"}`,
		`{"default":1,"g-a":1,"g-b":1,"g-c":1}`,
		`{}`,
		setting.AutoGroupStrategyCheapest)

	// 倍率全等时保持配置顺序
	require.Equal(t, []string{"g-b", "g-a", "g-c"}, ResolveAutoGroupOrder("default", nil))
}

func TestResolveAutoGroupOrderTokenOverride(t *testing.T) {
	setupAutoGroupOrderTest(t,
		`["g-b","g-a","g-c"]`,
		`{"default":"默认","g-a":"A","g-b":"B","g-c":"C"}`,
		`{"default":1,"g-a":3,"g-b":2,"g-c":1}`,
		`{}`,
		setting.AutoGroupStrategyCheapest)

	// 令牌顺序完全优先，不叠加全局策略
	require.Equal(t, []string{"g-a", "g-b"}, ResolveAutoGroupOrder("default", []string{"g-a", "g-b"}))
	// 非法项静默过滤
	require.Equal(t, []string{"g-c"}, ResolveAutoGroupOrder("default", []string{"not-exist", "g-c"}))
	// 全部失效回退全局策略（cheapest）
	require.Equal(t, []string{"g-c", "g-b", "g-a"}, ResolveAutoGroupOrder("default", []string{"not-exist"}))
}

func TestResolveAutoGroupOrderReturnsFreshSlice(t *testing.T) {
	setupAutoGroupOrderTest(t,
		`["g-a","g-b"]`,
		`{"default":"默认","g-a":"A","g-b":"B"}`,
		`{"default":1,"g-a":2,"g-b":1}`,
		`{}`,
		setting.AutoGroupStrategyCheapest)

	first := ResolveAutoGroupOrder("default", nil)
	require.Equal(t, []string{"g-b", "g-a"}, first)
	// cheapest 排序不得改变全局 AutoGroups 配置顺序
	require.Equal(t, []string{"g-a", "g-b"}, setting.GetAutoGroups())
}

func TestValidateTokenAutoGroups(t *testing.T) {
	setupAutoGroupOrderTest(t,
		`["g-b","g-a"]`,
		`{"default":"默认","g-a":"A","g-b":"B"}`,
		`{"default":1,"g-a":2,"g-b":1}`,
		`{}`,
		setting.AutoGroupStrategyOrder)

	require.NoError(t, ValidateTokenAutoGroups("default", ""))
	require.NoError(t, ValidateTokenAutoGroups("default", `["g-a","g-b"]`))

	require.Error(t, ValidateTokenAutoGroups("default", `{"not":"array"}`))
	require.Error(t, ValidateTokenAutoGroups("default", `["g-a","g-a"]`))
	require.Error(t, ValidateTokenAutoGroups("default", `[""]`))
	require.Error(t, ValidateTokenAutoGroups("default", `["not-exist"]`))

	tooLong := `["` + strings.Repeat("a", maxTokenAutoGroupBytes) + `"]`
	require.Error(t, ValidateTokenAutoGroups("default", tooLong))

	manyItems := make([]string, 0, maxTokenAutoGroupItems+1)
	for i := 0; i <= maxTokenAutoGroupItems; i++ {
		manyItems = append(manyItems, "g-a")
	}
	require.Error(t, ValidateTokenAutoGroups("default", `["`+strings.Join(manyItems, `","`)+`"]`))
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

func TestResolveAutoGroupOrderDoesNotMutateBaseOrderAcrossCalls(t *testing.T) {
	setupAutoGroupOrderTest(t,
		`["g-b","g-a"]`,
		`{"default":"默认","g-a":"A","g-b":"B"}`,
		`{"default":1,"g-a":2,"g-b":1}`,
		`{}`,
		setting.AutoGroupStrategyOrder)

	before := ResolveAutoGroupOrder("default", nil)
	_ = ResolveAutoGroupOrder("default", []string{"g-a"})
	after := ResolveAutoGroupOrder("default", nil)
	require.True(t, reflect.DeepEqual(before, after))
}
