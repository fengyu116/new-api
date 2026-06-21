package catalogimport

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/task_billing_rules"
)

//go:embed vector_special_templates.json
var vectorSpecialTemplateFS embed.FS

func ParseVectorBundle(req VectorBundleRequest) (*ProviderCatalog, error) {
	catalog, err := parseVectorNormal(ParseRequest{
		ProviderCode: req.ProviderCode,
		ProviderName: req.ProviderName,
		RuleType:     RuleTypeVectorNormal,
		BaseURL:      req.BaseURL,
		Content:      req.NormalContent,
	})
	if err != nil {
		return nil, err
	}
	specialRequired := false
	requiredSpecialModels := map[string]struct{}{}
	normalModels := make(map[string]struct{}, len(catalog.Models))
	for _, item := range catalog.Models {
		normalModels[item.Name] = struct{}{}
		if isSpecialCatalogModel(item) {
			specialRequired = true
			requiredSpecialModels[item.Name] = struct{}{}
		}
	}
	if len(req.SpecialContent) == 0 {
		if specialRequired {
			return nil, fmt.Errorf("普通规则包含特殊计费模型，必须同时上传特殊规则")
		}
	} else {
		specialCatalog, err := parseVectorSpecialForBundle(ParseRequest{
			ProviderCode: req.ProviderCode,
			ProviderName: req.ProviderName,
			RuleType:     RuleTypeVectorSpecial,
			BaseURL:      req.BaseURL,
			Content:      req.SpecialContent,
		}, normalModels, requiredSpecialModels)
		if err != nil {
			return nil, err
		}
		incomingSpecialModels := specialPricingModels(specialCatalog.SpecialPricing)
		for modelName, rawRule := range incomingSpecialModels {
			if _, ok := normalModels[modelName]; !ok {
				rule, _ := rawRule.(map[string]any)
				billingEnabled, _ := rule["billing_enabled"].(bool)
				if billingEnabled {
					return nil, fmt.Errorf("特殊规则模型 %s 不存在于普通规则", modelName)
				}
				delete(incomingSpecialModels, modelName)
				catalog.SkippedSpecialModels = append(catalog.SkippedSpecialModels, modelName)
			}
		}
		sort.Strings(catalog.SkippedSpecialModels)
		for modelName := range requiredSpecialModels {
			if _, ok := incomingSpecialModels[modelName]; !ok {
				return nil, fmt.Errorf("普通规则模型 %s 需要特殊规则，但特殊规则文件未包含该模型", modelName)
			}
		}
		catalog.SpecialPricing = specialCatalog.SpecialPricing
	}
	catalog.TaskBillingRules = buildTaskBillingRules(catalog)
	if ambiguous := ambiguousTaskBillingModels(catalog); len(ambiguous) > 0 {
		return nil, fmt.Errorf("以下任务模型无法确定计费单位: %s", strings.Join(ambiguous, ", "))
	}
	catalog.SourceHashes = map[string]string{
		"normal": fmt.Sprintf("%x", sha256.Sum256(req.NormalContent)),
	}
	if len(req.SpecialContent) > 0 {
		catalog.SourceHashes["special"] = fmt.Sprintf("%x", sha256.Sum256(req.SpecialContent))
	}
	if len(req.RemotePricingContent) > 0 {
		if err := AlignVectorCatalogToRemotePricing(catalog, req.RemotePricingContent); err != nil {
			return nil, err
		}
		AttachVectorRemotePricingValidation(catalog, req.RemotePricingContent)
	}
	return catalog, nil
}

func specialPricingModels(value map[string]any) map[string]any {
	if models, ok := value["models"].(map[string]any); ok {
		return models
	}
	return value
}

func isSpecialCatalogModel(item CatalogModel) bool {
	if item.QuotaType == 4 {
		return true
	}
	_, ok := vectorSpecialModelNames[item.Name]
	return ok
}

