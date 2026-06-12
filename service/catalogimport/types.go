package catalogimport

import "github.com/QuantumNous/new-api/setting/task_billing_rules"

type ParseRequest struct {
	ProviderCode string
	ProviderName string
	RuleType     string
	BaseURL      string
	Content      []byte
}

type CatalogVendor struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

type CatalogModel struct {
	Name                   string            `json:"model_name"`
	Description            string            `json:"description,omitempty"`
	Tags                   string            `json:"tags,omitempty"`
	ModelType              string            `json:"model_type,omitempty"`
	VendorID               int               `json:"vendor_id,omitempty"`
	QuotaType              int               `json:"quota_type,omitempty"`
	ModelRatio             float64           `json:"model_ratio,omitempty"`
	ModelPrice             float64           `json:"model_price,omitempty"`
	CompletionRatio        float64           `json:"completion_ratio,omitempty"`
	CacheRatio             *float64          `json:"cache_ratio,omitempty"`
	CreateCacheRatio       *float64          `json:"create_cache_ratio,omitempty"`
	AudioCompletionRatio   *float64          `json:"audio_completion_ratio,omitempty"`
	EnableGroups           []string          `json:"enable_groups,omitempty"`
	SupportedEndpointTypes []string          `json:"supported_endpoint_types,omitempty"`
	SortOrder              int               `json:"sort_order,omitempty"`
	BaseModel              string            `json:"base_model,omitempty"`
	ModelMapping           map[string]string `json:"model_mapping,omitempty"`
	ParamOverride          map[string]any    `json:"param_override,omitempty"`
	EndpointMap            map[string]any    `json:"endpoint_map,omitempty"`
	ChannelType            int               `json:"channel_type,omitempty"`
	StepRatios             []StepRatio       `json:"step_ratios,omitempty"`
	Extra                  map[string]any    `json:"extra,omitempty"`
}

type StepRatio struct {
	StepSize                    int     `json:"step_size"`
	CompletionStepSize          int     `json:"completion_step_size"`
	PromptStepRatio             float64 `json:"prompt_step_ratio"`
	CompletionStepRatio         float64 `json:"completion_step_ratio"`
	CacheStepRatio              float64 `json:"cache_step_ratio"`
	PromptThinkingStepRatio     float64 `json:"prompt_thinking_step_ratio"`
	CompletionThinkingStepRatio float64 `json:"completion_thinking_step_ratio"`
}

type ProviderCatalog struct {
	ProviderCode         string                             `json:"provider_code"`
	ProviderName         string                             `json:"provider_name"`
	BaseURL              string                             `json:"base_url"`
	AutoGroups           []string                           `json:"auto_groups,omitempty"`
	Vendors              []CatalogVendor                    `json:"vendors,omitempty"`
	Groups               map[string]string                  `json:"groups,omitempty"`
	GroupRatios          map[string]float64                 `json:"group_ratios,omitempty"`
	Models               []CatalogModel                     `json:"models,omitempty"`
	SpecialPricing       map[string]any                     `json:"special_pricing,omitempty"`
	TaskBillingRules     map[string]task_billing_rules.Rule `json:"task_billing_rules,omitempty"`
	BillingModes         map[string]string                  `json:"billing_modes,omitempty"`
	BillingExprs         map[string]string                  `json:"billing_exprs,omitempty"`
	RemotePricingReport  *RemotePricingReport               `json:"remote_pricing_report,omitempty"`
	ValidationErrors     []string                           `json:"validation_errors,omitempty"`
	SourceHashes         map[string]string                  `json:"source_hashes,omitempty"`
	SpecialOnly          bool                               `json:"-"`
	SkippedSpecialModels []string                           `json:"-"`
}

type VectorBundleRequest struct {
	ProviderCode         string
	ProviderName         string
	BaseURL              string
	NormalContent        []byte
	SpecialContent       []byte
	RemotePricingContent []byte
}

type RemotePricingMismatch struct {
	ModelName string `json:"model_name,omitempty"`
	Group     string `json:"group,omitempty"`
	Field     string `json:"field"`
	Local     any    `json:"local,omitempty"`
	Remote    any    `json:"remote,omitempty"`
}

type RemotePricingReport struct {
	CheckedModels int                     `json:"checked_models"`
	Mismatches    []RemotePricingMismatch `json:"mismatches,omitempty"`
	RemoteOnly    []string                `json:"remote_only_models,omitempty"`
}

type TokenRow struct {
	RowNumber int    `json:"row"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Group     string `json:"group"`
	Key       string `json:"-"`
	Models    string `json:"models"`
}

type GroupKeyReport struct {
	ProviderCode string                `json:"provider_code"`
	BaseURL      string                `json:"base_url"`
	ValidRows    int                   `json:"valid_rows"`
	InvalidRows  []GroupKeyReportError `json:"invalid_rows"`
	Groups       []GroupKeyReportRow   `json:"groups"`
}

type GroupKeyReportRow struct {
	Row     int    `json:"row"`
	Group   string `json:"group"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Models  string `json:"models"`
	KeyMask string `json:"key_mask"`
}

type GroupKeyReportError struct {
	Row     int      `json:"row"`
	Name    string   `json:"name"`
	Group   string   `json:"group"`
	KeyMask string   `json:"key_mask"`
	Errors  []string `json:"errors"`
}
