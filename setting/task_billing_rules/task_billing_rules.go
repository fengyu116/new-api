package task_billing_rules

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

const (
	OptionKey   = "TaskBillingRules"
	ModePerCall = "per_call"
	ModePerUnit = "per_unit"
	ModeSpecial = "special"
)

type Rule struct {
	Mode          string   `json:"mode"`
	FixedDuration int      `json:"fixed_duration,omitempty"`
	RatioKeys     []string `json:"ratio_keys,omitempty"`
}

type ResolveResult struct {
	Mode          string
	AppliedRatios map[string]float64
	Multiplier    float64
}

var (
	mu    sync.RWMutex
	rules = map[string]Rule{}
)

func UpdateByJSONString(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "{}"
	}
	next := map[string]Rule{}
	if err := json.Unmarshal([]byte(value), &next); err != nil {
		return err
	}
	for modelName, rule := range next {
		if strings.TrimSpace(modelName) == "" {
			return fmt.Errorf("task billing rule model name cannot be empty")
		}
		switch rule.Mode {
		case ModePerCall, ModePerUnit, ModeSpecial:
		default:
			return fmt.Errorf("unsupported task billing mode %q for model %s", rule.Mode, modelName)
		}
		next[modelName] = rule
	}
	mu.Lock()
	rules = next
	mu.Unlock()
	return nil
}

func ToJSONString() string {
	mu.RLock()
	defer mu.RUnlock()
	data, _ := json.Marshal(rules)
	return string(data)
}

func GetRule(modelName string) (Rule, bool) {
	mu.RLock()
	defer mu.RUnlock()
	rule, ok := rules[modelName]
	return rule, ok
}

func GetRulesCopy() map[string]Rule {
	mu.RLock()
	defer mu.RUnlock()
	result := make(map[string]Rule, len(rules))
	for modelName, rule := range rules {
		result[modelName] = rule
	}
	return result
}

func Resolve(modelName string, req relaycommon.TaskSubmitReq, estimated map[string]float64) (ResolveResult, bool, error) {
	rule, ok := GetRule(modelName)
	if !ok {
		return ResolveResult{}, false, nil
	}
	duration := requestDuration(req)
	if rule.FixedDuration > 0 && duration > 0 && duration != rule.FixedDuration {
		return ResolveResult{}, true, fmt.Errorf(
			"model %s only supports fixed duration %ds, got %ds",
			modelName,
			rule.FixedDuration,
			duration,
		)
	}
	result := ResolveResult{
		Mode:          rule.Mode,
		AppliedRatios: map[string]float64{},
		Multiplier:    1,
	}
	switch rule.Mode {
	case ModePerCall, ModeSpecial:
		return result, true, nil
	case ModePerUnit:
		for _, key := range rule.RatioKeys {
			ratio, exists := estimated[key]
			if !exists || ratio <= 0 {
				return ResolveResult{}, true, fmt.Errorf("model %s requires billing ratio %s", modelName, key)
			}
			result.AppliedRatios[key] = ratio
			result.Multiplier *= ratio
		}
		return result, true, nil
	default:
		return ResolveResult{}, true, fmt.Errorf("unsupported task billing mode %q", rule.Mode)
	}
}

func requestDuration(req relaycommon.TaskSubmitReq) int {
	if req.Duration > 0 {
		return req.Duration
	}
	if seconds := strings.TrimSpace(req.Seconds); seconds != "" {
		value, _ := strconv.Atoi(seconds)
		return value
	}
	return 0
}