var vectorSpecialModelNames = map[string]struct{}{
	"aigc-template-effect-vidu": {}, "aigc-video-hailuo": {}, "aigc-video-kling": {},
	"aigc-video-vidu": {}, "alibailian-video": {}, "audio1.0": {},
	"doubao-seedance-2-0-260128": {}, "doubao-seedance-2-0-fast-260128": {},
	"gemini-3-pro-image": {}, "gemini-3-pro-image-preview": {},
	"gemini-3.1-flash-image": {}, "gemini-3.1-flash-image-preview": {},
	"jimeng-videos": {}, "kling-advanced-lip-sync": {}, "kling-audio": {},
	"kling-avatar-image2video": {}, "kling-effects": {}, "kling-image": {},
	"kling-image-recognize": {}, "kling-kolors-virtual-try-on": {},
	"kling-motion-control": {}, "kling-multi-elements": {}, "kling-omni-image": {},
	"kling-omni-video": {}, "kling-video": {}, "kling-video-extend": {},
	"MiniMax-Hailuo-02": {}, "MiniMax-Hailuo-2.3": {}, "MiniMax-Hailuo-2.3-Fast": {},
	"pixverse-image-template": {}, "pixverse-lipsync": {}, "pixverse-mask-selection": {},
	"pixverse-mimic": {}, "pixverse-modify": {}, "pixverse-multi-transition": {},
	"pixverse-restyle": {}, "pixverse-sound-effect": {}, "pixverse-swap": {},
	"pixverse-upload": {}, "pixverse-video": {}, "sora-2": {}, "sora-2-pro": {},
	"suno_music_open": {}, "vidu-tts": {}, "vidu2.0": {}, "viduq1": {},
	"viduq1-classic": {}, "viduq2": {}, "viduq2-pro": {}, "viduq2-turbo": {},
	"viduq3": {}, "viduq3-mix": {}, "viduq3-pro": {}, "viduq3-turbo": {},
	"wan2.5-i2v-preview": {}, "wan2.6-i2v": {}, "wan2.6-i2v-flash": {},
}

func buildTaskBillingRules(catalog *ProviderCatalog) map[string]task_billing_rules.Rule {
	result := map[string]task_billing_rules.Rule{}
	specialModels := specialPricingModels(catalog.SpecialPricing)
	for _, item := range catalog.Models {
		if _, ok := specialModels[item.Name]; ok {
			result[item.Name] = task_billing_rules.Rule{Mode: task_billing_rules.ModeSpecial}
			continue
		}
		name := strings.ToLower(item.Name)
		endpoints := strings.ToLower(strings.Join(item.SupportedEndpointTypes, ","))
		if item.QuotaType == 1 && (strings.Contains(endpoints, "视频") || strings.Contains(endpoints, "video")) {
			if name == "sora-2-all" {
				result[item.Name] = task_billing_rules.Rule{
					Mode:      task_billing_rules.ModePerUnit,
					RatioKeys: []string{"seconds", "size"},
				}
			} else if isFixedPriceTaskModel(item) {
				result[item.Name] = task_billing_rules.Rule{
					Mode:          task_billing_rules.ModePerCall,
					FixedDuration: fixedTaskDuration(item),
				}
			}
		}
	}
	return result
}

func isFixedPriceTaskModel(item CatalogModel) bool {
	return item.ModelPrice > 0 && item.ModelRatio == 0
}

func fixedTaskDuration(item CatalogModel) int {
	values := []string{strings.ToLower(item.Name), strings.ToLower(item.Description)}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`-(\d+)\s*s$`),
		regexp.MustCompile(`(\d+)\s*s`),
		regexp.MustCompile(`(\d+)\s*秒`),
	}
	for _, value := range values {
		for _, pattern := range patterns {
			if match := pattern.FindStringSubmatch(value); len(match) == 2 {
				duration, _ := strconv.Atoi(match[1])
				return duration
			}
		}
	}
	return 0
}

func ambiguousTaskBillingModels(catalog *ProviderCatalog) []string {
	var ambiguous []string
	for _, item := range catalog.Models {
		if item.QuotaType != 1 || !isTaskLikeModel(item) {
			continue
		}
		if _, ok := catalog.TaskBillingRules[item.Name]; !ok {
			ambiguous = append(ambiguous, item.Name)
		}
	}
	sort.Strings(ambiguous)
	return ambiguous
}

func isTaskLikeModel(item CatalogModel) bool {
	endpoints := strings.ToLower(strings.Join(item.SupportedEndpointTypes, ","))
	for _, marker := range []string{"视频", "video", "音乐"} {
		if strings.Contains(endpoints, marker) {
			return true
		}
	}
	return false
}

