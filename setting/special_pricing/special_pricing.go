package special_pricing

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

const OptionKey = "SpecialModelPricing"

type TableColumn struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

type TableRow struct {
	Ability          string  `json:"ability,omitempty"`
	Model            string  `json:"model,omitempty"`
	InputImages      string  `json:"input_images,omitempty"`
	Resolution       string  `json:"resolution,omitempty"`
	Version          string  `json:"version,omitempty"`
	Mode             string  `json:"mode,omitempty"`
	Duration         string  `json:"duration,omitempty"`
	Multiplier       float64 `json:"multiplier,omitempty"`
	Price            float64 `json:"price,omitempty"`
	PriceText        string  `json:"price_text,omitempty"`
	FirstSecondPrice float64 `json:"first_second_price,omitempty"`
	NextSecondPrice  float64 `json:"next_second_price,omitempty"`
	Unit             string  `json:"unit,omitempty"`
	Description      string  `json:"description,omitempty"`
}

type DisplaySection struct {
	Title       string        `json:"title,omitempty"`
	Description string        `json:"description,omitempty"`
	Unit        string        `json:"unit,omitempty"`
	Columns     []TableColumn `json:"columns,omitempty"`
	Rows        []TableRow    `json:"rows,omitempty"`
}

type DisplayConfig struct {
	Title             string           `json:"title,omitempty"`
	Description       string           `json:"description,omitempty"`
	Unit              string           `json:"unit,omitempty"`
	CreditPrice       float64          `json:"credit_unit_price,omitempty"`
	Columns           []TableColumn    `json:"columns,omitempty"`
	Rows              []TableRow       `json:"rows,omitempty"`
	Sections          []DisplaySection `json:"sections,omitempty"`
	MinPrice          float64          `json:"min_price,omitempty"`
	MinPriceUnit      string           `json:"min_price_unit,omitempty"`
	MinPriceText      string           `json:"min_price_text,omitempty"`
	BillingReady      bool             `json:"billing_enabled"`
	DisplayOnlyReason string           `json:"display_only_reason,omitempty"`
}

type PricingEntry struct {
	Key              string             `json:"key"`
	Price            float64            `json:"price,omitempty"`
	FirstSecondPrice float64            `json:"first_second_price,omitempty"`
	NextSecondPrice  float64            `json:"next_second_price,omitempty"`
	Unit             string             `json:"unit,omitempty"`
	Addons           map[string]float64 `json:"addons,omitempty"`
}

type RuleConfig struct {
	ModelName       string         `json:"model_name,omitempty"`
	Type            string         `json:"type"`
	CreditUnitPrice float64        `json:"credit_unit_price"`
	BillingEnabled  bool           `json:"billing_enabled"`
	DefaultDuration int            `json:"default_duration,omitempty"`
	MinDuration     int            `json:"min_duration,omitempty"`
	MaxDuration     int            `json:"max_duration,omitempty"`
	DefaultValue    string         `json:"default_value,omitempty"`
	DurationField   string         `json:"duration_field,omitempty"`
	ValueField      string         `json:"value_field,omitempty"`
	Multipliers     map[string]any `json:"multipliers,omitempty"`
	Entries         []PricingEntry `json:"entries,omitempty"`
	Display         DisplayConfig  `json:"display,omitempty"`
}

type Config struct {
	Version string                `json:"version,omitempty"`
	Models  map[string]RuleConfig `json:"models"`
}

type MatchResult struct {
	Rule        RuleConfig
	Multiplier  float64
	Duration    int
	UnitPrice   float64
	FinalPrice  float64
	RuleKey     string
	Spec        map[string]any
	DisplayInfo DisplayConfig
}

var (
	mu     sync.RWMutex
	config = Config{Models: map[string]RuleConfig{}}
)

func DefaultConfig() Config {
	return Config{Version: "1", Models: map[string]RuleConfig{}}
}

func UpdateByJSONString(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "{}"
	}
	var next Config
	if err := json.Unmarshal([]byte(value), &next); err != nil {
		return err
	}
	if next.Models == nil {
		next.Models = map[string]RuleConfig{}
	}
	for name, rule := range next.Models {
		if rule.ModelName == "" {
			rule.ModelName = name
		}
		if rule.CreditUnitPrice <= 0 {
			rule.CreditUnitPrice = 0.05
		}
		next.Models[name] = rule
	}
	mu.Lock()
	config = next
	mu.Unlock()
	return nil
}

