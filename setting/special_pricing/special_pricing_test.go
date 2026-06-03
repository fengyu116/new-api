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

func TestResolveViduQ2VideoFirstThenSeconds(t *testing.T) {
	err := UpdateByJSONString(`{
		"models":{
			"viduq2":{
				"type":"vidu_q2",
				"credit_unit_price":0.05,
				"billing_enabled":true,
				"default_duration":5,
				"min_duration":1,
				"max_duration":16,
				"default_value":"1080p",
				"entries":[
					{"key":"video|text|720p","first_second_price":0.75,"next_second_price":0.25,"addons":{"with_audio":0.75,"recommend_prompt":0.5}},
					{"key":"image|text|0|1080p","price":0.3}
				]
			}
		}
	}`)
	if err != nil {
		t.Fatalf("UpdateByJSONString error = %v", err)
	}

	got, matched, err := ResolveTaskPricing("viduq2", relaycommon.TaskSubmitReq{
		Duration: 5,
		Size:     "720p",
		Metadata: map[string]interface{}{"ability": "text", "with_audio": true},
	})
	if err != nil {
		t.Fatalf("ResolveTaskPricing error = %v", err)
	}
	if !matched {
		t.Fatal("ResolveTaskPricing matched = false")
	}
	want := 0.75 + 4*0.25 + 0.75
	if math.Abs(got.FinalPrice-want) > 0.000001 {
		t.Fatalf("FinalPrice = %v, want %v", got.FinalPrice, want)
	}
}

func TestResolveViduQ2ImageFixedSpec(t *testing.T) {
	err := UpdateByJSONString(`{
		"models":{
			"viduq2":{
				"type":"vidu_q2",
				"credit_unit_price":0.05,
				"billing_enabled":true,
				"default_value":"1080p",
				"entries":[
					{"key":"image|reference|4-7|4k","price":1.5}
				]
			}
		}
	}`)
	if err != nil {
		t.Fatalf("UpdateByJSONString error = %v", err)
	}

	got, matched, err := ResolveTaskPricing("viduq2", relaycommon.TaskSubmitReq{
		Size:   "4K",
		Images: []string{"1", "2", "3", "4"},
		Metadata: map[string]interface{}{
			"endpoint": "/ent/v2/reference2image",
			"ability":  "reference",
		},
	})
	if err != nil {
		t.Fatalf("ResolveTaskPricing error = %v", err)
	}
	if !matched {
		t.Fatal("ResolveTaskPricing matched = false")
	}
	if math.Abs(got.FinalPrice-1.5) > 0.000001 {
		t.Fatalf("FinalPrice = %v, want 1.5", got.FinalPrice)
	}
}

func TestResolveViduQ2RejectsUnsupportedSpec(t *testing.T) {
	err := UpdateByJSONString(`{
		"models":{
			"viduq2":{
				"type":"vidu_q2",
				"credit_unit_price":0.05,
				"billing_enabled":true,
				"default_duration":5,
				"default_value":"1080p",
				"entries":[
					{"key":"video|text|720p","first_second_price":0.75,"next_second_price":0.25}
				]
			}
		}
	}`)
	if err != nil {
		t.Fatalf("UpdateByJSONString error = %v", err)
	}

	_, matched, err := ResolveTaskPricing("viduq2", relaycommon.TaskSubmitReq{
		Duration: 5,
		Size:     "4K",
		Metadata: map[string]interface{}{"ability": "text"},
	})
	if !matched {
		t.Fatal("ResolveTaskPricing matched = false")
	}
	if err == nil {
		t.Fatal("ResolveTaskPricing error = nil, want unsupported spec error")
	}
}