const (
	RuleTypeVectorNormal     = "vector_normal"
	RuleTypeVectorSpecial    = "vector_special"
	RuleTypeVectorBundle     = "vector_bundle"
	RuleTypeFiveTwoOneNormal = "521_normal"
	RuleTypeShenggeNormal    = "shengge_normal"
)

func ParseCatalog(req ParseRequest) (*ProviderCatalog, error) {
	if strings.TrimSpace(req.ProviderCode) == "" {
		return nil, fmt.Errorf("provider_code 不能为空")
	}
	if strings.TrimSpace(req.BaseURL) == "" {
		return nil, fmt.Errorf("base_url 不能为空")
	}
	switch req.RuleType {
	case RuleTypeVectorNormal:
		return parseVectorNormal(req)
	case RuleTypeFiveTwoOneNormal:
		return parseVectorNormal(req)
	case RuleTypeVectorSpecial:
		return parseVectorSpecial(req)
	case RuleTypeShenggeNormal:
		return parseShenggeNormal(req)
	default:
		return nil, fmt.Errorf("不支持的规则类型: %s", req.RuleType)
	}
}

type vectorNormalFile struct {
	AutoGroups        []string                      `json:"auto_groups"`
	Vendors           []CatalogVendor               `json:"vendors"`
	Data              []vectorNormalModel           `json:"data"`
	GroupRatio        map[string]float64            `json:"group_ratio"`
	GroupModelRatio   map[string]map[string]float64 `json:"group_model_ratio"`
	UsableGroup       map[string]string             `json:"usable_group"`
	SupportedEndpoint map[string]any                `json:"supported_endpoint"`
}

type vectorNormalModel struct {
	ModelName              string      `json:"model_name"`
	Description            string      `json:"description"`
	Tags                   string      `json:"tags"`
	ModelType              string      `json:"model_type"`
	VendorID               int         `json:"vendor_id"`
	QuotaType              int         `json:"quota_type"`
	ModelRatio             float64     `json:"model_ratio"`
	ModelPrice             float64     `json:"model_price"`
	OwnerBy                string      `json:"owner_by"`
	CompletionRatio        float64     `json:"completion_ratio"`
	CacheRatio             *float64    `json:"cache_ratio"`
	CreateCacheRatio       *float64    `json:"create_cache_ratio"`
	AudioCompletionRatio   *float64    `json:"audio_completion_ratio"`
	EnableGroups           []string    `json:"enable_groups"`
	SupportedEndpointTypes []string    `json:"supported_endpoint_types"`
	SortOrder              int         `json:"sort_order"`
	StepRatios             []StepRatio `json:"step_ratios"`
}

func parseVectorNormal(req ParseRequest) (*ProviderCatalog, error) {
	var raw vectorNormalFile
	if err := json.Unmarshal(req.Content, &raw); err != nil {
		return nil, fmt.Errorf("向量普通规则必须是 JSON: %w", err)
	}
	catalog := &ProviderCatalog{
		ProviderCode: req.ProviderCode,
		ProviderName: req.ProviderName,
		BaseURL:      strings.TrimRight(req.BaseURL, "/"),
		AutoGroups:   uniqueStrings(raw.AutoGroups),
		Vendors:      raw.Vendors,
		Groups:       raw.UsableGroup,
		GroupRatios:  raw.GroupRatio,
		BillingModes: map[string]string{},
		BillingExprs: map[string]string{},
	}
	for _, row := range raw.Data {
		if strings.TrimSpace(row.ModelName) == "" {
			continue
		}
		endpointMap := make(map[string]any)
		for _, endpoint := range row.SupportedEndpointTypes {
			if value, ok := raw.SupportedEndpoint[endpoint]; ok {
				endpointMap[endpoint] = value
			}
		}
		model := CatalogModel{
			Name:                   row.ModelName,
			Description:            row.Description,
			Tags:                   row.Tags,
			ModelType:              row.ModelType,
			VendorID:               row.VendorID,
			QuotaType:              row.QuotaType,
			ModelRatio:             row.ModelRatio,
			ModelPrice:             row.ModelPrice,
			CompletionRatio:        row.CompletionRatio,
			CacheRatio:             row.CacheRatio,
			CreateCacheRatio:       row.CreateCacheRatio,
			AudioCompletionRatio:   row.AudioCompletionRatio,
			EnableGroups:           uniqueStrings(row.EnableGroups),
			SupportedEndpointTypes: uniqueStrings(row.SupportedEndpointTypes),
			SortOrder:              row.SortOrder,
			StepRatios:             row.StepRatios,
			EndpointMap:            endpointMap,
		}
		if len(model.StepRatios) > 0 {
			expr, err := compileStepRatios(model)
			if err != nil {
				return nil, fmt.Errorf("模型 %s 阶梯价格无法转换: %w", model.Name, err)
			}
			catalog.BillingModes[model.Name] = billing_setting.BillingModeTieredExpr
			catalog.BillingExprs[model.Name] = expr
		}
		model.ChannelType = channelTypeForModel(model)
		catalog.Models = append(catalog.Models, model)
	}
	return catalog, nil
}