func ToJSONString() string {
	mu.RLock()
	defer mu.RUnlock()
	data, _ := json.Marshal(config)
	return string(data)
}

func GetModelRule(modelName string) (RuleConfig, bool) {
	mu.RLock()
	defer mu.RUnlock()
	rule, ok := config.Models[modelName]
	return rule, ok
}

func GetDisplay(modelName string) (DisplayConfig, bool) {
	rule, ok := GetModelRule(modelName)
	if !ok {
		return DisplayConfig{}, false
	}
	display := rule.Display
	if display.CreditPrice <= 0 {
		display.CreditPrice = rule.CreditUnitPrice
	}
	if display.MinPrice <= 0 {
		display.MinPrice, display.MinPriceUnit = minDisplayPrice(display, rule)
	}
	display.BillingReady = rule.BillingEnabled
	return display, true
}

func ResolveTaskPricing(modelName string, req relaycommon.TaskSubmitReq) (MatchResult, bool, error) {
	rule, ok := GetModelRule(modelName)
	if !ok {
		return MatchResult{}, false, nil
	}
	if !rule.BillingEnabled {
		return MatchResult{}, true, fmt.Errorf("model %s has special pricing display but billing is not enabled", modelName)
	}
	key, spec := resolveRuleKey(rule, req)
	if len(rule.Entries) > 0 {
		entry, ok := lookupEntry(rule.Entries, key)
		if !ok {
			return MatchResult{}, true, fmt.Errorf("model %s does not support special pricing spec %s", modelName, key)
		}
		finalPrice := entry.Price
		duration := 0
		if entry.FirstSecondPrice > 0 || entry.NextSecondPrice > 0 {
			duration = resolveDuration(req, rule.DefaultDuration)
			if duration <= 0 {
				return MatchResult{}, true, fmt.Errorf("model %s requires a positive duration", modelName)
			}
			if rule.MinDuration > 0 && duration < rule.MinDuration {
				return MatchResult{}, true, fmt.Errorf("model %s duration %d is below minimum %d", modelName, duration, rule.MinDuration)
			}
			if rule.MaxDuration > 0 && duration > rule.MaxDuration {
				return MatchResult{}, true, fmt.Errorf("model %s duration %d exceeds maximum %d", modelName, duration, rule.MaxDuration)
			}
			finalPrice = entry.FirstSecondPrice
			if duration > 1 {
				finalPrice += float64(duration-1) * entry.NextSecondPrice
			}
		}
		for addonKey, addonPrice := range entry.Addons {
			if metadataBool(req.Metadata, addonKey) {
				finalPrice += addonPrice
				spec[addonKey] = true
			}
		}
		return MatchResult{
			Rule:        rule,
			Duration:    duration,
			UnitPrice:   finalPrice,
			FinalPrice:  finalPrice,
			RuleKey:     key,
			Spec:        spec,
			DisplayInfo: rule.Display,
		}, true, nil
	}

	duration := resolveDuration(req, rule.DefaultDuration)
	if duration <= 0 {
		return MatchResult{}, true, fmt.Errorf("model %s requires a positive duration", modelName)
	}
	if rule.MinDuration > 0 && duration < rule.MinDuration {
		return MatchResult{}, true, fmt.Errorf("model %s duration %d is below minimum %d", modelName, duration, rule.MinDuration)
	}
	if rule.MaxDuration > 0 && duration > rule.MaxDuration {
		return MatchResult{}, true, fmt.Errorf("model %s duration %d exceeds maximum %d", modelName, duration, rule.MaxDuration)
	}
	multiplier, ok := lookupMultiplier(rule.Multipliers, key)
	if !ok {
		return MatchResult{}, true, fmt.Errorf("model %s does not support special pricing spec %s", modelName, key)
	}
	unitPrice := rule.CreditUnitPrice * multiplier
	return MatchResult{
		Rule:        rule,
		Multiplier:  multiplier,
		Duration:    duration,
		UnitPrice:   unitPrice,
		FinalPrice:  unitPrice * float64(duration),
		RuleKey:     key,
		Spec:        spec,
		DisplayInfo: rule.Display,
	}, true, nil
}

