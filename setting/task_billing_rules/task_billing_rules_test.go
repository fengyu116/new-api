package task_billing_rules

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestResolvePerCallFixedDurationDoesNotApplyRatios(t *testing.T) {
	t.Cleanup(func() { _ = UpdateByJSONString(`{}`) })
	if err := UpdateByJSONString(`{
		"grok-video-3-10s": {
			"mode": "per_call",
			"fixed_duration": 10,
			"ratio_keys": []
		}
	}`); err != nil {
		t.Fatal(err)
	}

	result, ok, err := Resolve("grok-video-3-10s", relaycommon.TaskSubmitReq{
		Seconds: "10",
	}, map[string]float64{
		"seconds": 10,
		"size":    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected task billing rule to match")
	}
	if result.Mode != ModePerCall {
		t.Fatalf("expected per_call mode, got %q", result.Mode)
	}
	if len(result.AppliedRatios) != 0 || result.Multiplier != 1 {
		t.Fatalf("per-call rule must not apply task ratios: %+v", result)
	}
}

func TestResolvePerCallRejectsUnexpectedDuration(t *testing.T) {
	t.Cleanup(func() { _ = UpdateByJSONString(`{}`) })
	if err := UpdateByJSONString(`{
		"grok-video-3-10s": {
			"mode": "per_call",
			"fixed_duration": 10
		}
	}`); err != nil {
		t.Fatal(err)
	}

	_, ok, err := Resolve("grok-video-3-10s", relaycommon.TaskSubmitReq{
		Seconds: "5",
	}, map[string]float64{"seconds": 5})
	if !ok {
		t.Fatal("expected task billing rule to match")
	}
	if err == nil {
		t.Fatal("expected unexpected duration to be rejected")
	}
}

func TestResolvePerUnitOnlyAppliesConfiguredRatioKeys(t *testing.T) {
	t.Cleanup(func() { _ = UpdateByJSONString(`{}`) })
	if err := UpdateByJSONString(`{
		"video-per-second": {
			"mode": "per_unit",
			"ratio_keys": ["seconds"]
		}
	}`); err != nil {
		t.Fatal(err)
	}

	result, ok, err := Resolve("video-per-second", relaycommon.TaskSubmitReq{
		Seconds: "10",
		Size:    "1792x1024",
	}, map[string]float64{
		"seconds": 10,
		"size":    1.666667,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected task billing rule to match")
	}
	if result.Multiplier != 10 {
		t.Fatalf("expected seconds-only multiplier 10, got %v", result.Multiplier)
	}
	if len(result.AppliedRatios) != 1 || result.AppliedRatios["seconds"] != 10 {
		t.Fatalf("unexpected applied ratios: %+v", result.AppliedRatios)
	}
}
