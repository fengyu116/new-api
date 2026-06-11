package relay

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/task_billing_rules"
	"github.com/QuantumNous/new-api/types"
)

func TestApplyConfiguredTaskBillingKeepsPerCallQuota(t *testing.T) {
	t.Cleanup(func() { _ = task_billing_rules.UpdateByJSONString(`{}`) })
	if err := task_billing_rules.UpdateByJSONString(`{
		"grok-video-3-10s":{"mode":"per_call","fixed_duration":10}
	}`); err != nil {
		t.Fatal(err)
	}
	priceData := types.PriceData{
		ModelPrice:  0.6,
		UsePrice:    true,
		Quota:       300000,
		OtherRatios: map[string]float64{"seconds": 10, "size": 1},
	}

	result, err := applyConfiguredTaskBilling(
		"grok-video-3-10s",
		relaycommon.TaskSubmitReq{Seconds: "10"},
		&priceData,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Configured || result.Mode != task_billing_rules.ModePerCall {
		t.Fatalf("unexpected billing result: %+v", result)
	}
	if priceData.Quota != 300000 {
		t.Fatalf("fixed per-call quota must remain 300000, got %d", priceData.Quota)
	}
	if len(priceData.OtherRatios) != 0 {
		t.Fatalf("ignored ratios must not remain in billing data: %+v", priceData.OtherRatios)
	}
}

func TestApplyConfiguredTaskBillingAppliesOnlyAllowedRatios(t *testing.T) {
	t.Cleanup(func() { _ = task_billing_rules.UpdateByJSONString(`{}`) })
	if err := task_billing_rules.UpdateByJSONString(`{
		"video-per-second":{"mode":"per_unit","ratio_keys":["seconds"]}
	}`); err != nil {
		t.Fatal(err)
	}
	priceData := types.PriceData{
		ModelPrice:  0.6,
		UsePrice:    true,
		Quota:       300000,
		OtherRatios: map[string]float64{"seconds": 10, "size": 1.666667},
	}

	result, err := applyConfiguredTaskBilling(
		"video-per-second",
		relaycommon.TaskSubmitReq{Seconds: "10", Size: "1792x1024"},
		&priceData,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if priceData.Quota != 3000000 {
		t.Fatalf("expected seconds-only quota 3000000, got %d", priceData.Quota)
	}
	if len(result.AppliedRatios) != 1 || result.AppliedRatios["seconds"] != 10 {
		t.Fatalf("unexpected applied ratios: %+v", result.AppliedRatios)
	}
}

func TestRecalculateConfiguredTaskBillingIgnoresAdjustedRatiosForPerCall(t *testing.T) {
	info := &relaycommon.RelayInfo{
		PriceData: types.PriceData{
			Quota:       300000,
			OtherRatios: map[string]float64{},
		},
	}
	result := taskBillingResult{
		Configured: true,
		Mode:       task_billing_rules.ModePerCall,
		Multiplier: 1,
	}

	quota, ratios, multiplier := recalculateConfiguredTaskBilling(
		info,
		result,
		map[string]float64{"seconds": 10},
	)

	if quota != 300000 {
		t.Fatalf("per-call quota must remain 300000, got %d", quota)
	}
	if len(ratios) != 0 {
		t.Fatalf("per-call adjusted ratios must be ignored, got %+v", ratios)
	}
	if multiplier != 1 {
		t.Fatalf("per-call multiplier must remain 1, got %v", multiplier)
	}
}

func TestRecalculateConfiguredTaskBillingFiltersAdjustedPerUnitRatios(t *testing.T) {
	info := &relaycommon.RelayInfo{
		PriceData: types.PriceData{
			Quota:       3000000,
			OtherRatios: map[string]float64{"seconds": 10},
		},
	}
	result := taskBillingResult{
		Configured:    true,
		Mode:          task_billing_rules.ModePerUnit,
		AppliedRatios: map[string]float64{"seconds": 10},
		Multiplier:    10,
	}

	quota, ratios, multiplier := recalculateConfiguredTaskBilling(
		info,
		result,
		map[string]float64{"seconds": 12, "size": 2},
	)

	if quota != 3600000 {
		t.Fatalf("expected seconds-only adjusted quota 3600000, got %d", quota)
	}
	if len(ratios) != 1 || ratios["seconds"] != 12 {
		t.Fatalf("unexpected filtered adjusted ratios: %+v", ratios)
	}
	if multiplier != 12 {
		t.Fatalf("expected adjusted multiplier 12, got %v", multiplier)
	}
}