func resolveDuration(req relaycommon.TaskSubmitReq, fallback int) int {
	if req.Duration > 0 {
		return req.Duration
	}
	if strings.TrimSpace(req.Seconds) != "" {
		if seconds, err := strconv.Atoi(strings.TrimSpace(req.Seconds)); err == nil && seconds > 0 {
			return seconds
		}
	}
	if value, ok := metadataNumber(req.Metadata, "duration"); ok && value > 0 {
		return int(math.Round(value))
	}
	if value, ok := metadataNumber(req.Metadata, "seconds"); ok && value > 0 {
		return int(math.Round(value))
	}
	return fallback
}

func resolveRuleKey(rule RuleConfig, req relaycommon.TaskSubmitReq) (string, map[string]any) {
	spec := map[string]any{}
	switch rule.Type {
	case "resolution_seconds":
		resolution := normalizeResolution(firstString(
			req.Size,
			metadataString(req.Metadata, "resolution"),
			metadataString(req.Metadata, "size"),
			rule.DefaultValue,
		))
		spec["resolution"] = resolution
		return resolution, spec
	case "sora_seconds_size":
		size := normalizeSize(firstString(
			req.Size,
			metadataString(req.Metadata, "size"),
			rule.DefaultValue,
		))
		spec["size"] = size
		return size, spec
	case "kling_video":
		requestModel := strings.TrimSpace(req.Model)
		if strings.EqualFold(requestModel, rule.ModelName) {
			requestModel = ""
		}
		version := firstString(metadataString(req.Metadata, "model_name"), metadataString(req.Metadata, "model"), requestModel, rule.DefaultValue)
		mode := strings.ToLower(firstString(req.Mode, metadataString(req.Metadata, "mode"), "std"))
		audio := metadataBool(req.Metadata, "with_audio")
		if audio && !strings.Contains(mode, "audio") {
			mode = mode + "-audio"
		}
		key := strings.ToLower(strings.TrimSpace(version)) + "|" + mode
		spec["version"] = version
		spec["mode"] = mode
		spec["with_audio"] = audio
		return key, spec
	case "vidu_q2":
		endpoint := strings.ToLower(firstString(metadataString(req.Metadata, "endpoint"), metadataString(req.Metadata, "path")))
		kind := strings.ToLower(firstString(metadataString(req.Metadata, "kind"), metadataString(req.Metadata, "category")))
		ability := normalizeAbility(firstString(
			metadataString(req.Metadata, "ability"),
			metadataString(req.Metadata, "action"),
			metadataString(req.Metadata, "type"),
		))
		resolution := normalizeResolution(firstString(
			req.Size,
			metadataString(req.Metadata, "resolution"),
			metadataString(req.Metadata, "size"),
			rule.DefaultValue,
		))
		imageCount := resolveImageCount(req)
		isImage := strings.Contains(endpoint, "image") || strings.Contains(kind, "image") || strings.Contains(ability, "image")
		if isImage {
			if ability == "" || ability == "text" {
				if imageCount > 0 {
					ability = "reference"
				} else {
					ability = "text"
				}
			}
			imageRange := imageCountRange(imageCount)
			spec["category"] = "image"
			spec["ability"] = ability
			spec["input_images"] = imageRange
			spec["resolution"] = resolution
			return "image|" + ability + "|" + imageRange + "|" + resolution, spec
		}
		if ability == "" {
			if req.HasImage() || strings.TrimSpace(req.Image) != "" || strings.TrimSpace(req.InputReference) != "" {
				ability = "reference"
			} else {
				ability = "text"
			}
		}
		spec["category"] = "video"
		spec["ability"] = ability
		spec["resolution"] = resolution
		return "video|" + ability + "|" + resolution, spec
	default:
		value := firstString(req.Size, metadataString(req.Metadata, rule.ValueField), rule.DefaultValue)
		spec["value"] = value
		return value, spec
	}
}

