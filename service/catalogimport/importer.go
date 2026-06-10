package catalogimport

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/constant"
)

const (
	RuleTypeVectorNormal  = "vector_normal"
	RuleTypeVectorSpecial = "vector_special"
	RuleTypeShenggeNormal = "shengge_normal"
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
	ModelName              string   `json:"model_name"`
	Description            string   `json:"description"`
	Tags                   string   `json:"tags"`
	ModelType              string   `json:"model_type"`
	VendorID               int      `json:"vendor_id"`
	QuotaType              int      `json:"quota_type"`
	ModelRatio             float64  `json:"model_ratio"`
	ModelPrice             float64  `json:"model_price"`
	OwnerBy                string   `json:"owner_by"`
	CompletionRatio        float64  `json:"completion_ratio"`
	CacheRatio             *float64 `json:"cache_ratio"`
	CreateCacheRatio       *float64 `json:"create_cache_ratio"`
	AudioCompletionRatio   *float64 `json:"audio_completion_ratio"`
	EnableGroups           []string `json:"enable_groups"`
	SupportedEndpointTypes []string `json:"supported_endpoint_types"`
	SortOrder              int      `json:"sort_order"`
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
			EndpointMap:            endpointMap,
		}
		model.ChannelType = channelTypeForModel(model)
		catalog.Models = append(catalog.Models, model)
	}
	return catalog, nil
}

func parseVectorSpecial(req ParseRequest) (*ProviderCatalog, error) {
	var raw map[string]any
	if err := json.Unmarshal(req.Content, &raw); err != nil {
		return nil, fmt.Errorf("特殊规则必须使用标准 JSON，不能上传前端打包 JS: %w", err)
	}
	catalog := &ProviderCatalog{
		ProviderCode:   req.ProviderCode,
		ProviderName:   req.ProviderName,
		BaseURL:        strings.TrimRight(req.BaseURL, "/"),
		SpecialPricing: raw,
	}
	return catalog, nil
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
