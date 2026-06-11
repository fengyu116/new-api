package middleware

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
)

func TestCanUseTokenGroupAllowsAutoWhenUserHasAutoRouteGroups(t *testing.T) {
	oldUsableGroups := setting.UserUsableGroups2JSONString()
	oldAutoGroups := setting.AutoGroups2JsonString()
	t.Cleanup(func() {
		_ = setting.UpdateUserUsableGroupsByJSONString(oldUsableGroups)
		_ = setting.UpdateAutoGroupsByJsonString(oldAutoGroups)
	})

	if err := setting.UpdateUserUsableGroupsByJSONString(`{"default":"default group"}`); err != nil {
		t.Fatal(err)
	}
	if err := setting.UpdateAutoGroupsByJsonString(`["default"]`); err != nil {
		t.Fatal(err)
	}

	if !canUseTokenGroup("default", "auto", nil) {
		t.Fatal("expected auto token group to be allowed when it has usable route groups")
	}
}

func TestCanUseTokenGroupAllowsAutoWithTokenPriorityList(t *testing.T) {
	oldUsableGroups := setting.UserUsableGroups2JSONString()
	oldAutoGroups := setting.AutoGroups2JsonString()
	t.Cleanup(func() {
		_ = setting.UpdateUserUsableGroupsByJSONString(oldUsableGroups)
		_ = setting.UpdateAutoGroupsByJsonString(oldAutoGroups)
	})

	// 全局 auto 链路为空，但令牌自带的优先级列表命中用户可选分组时应放行
	if err := setting.UpdateUserUsableGroupsByJSONString(`{"vip":"vip group"}`); err != nil {
		t.Fatal(err)
	}
	if err := setting.UpdateAutoGroupsByJsonString(`[]`); err != nil {
		t.Fatal(err)
	}

	if !canUseTokenGroup("vip", "auto", []string{"vip"}) {
		t.Fatal("expected auto token group to be allowed via token priority list")
	}
	if canUseTokenGroup("vip", "auto", []string{"not-usable"}) {
		t.Fatal("expected auto token group to be rejected when token list has no usable groups")
	}
}

func TestCanUseTokenGroupRejectsAutoWhenUserHasNoAutoRouteGroups(t *testing.T) {
	oldUsableGroups := setting.UserUsableGroups2JSONString()
	oldAutoGroups := setting.AutoGroups2JsonString()
	t.Cleanup(func() {
		_ = setting.UpdateUserUsableGroupsByJSONString(oldUsableGroups)
		_ = setting.UpdateAutoGroupsByJsonString(oldAutoGroups)
	})

	if err := setting.UpdateUserUsableGroupsByJSONString(`{"vip":"vip group"}`); err != nil {
		t.Fatal(err)
	}
	if err := setting.UpdateAutoGroupsByJsonString(`["default"]`); err != nil {
		t.Fatal(err)
	}

	if canUseTokenGroup("vip", "auto", nil) {
		t.Fatal("expected auto token group to be rejected without usable route groups")
	}
}
