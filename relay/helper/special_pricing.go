package helper

import (
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/special_pricing"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func ApplySpecialModelPricing(c *gin.Context, info *relaycommon.RelayInfo, priceData *types.PriceData) (bool, error) {
	if info == nil || priceData == nil {
		return false, nil
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return false, nil
	}
	result, matched, err := special_pricing.ResolveTaskPricing(info.OriginModelName, req)
	if err != nil {
		return matched, err
	}
	if !matched {
		return false, nil
	}
	groupRatio := priceData.GroupRatioInfo.GroupRatio
	quota := int(math.Round(result.FinalPrice * common.QuotaPerUnit * groupRatio))
	if quota < 0 {
		return true, fmt.Errorf("model %s special pricing produced negative quota", info.OriginModelName)
	}
	priceData.ModelPrice = result.FinalPrice
	priceData.ModelRatio = 0
	priceData.UsePrice = true
	priceData.Quota = quota
	priceData.FreeModel = quota == 0
	priceData.OtherRatios = map[string]float64{
		"special_multiplier": result.Multiplier,
		"special_duration":   float64(result.Duration),
		"special_unit_price": result.UnitPrice,
	}
	for key, value := range result.Spec {
		if number, ok := value.(float64); ok {
			priceData.OtherRatios["special_"+key] = number
		}
	}
	info.PriceData = *priceData
	c.Set("special_pricing_match", result)
	return true, nil
}