func compileStepRatios(model CatalogModel) (string, error) {
	if model.ModelRatio <= 0 || model.CompletionRatio <= 0 {
		return "", fmt.Errorf("token 阶梯模型缺少 model_ratio 或 completion_ratio")
	}
	steps := append([]StepRatio(nil), model.StepRatios...)
	sort.SliceStable(steps, func(i, j int) bool {
		if steps[i].StepSize == steps[j].StepSize {
			return steps[i].CompletionStepSize < steps[j].CompletionStepSize
		}
		return steps[i].StepSize < steps[j].StepSize
	})
	baseInput := model.ModelRatio * 2
	branches := make([]string, 0, len(steps))
	for index, step := range steps {
		if step.StepSize <= 0 || step.PromptStepRatio <= 0 || step.CompletionStepRatio <= 0 {
			return "", fmt.Errorf("第 %d 档包含无效倍率或阈值", index+1)
		}
		normalBody := tierBody(
			baseInput*step.PromptStepRatio,
			baseInput*model.CompletionRatio*step.CompletionStepRatio,
			cacheTierPrice(baseInput, model.CacheRatio, step.CacheStepRatio, step.PromptStepRatio),
		)
		body := normalBody
		if step.PromptThinkingStepRatio > 0 || step.CompletionThinkingStepRatio > 0 {
			thinkingPrompt := step.PromptThinkingStepRatio
			if thinkingPrompt <= 0 {
				thinkingPrompt = step.PromptStepRatio
			}
			thinkingCompletion := step.CompletionThinkingStepRatio
			if thinkingCompletion <= 0 {
				thinkingCompletion = step.CompletionStepRatio
			}
			thinkingBody := tierBody(
				baseInput*thinkingPrompt,
				baseInput*model.CompletionRatio*thinkingCompletion,
				cacheTierPrice(baseInput, model.CacheRatio, step.CacheStepRatio, thinkingPrompt),
			)
			body = fmt.Sprintf(`param("enable_thinking") == false ? (%s) : (%s)`, normalBody, thinkingBody)
		}
		tierExpr := fmt.Sprintf(`tier("tier_%d", %s)`, index+1, body)
		if index < len(steps)-1 {
			condition := fmt.Sprintf("len <= %d", step.StepSize)
			if step.CompletionStepSize > 0 {
				condition += fmt.Sprintf(" && c <= %d", step.CompletionStepSize)
			}
			branches = append(branches, condition+" ? "+tierExpr+" : ")
		} else {
			branches = append(branches, tierExpr)
		}
	}
	expr := strings.Join(branches, "")
	if err := billing_setting.SmokeTestExpr(expr); err != nil {
		return "", err
	}
	return expr, nil
}

func tierBody(inputPrice, outputPrice, cachePrice float64) string {
	parts := []string{
		"p * " + formatPrice(inputPrice),
		"c * " + formatPrice(outputPrice),
	}
	if cachePrice > 0 {
		parts = append(parts, "cr * "+formatPrice(cachePrice))
	}
	return strings.Join(parts, " + ")
}

func cacheTierPrice(baseInput float64, cacheRatio *float64, cacheStepRatio, promptStepRatio float64) float64 {
	if cacheRatio == nil || *cacheRatio <= 0 {
		return 0
	}
	if cacheStepRatio <= 0 {
		cacheStepRatio = promptStepRatio
	}
	return baseInput * *cacheRatio * cacheStepRatio
}

