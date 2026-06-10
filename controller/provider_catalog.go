package controller

import (
	"fmt"
	"io"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/catalogimport"

	"github.com/gin-gonic/gin"
)

const providerCatalogApplyConfirm = "APPLY_PROVIDER_CATALOG"

func ProviderCatalogPreview(c *gin.Context) {
	req, err := buildProviderCatalogImportRequest(c, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	report, err := catalogimport.DryRun(req)
	if err != nil && len(report.InvalidRows) == 0 {
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
	ruleContent, err := readMultipartFile(c, "rule_file")
	if err != nil {
		return catalogimport.ImportRequest{}, err
	}
	catalog, err := catalogimport.ParseCatalog(catalogimport.ParseRequest{
		ProviderCode: providerCode,
		ProviderName: providerName,
		RuleType:     ruleType,
		BaseURL:      baseURL,
		Content:      ruleContent,
	})
	if err != nil {
		return catalogimport.ImportRequest{}, err
	}
	var tokenRows []catalogimport.TokenRow
	if keyContent, err := readOptionalMultipartFile(c, "key_file"); err == nil && len(keyContent) > 0 {
		tokenRows, err = catalogimport.ParseTokenRowsFromXLSX(keyContent)
		if err != nil {
			return catalogimport.ImportRequest{}, err
		}
	}
	return catalogimport.ImportRequest{Catalog: catalog, GroupKeys: tokenRows, Apply: apply}, nil
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
