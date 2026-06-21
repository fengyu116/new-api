package controller

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/catalogimport"

	"github.com/gin-gonic/gin"
)

const providerCatalogApplyConfirm = "APPLY_PROVIDER_CATALOG"

var fetchVectorRemotePricing = func(ctx context.Context, baseURL string) ([]byte, error) {
	return catalogimport.FetchVectorRemotePricing(ctx, service.GetHttpClient(), baseURL)
}

func ProviderCatalogPreview(c *gin.Context) {
	req, err := buildProviderCatalogImportRequest(c, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	report, err := catalogimport.DryRun(req)
	if err != nil && len(report.InvalidRows) == 0 && len(report.BlockedReasons) == 0 {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, report)
}

func ProviderCatalogBillingAudit(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	report, err := catalogimport.AuditTaskBilling(limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, report)
}

func ProviderCatalogApply(c *gin.Context) {
	if c.PostForm("confirm") != providerCatalogApplyConfirm {
		common.ApiError(c, fmt.Errorf("确认文本错误，需要填写 %s", providerCatalogApplyConfirm))
		return
	}
	req, err := buildProviderCatalogImportRequest(c, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	report, err := catalogimport.Apply(req)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	_ = catalogimport.SaveLastReport(report)
	common.ApiSuccess(c, report)
}

func buildProviderCatalogImportRequest(c *gin.Context, apply bool) (catalogimport.ImportRequest, error) {
	providerCode := strings.TrimSpace(c.PostForm("provider_code"))
	providerName := strings.TrimSpace(c.PostForm("provider_name"))
	ruleType := strings.TrimSpace(c.PostForm("rule_type"))
	baseURL := strings.TrimSpace(c.PostForm("base_url"))
	if providerCode == "" || ruleType == "" || baseURL == "" {
		return catalogimport.ImportRequest{}, fmt.Errorf("provider_code、rule_type、base_url 均不能为空")
	}
	if providerName == "" {
		providerName = providerCode
	}
	var catalog *catalogimport.ProviderCatalog
	var err error
	if providerCode == "vector" && hasMultipartFile(c, "normal_rule_file") && hasMultipartFile(c, "special_rule_file") {
		ruleType = catalogimport.RuleTypeVectorBundle
	}
	if ruleType == catalogimport.RuleTypeVectorBundle {
		normalContent, readErr := readMultipartFile(c, "normal_rule_file")
		if readErr != nil {
			return catalogimport.ImportRequest{}, readErr
		}
		specialContent, readErr := readMultipartFile(c, "special_rule_file")
		if readErr != nil {
			return catalogimport.ImportRequest{}, readErr
		}
		remotePricingContent, fetchErr := fetchVectorRemotePricing(c.Request.Context(), baseURL)
		if fetchErr != nil {
			return catalogimport.ImportRequest{}, fmt.Errorf("向量远端价格校验失败: %w", fetchErr)
		}
		catalog, err = catalogimport.ParseVectorBundle(catalogimport.VectorBundleRequest{
			ProviderCode:         providerCode,
			ProviderName:         providerName,
			BaseURL:              baseURL,
			NormalContent:        normalContent,
			SpecialContent:       specialContent,
			RemotePricingContent: remotePricingContent,
		})
	} else {
		ruleField := "rule_file"
		if providerCode == "vector" && ruleType == catalogimport.RuleTypeVectorNormal && hasMultipartFile(c, "normal_rule_file") {
			ruleField = "normal_rule_file"
		}
		if providerCode == "vector" && ruleType == catalogimport.RuleTypeVectorSpecial && hasMultipartFile(c, "special_rule_file") {
			ruleField = "special_rule_file"
		}
		ruleContent, readErr := readMultipartFile(c, ruleField)
		if readErr != nil {
			return catalogimport.ImportRequest{}, readErr
		}
		catalog, err = catalogimport.ParseCatalog(catalogimport.ParseRequest{
			ProviderCode: providerCode,
			ProviderName: providerName,
			RuleType:     ruleType,
			BaseURL:      baseURL,
			Content:      ruleContent,
		})
		if err == nil && providerCode == "vector" && ruleType == catalogimport.RuleTypeVectorNormal {
			remotePricingContent, fetchErr := fetchVectorRemotePricing(c.Request.Context(), baseURL)
			if fetchErr != nil {
				return catalogimport.ImportRequest{}, fmt.Errorf("向量远端价格校验失败: %w", fetchErr)
			}
			catalogimport.AttachVectorRemotePricingValidation(catalog, remotePricingContent)
		}
	}
	if err != nil {
		return catalogimport.ImportRequest{}, err
	}
	if providerCode == "vector" && ruleType == catalogimport.RuleTypeVectorNormal {
		for _, item := range catalog.Models {
			if item.QuotaType == 1 {
				return catalogimport.ImportRequest{}, fmt.Errorf("向量普通规则包含固定价模型，必须使用“向量完整目录”同时导入普通与特殊规则")
			}
		}
	}
	var tokenRows []catalogimport.TokenRow
	if keyContent, err := readOptionalMultipartFile(c, "key_file"); err == nil && len(keyContent) > 0 {
		tokenRows, err = catalogimport.ParseTokenRowsFromXLSX(keyContent)
		if err != nil {
			return catalogimport.ImportRequest{}, err
		}
	}
	return catalogimport.ImportRequest{
		Catalog:           catalog,
		GroupKeys:         tokenRows,
		GroupConflictMode: strings.TrimSpace(c.PostForm("group_conflict_mode")),
		Apply:             apply,
	}, nil
}

func hasMultipartFile(c *gin.Context, field string) bool {
	_, err := c.FormFile(field)
	return err == nil
}

func readMultipartFile(c *gin.Context, field string) ([]byte, error) {
	data, err := readOptionalMultipartFile(c, field)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("缺少上传文件: %s", field)
	}
	return data, nil
}

func readOptionalMultipartFile(c *gin.Context, field string) ([]byte, error) {
	file, err := c.FormFile(field)
	if err != nil {
		return nil, nil
	}
	f, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