func formatPrice(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func parseVectorSpecial(req ParseRequest) (*ProviderCatalog, error) {
	return parseVectorSpecialForBundle(req, nil, nil)
}

func parseVectorSpecialForBundle(req ParseRequest, allowedModels, requiredModels map[string]struct{}) (*ProviderCatalog, error) {
	var raw map[string]any
	if err := json.Unmarshal(req.Content, &raw); err != nil {
		cleaned, cleanErr := cleanVectorSpecialJavaScript(req.Content, allowedModels, requiredModels)
		if cleanErr != nil {
			return nil, fmt.Errorf("特殊规则必须使用标准 JSON，或使用可识别的向量特殊规则 JS: %w", cleanErr)
		}
		raw = cleaned
	}
	taskRules := map[string]task_billing_rules.Rule{}
	for modelName := range specialPricingModels(raw) {
		taskRules[modelName] = task_billing_rules.Rule{Mode: task_billing_rules.ModeSpecial}
	}
	catalog := &ProviderCatalog{
		ProviderCode:     req.ProviderCode,
		ProviderName:     req.ProviderName,
		BaseURL:          strings.TrimRight(req.BaseURL, "/"),
		SpecialPricing:   raw,
		TaskBillingRules: taskRules,
		SourceHashes: map[string]string{
			"special": fmt.Sprintf("%x", sha256.Sum256(req.Content)),
		},
		SpecialOnly: true,
	}
	return catalog, nil
}

func cleanVectorSpecialJavaScript(content []byte, allowedModels, requiredModels map[string]struct{}) (map[string]any, error) {
	source := string(content)
	discovered := discoverVectorSpecialModels(source)
	if hasQuotaTypeFourBranch(source) {
		for modelName := range requiredModels {
			discovered = append(discovered, modelName)
		}
		discovered = uniqueStrings(discovered)
		sort.Strings(discovered)
	}
	if len(discovered) == 0 {
		return nil, fmt.Errorf("没有发现可识别的特殊模型")
	}
	templates, err := loadVectorSpecialTemplates()
	if err != nil {
		return nil, err
	}
	templateModels := specialPricingModels(templates)
	models := make(map[string]any, len(discovered))
	var uncovered []string
	for _, modelName := range discovered {
		if allowedModels != nil {
			if _, ok := allowedModels[modelName]; !ok {
				continue
			}
		}
		rule, ok := templateModels[modelName]
		if !ok {
			uncovered = append(uncovered, modelName)
			continue
		}
		models[modelName] = rule
	}
	if len(uncovered) > 0 {
		return nil, fmt.Errorf("发现特殊模型但没有转换器: %s", strings.Join(uncovered, ", "))
	}
	return map[string]any{
		"version": "1",
		"models":  models,
	}, nil
}

func discoverVectorSpecialModels(source string) []string {
	found := map[string]struct{}{}
	for modelName := range vectorSpecialModelNames {
		if quotedLiteralPresent(source, modelName) {
			found[modelName] = struct{}{}
		}
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`else\s+if\s*\(\s*["']([^"']+)["']\s*===\s*[A-Za-z_$][\w$?.]*`),
		regexp.MustCompile(`else\s+if\s*\(\s*[A-Za-z_$][\w$?.]*\s*===\s*["']([^"']+)["']`),
		regexp.MustCompile(`["']([^"']+)["']\s*===\s*[A-Za-z_$][\w$?.]*\s*\?\s*[A-Za-z_$][\w$]*\.push\s*\(`),
	}
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(source, -1) {
			if len(match) == 2 && looksLikeModelName(match[1]) {
				found[match[1]] = struct{}{}
			}
		}
	}
	arrayPattern := regexp.MustCompile(`new\s+Set\s*\(\s*\[([^\]]+)\]\s*\)\.has\s*\([^)]*model_name`)
	literalPattern := regexp.MustCompile(`["']([^"']+)["']`)
	for _, match := range arrayPattern.FindAllStringSubmatch(source, -1) {
		for _, literal := range literalPattern.FindAllStringSubmatch(match[1], -1) {
			if len(literal) == 2 && looksLikeModelName(literal[1]) {
				found[literal[1]] = struct{}{}
			}
		}
	}
	for _, nonSpecial := range []string{"aigc-image-gem", "aigc-image-hunyuan", "aigc-image-qwen"} {
		delete(found, nonSpecial)
	}
	result := make([]string, 0, len(found))
	for modelName := range found {
		result = append(result, modelName)
	}
	sort.Strings(result)
	return result
}

