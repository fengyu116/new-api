package special_pricing

import (
	"math"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestResolveViduTurboSpecialPricing(t *testing.T) {
	err := UpdateByJSONString(`{
		"version":"1",
		"models":{
			"viduq3-turbo":{
				"type":"resolution_seconds",
				"credit_unit_price":0.05,
				"billing_enabled":true,
				"default_duration":5,
				"min_duration":1,
				"max_duration":16,
				"default_value":"1080p",
				"multipliers":{"1080p":16,"720p":12,"540p":8}
			}
		}
	}`)
	if err != nil {
		t.Fatalf("UpdateByJSONString error = %v", err)
	}

	got, matched, err := ResolveTaskPricing("viduq3-turbo", relaycommon.TaskSubmitReq{
		Duration: 15,
		Size:     "720p",
	})
	if err != nil {
		t.Fatalf("ResolveTaskPricing error = %v", err)
	}
	if !matched {
		t.Fatal("ResolveTaskPricing matched = false")
	}
	if got.Multiplier != 12 {
		t.Fatalf("Multiplier = %v, want 12", got.Multiplier)
	}
	if got.Duration != 15 {
		t.Fatalf("Duration = %v, want 15", got.Duration)
	}
	if math.Abs(got.FinalPrice-9) > 0.000001 {
		t.Fatalf("FinalPrice = %v, want 9", got.FinalPrice)
	}
}

func TestResolveViduRejectsUnsupportedResolution(t *testing.T) {
	err := UpdateByJSONString(`{
		"models":{
			"viduq3-turbo":{
				"type":"resolution_seconds",
				"credit_unit_price":0.05,
				"billing_enabled":true,
				"default_duration":5,
				"min_duration":1,
				"max_duration":16,
				"multipliers":{"720p":12}
			}
		}
	}`)
	if err != nil {
		t.Fatalf("UpdateByJSONString error = %v", err)
	}

	_, matched, err := ResolveTaskPricing("viduq3-turbo", relaycommon.TaskSubmitReq{
		Duration: 5,
		Size:     "4k",
	})
	if !matched {
		t.Fatal("ResolveTaskPricing matched = false")
	}
	if err == nil {
		t.Fatal("ResolveTaskPricing error = nil, want unsupported spec error")
	}
}

func TestResolveViduRejectsUnsupportedDuration(t *testing.T) {
	err := UpdateByJSONString(`{
		"models":{
			"viduq3-turbo":{
				"type":"resolution_seconds",
				"credit_unit_price":0.05,
				"billing_enabled":true,
				"default_duration":5,
				"min_duration":1,
				"max_duration":16,
				"multipliers":{"720p":12}
			}
		}
	}`)
	if err != nil {
		t.Fatalf("UpdateByJSONString error = %v", err)
	}

	_, matched, err := ResolveTaskPricing("viduq3-turbo", relaycommon.TaskSubmitReq{
		Duration: 30,
		Size:     "720p",
	})
	if !matched {
		t.Fatal("ResolveTaskPricing matched = false")
	}
	if err == nil {
		t.Fatal("ResolveTaskPricing error = nil, want unsupported duration error")
	}
}
