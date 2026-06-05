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

	if !canUseTokenGroup("default", "auto") {
		t.Fatal("expected auto token group to be allowed when it has usable route groups")
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

	if canUseTokenGroup("vip", "auto") {
		t.Fatal("expected auto token group to be rejected without usable route groups")
	}
}