func hasQuotaTypeFourBranch(source string) bool {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`4\s*[!=]==?\s*[A-Za-z_$][\w$?.]*quota_type`),
		regexp.MustCompile(`[A-Za-z_$][\w$?.]*quota_type\s*[!=]==?\s*4`),
	}
	for _, pattern := range patterns {
		if pattern.MatchString(source) {
			return true
		}
	}
	return false
}

func loadVectorSpecialTemplates() (map[string]any, error) {
	content, err := vectorSpecialTemplateFS.ReadFile("vector_special_templates.json")
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(content, &raw); err != nil {
		return nil, fmt.Errorf("内置向量特殊规则模板无效: %w", err)
	}
	return raw, nil
}

func quotedLiteralPresent(source, value string) bool {
	return strings.Contains(source, `"`+value+`"`) || strings.Contains(source, `'`+value+`'`)
}

func looksLikeModelName(value string) bool {
	if value == "" || len(value) > 100 || strings.ContainsFunc(value, unicode.IsSpace) {
		return false
	}
	ok, _ := regexp.MatchString(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`, value)
	return ok
}

type shenggeGroup struct {
	Group      string         `json:"group"`
	Path       string         `json:"path"`
	Multiplier float64        `json:"multiplier"`
	Models     []shenggeModel `json:"models"`
	rawModels  []json.RawMessage
}

type shenggeModel struct {
	Name        string   `json:"name"`
	BaseModel   string   `json:"base_model"`
	Resolutions []string `json:"resolutions"`
	Notes       string   `json:"notes"`
}

func (g *shenggeGroup) UnmarshalJSON(data []byte) error {
	type alias shenggeGroup
	var raw struct {
		alias
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*g = shenggeGroup(raw.alias)
	g.rawModels = raw.Models
	return nil
}

func parseShenggeNormal(req ParseRequest) (*ProviderCatalog, error) {
	var groups []shenggeGroup
	if err := json.Unmarshal(req.Content, &groups); err != nil {
		return nil, fmt.Errorf("胜哥普通规则必须是 JSON: %w", err)
	}
	catalog := &ProviderCatalog{
		ProviderCode: req.ProviderCode,
		ProviderName: req.ProviderName,
		BaseURL:      strings.TrimRight(req.BaseURL, "/"),
		Groups:       map[string]string{},
		GroupRatios:  map[string]float64{},
	}
	seen := map[string]int{}
	for _, group := range groups {
		groupName := strings.TrimSpace(group.Group)
		if groupName == "" {
			continue
		}
		catalog.Groups[groupName] = group.Path
		if group.Multiplier > 0 {
			catalog.GroupRatios[groupName] = group.Multiplier
		}
		for _, rawModel := range group.rawModels {
			model, ok := parseShenggeModel(rawModel)
			if !ok {
				continue
			}
			idx, exists := seen[model.Name]
			if !exists {
				model.ModelRatio = group.Multiplier
				if model.ModelRatio == 0 {
					model.ModelRatio = 1
				}
				model.EnableGroups = []string{groupName}
				model.SupportedEndpointTypes = []string{endpointTypeFromPath(group.Path)}
				model.ChannelType = channelTypeForPath(group.Path)
				if model.BaseModel != "" && model.BaseModel != model.Name {
					model.ModelMapping = map[string]string{model.Name: model.BaseModel}
				}
				catalog.Models = append(catalog.Models, model)
				seen[model.Name] = len(catalog.Models) - 1
				continue
			}
			catalog.Models[idx].EnableGroups = uniqueStrings(append(catalog.Models[idx].EnableGroups, groupName))
		}
	}
	return catalog, nil
}

func parseShenggeModel(raw json.RawMessage) (CatalogModel, bool) {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		asString = strings.TrimSpace(asString)
		if asString == "" {
			return CatalogModel{}, false
		}
		return CatalogModel{Name: asString}, true
	}
	var object shenggeModel
	if err := json.Unmarshal(raw, &object); err != nil || strings.TrimSpace(object.Name) == "" {
		return CatalogModel{}, false
	}
	model := CatalogModel{Name: object.Name, BaseModel: object.BaseModel, Description: object.Notes, Tags: strings.Join(object.Resolutions, ","), Extra: map[string]any{}}
	model.Extra["resolutions"] = object.Resolutions
	if object.BaseModel != "" {
		model.Extra["base_model"] = object.BaseModel
	}
	model.ModelMapping = map[string]string{}
	if object.BaseModel != "" && object.BaseModel != object.Name {
		model.ModelMapping[object.Name] = object.BaseModel
	}
	return model, true
}

func channelTypeForModel(model CatalogModel) int {
	name := strings.ToLower(model.Name)
	endpoints := strings.ToLower(strings.Join(model.SupportedEndpointTypes, ","))
	switch {
	case strings.HasPrefix(name, "vidu") || strings.Contains(endpoints, "vidu"):
		return constant.ChannelTypeVidu
	case strings.HasPrefix(name, "kling-") || strings.Contains(endpoints, "kling"):
		return constant.ChannelTypeKling
	case strings.Contains(name, "sora") || strings.Contains(endpoints, "sora"):
		return constant.ChannelTypeSora
	case strings.Contains(name, "seedance") || strings.Contains(endpoints, "doubao"):
		return constant.ChannelTypeDoubaoVideo
	case strings.Contains(endpoints, "anthropic"):
		return constant.ChannelTypeAnthropic
	case strings.Contains(endpoints, "gemini"):
		return constant.ChannelTypeGemini
	case strings.Contains(endpoints, "ali"):
		return constant.ChannelTypeAli
	default:
		return constant.ChannelTypeOpenAI
	}
}

func channelTypeForPath(path string) int {
	path = strings.ToLower(path)
	switch {
	case strings.Contains(path, "claude"):
		return constant.ChannelTypeAnthropic
	case strings.Contains(path, "gemini"):
		return constant.ChannelTypeGemini
	default:
		return constant.ChannelTypeOpenAI
	}
}

func endpointTypeFromPath(path string) string {
	path = strings.ToLower(strings.TrimSpace(path))
	switch {
	case strings.Contains(path, "claude"):
		return "anthropic"
	case strings.Contains(path, "gemini"):
		return "gemini"
	default:
		return "openai"
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func maskKey(key string) string {
	if len(key) <= 14 {
		if len(key) <= 4 {
			return "***"
		}
		return key[:4] + "***"
	}
	return key[:10] + "***" + key[len(key)-6:]
}

var requiredTokenHeaders = []string{"名称", "状态", "分组", "密钥（sk-前缀）", "可用模型"}

func ParseTokenRowsFromSheetRows(rows [][]string) ([]TokenRow, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("Excel 文件没有可读取的数据")
	}
	headers := rows[0]
	index := map[string]int{}
	for i, header := range headers {
		index[strings.TrimSpace(header)] = i
	}
	for _, header := range requiredTokenHeaders {
		if _, ok := index[header]; !ok {
			return nil, fmt.Errorf("Excel 缺少必要列: %s", header)
		}
	}
	out := make([]TokenRow, 0, len(rows)-1)
	for offset, row := range rows[1:] {
		if isEmptyRow(row) {
			continue
		}
		get := func(header string) string {
			i := index[header]
			if i >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[i])
		}
		out = append(out, TokenRow{
			RowNumber: offset + 2,
			Name:      get("名称"),
			Status:    get("状态"),
			Group:     get("分组"),
			Key:       get("密钥（sk-前缀）"),
			Models:    get("可用模型"),
		})
	}
	return out, nil
}

func BuildGroupKeyReport(providerCode, baseURL string, rows []TokenRow) (GroupKeyReport, error) {
	report := GroupKeyReport{ProviderCode: providerCode, BaseURL: strings.TrimRight(baseURL, "/")}
	keysByGroup := map[string]string{}
	for _, row := range rows {
		rowErrors := make([]string, 0)
		if row.Status != "已启用" {
			rowErrors = append(rowErrors, "状态不是已启用")
		}
		if row.Group == "" {
			rowErrors = append(rowErrors, "分组为空")
		}
		if !strings.HasPrefix(row.Key, "sk-") {
			rowErrors = append(rowErrors, "密钥不是 sk- 前缀")
		}
		if previous, ok := keysByGroup[row.Group]; ok && previous != row.Key {
			rowErrors = append(rowErrors, "同一分组出现多个不同密钥")
		}
		if len(rowErrors) > 0 {
			report.InvalidRows = append(report.InvalidRows, GroupKeyReportError{Row: row.RowNumber, Name: row.Name, Group: row.Group, KeyMask: maskKey(row.Key), Errors: rowErrors})
			continue
		}
		keysByGroup[row.Group] = row.Key
		report.Groups = append(report.Groups, GroupKeyReportRow{Row: row.RowNumber, Group: row.Group, Name: row.Name, Status: row.Status, Models: row.Models, KeyMask: maskKey(row.Key)})
	}
	report.ValidRows = len(report.Groups)
	if len(report.InvalidRows) > 0 {
		return report, fmt.Errorf("分组 key 文件存在校验错误")
	}
	return report, nil
}

func isEmptyRow(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

func ParseTokenRowsFromXLSX(content []byte) ([]TokenRow, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, fmt.Errorf("无法读取 xlsx: %w", err)
	}
	rows, err := readSheetRows(reader)
	if err != nil {
		return nil, err
	}
	return ParseTokenRowsFromSheetRows(rows)
}

func readSheetRows(reader *zip.Reader) ([][]string, error) {
	sharedStrings, _ := readSharedStrings(reader)
	sheetBytes, err := readZipFile(reader, "xl/worksheets/sheet1.xml")
	if err != nil {
		return nil, err
	}
	type cell struct {
		Ref    string `xml:"r,attr"`
		Type   string `xml:"t,attr"`
		Value  string `xml:"v"`
		Inline string `xml:"is>t"`
	}
	type row struct {
		Cells []cell `xml:"c"`
	}
	type worksheet struct {
		Rows []row `xml:"sheetData>row"`
	}
	var ws worksheet
	if err := xml.Unmarshal(sheetBytes, &ws); err != nil {
		return nil, err
	}
	rows := make([][]string, 0, len(ws.Rows))
	for _, r := range ws.Rows {
		values := map[int]string{}
		maxIndex := -1
		for _, c := range r.Cells {
			idx := columnIndex(c.Ref)
			if idx > maxIndex {
				maxIndex = idx
			}
			values[idx] = resolveCellValue(c.Type, c.Value, c.Inline, sharedStrings)
		}
		rowValues := make([]string, maxIndex+1)
		for i := 0; i <= maxIndex; i++ {
			rowValues[i] = strings.TrimSpace(values[i])
		}
		rows = append(rows, rowValues)
	}
	return rows, nil
}

func readSharedStrings(reader *zip.Reader) ([]string, error) {
	data, err := readZipFile(reader, "xl/sharedStrings.xml")
	if err != nil {
		return nil, err
	}
	type si struct {
		Texts []string `xml:"t"`
	}
	type sst struct {
		Items []si `xml:"si"`
	}
	var parsed sst
	if err := xml.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(parsed.Items))
	for _, item := range parsed.Items {
		out = append(out, strings.Join(item.Texts, ""))
	}
	return out, nil
}

func readZipFile(reader *zip.Reader, name string) ([]byte, error) {
	cleanName := filepath.ToSlash(name)
	for _, f := range reader.File {
		if filepath.ToSlash(f.Name) != cleanName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("xlsx 缺少文件: %s", name)
}

func resolveCellValue(cellType, raw, inline string, sharedStrings []string) string {
	if cellType == "s" && raw != "" {
		var idx int
		_, _ = fmt.Sscanf(raw, "%d", &idx)
		if idx >= 0 && idx < len(sharedStrings) {
			return sharedStrings[idx]
		}
	}
	if cellType == "inlineStr" {
		return inline
	}
	return raw
}

var cellColumnPattern = regexp.MustCompile(`^[A-Z]+`)

func columnIndex(ref string) int {
	letters := cellColumnPattern.FindString(ref)
	value := 0
	for len(letters) > 0 {
		r, size := utf8.DecodeRuneInString(letters)
		if !unicode.IsUpper(r) {
			break
		}
		value = value*26 + int(r-'A') + 1
		letters = letters[size:]
	}
	if value == 0 {
		return 0
	}
	return value - 1
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