func lookupEntry(entries []PricingEntry, key string) (PricingEntry, bool) {
	candidates := []string{key, strings.ToLower(key), strings.ToUpper(key)}
	for _, entry := range entries {
		for _, candidate := range candidates {
			if entry.Key == candidate || strings.EqualFold(entry.Key, candidate) {
				return entry, true
			}
		}
	}
	return PricingEntry{}, false
}

func lookupMultiplier(m map[string]any, key string) (float64, bool) {
	if len(m) == 0 {
		return 0, false
	}
	candidates := []string{key, strings.ToLower(key), strings.ToUpper(key)}
	for _, candidate := range candidates {
		if value, ok := m[candidate]; ok {
			return numberValue(value)
		}
	}
	return 0, false
}

func numberValue(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func firstString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func normalizeResolution(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "")
	if value == "" {
		return value
	}
	if strings.Contains(value, "1080") {
		return "1080p"
	}
	if strings.Contains(value, "4k") || strings.Contains(value, "4096") {
		return "4k"
	}
	if strings.Contains(value, "2k") || strings.Contains(value, "2048") {
		return "2k"
	}
	if strings.Contains(value, "720") {
		return "720p"
	}
	if strings.Contains(value, "540") {
		return "540p"
	}
	if strings.Contains(value, "480") {
		return "480p"
	}
	return value
}

func normalizeAbility(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(value, "reference"), strings.Contains(value, "参考"), strings.Contains(value, "image"):
		return "reference"
	case strings.Contains(value, "text"), strings.Contains(value, "文生"):
		return "text"
	default:
		return value
	}
}

func resolveImageCount(req relaycommon.TaskSubmitReq) int {
	if value, ok := metadataNumber(req.Metadata, "input_image_count"); ok {
		return int(math.Round(value))
	}
	if value, ok := metadataNumber(req.Metadata, "image_count"); ok {
		return int(math.Round(value))
	}
	count := len(req.Images)
	if strings.TrimSpace(req.Image) != "" {
		count++
	}
	if strings.TrimSpace(req.InputReference) != "" && count == 0 {
		count = 1
	}
	return count
}

func imageCountRange(count int) string {
	switch {
	case count <= 0:
		return "0"
	case count <= 3:
		return "1-3"
	case count <= 7:
		return "4-7"
	default:
		return "8+"
	}
}

func minDisplayPrice(display DisplayConfig, rule RuleConfig) (float64, string) {
	minPrice := math.Inf(1)
	unit := display.Unit
	visitRow := func(row TableRow, rowUnit string) {
		price := row.Price
		if price <= 0 && row.Multiplier > 0 {
			unitPrice := display.CreditPrice
			if unitPrice <= 0 {
				unitPrice = rule.CreditUnitPrice
			}
			price = unitPrice * row.Multiplier
		}
		if price <= 0 && row.FirstSecondPrice > 0 {
			price = row.FirstSecondPrice
		}
		if price > 0 && price < minPrice {
			minPrice = price
			unit = firstString(row.Unit, rowUnit, display.Unit, "次")
		}
	}
	for _, row := range display.Rows {
		visitRow(row, display.Unit)
	}
	for _, section := range display.Sections {
		for _, row := range section.Rows {
			visitRow(row, section.Unit)
		}
	}
	if math.IsInf(minPrice, 1) {
		return 0, unit
	}
	return minPrice, unit
}

func normalizeSize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "*", "x")
	value = strings.ReplaceAll(value, " ", "")
	if value == "" {
		return value
	}
	if value == "1792x1024" {
		return "1024x1792"
	}
	if value == "1280x720" {
		return "720x1280"
	}
	return value
}

func metadataString(metadata map[string]interface{}, key string) string {
	if metadata == nil || strings.TrimSpace(key) == "" {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func metadataNumber(metadata map[string]interface{}, key string) (float64, bool) {
	if metadata == nil {
		return 0, false
	}
	return numberValue(metadata[key])
}

func metadataBool(metadata map[string]interface{}, key string) bool {
	if metadata == nil {
		return false
	}
	value := metadata[key]
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true") || v == "1" || strings.EqualFold(v, "yes")
	default:
		return false
	}
}
