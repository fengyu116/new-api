package catalogimport

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
	Extra                  map[string]any    `json:"extra,omitempty"`
}

type ProviderCatalog struct {
	ProviderCode   string             `json:"provider_code"`
	ProviderName   string             `json:"provider_name"`
	BaseURL        string             `json:"base_url"`
	AutoGroups     []string           `json:"auto_groups,omitempty"`
	Vendors        []CatalogVendor    `json:"vendors,omitempty"`
	Groups         map[string]string  `json:"groups,omitempty"`
	GroupRatios    map[string]float64 `json:"group_ratios,omitempty"`
	Models         []CatalogModel     `json:"models,omitempty"`
	SpecialPricing map[string]any     `json:"special_pricing,omitempty"`
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
