package helper

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
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
	return applySpecialPricingResult(c, info, priceData, result, matched, err)
}

func ApplySpecialModelPricingForRequest(c *gin.Context, info *relaycommon.RelayInfo, priceData *types.PriceData, request any) (bool, error) {
	if info == nil || priceData == nil || request == nil {
		return false, nil
	}
	req, ok := specialTaskReqFromRequest(request)
	if !ok {
		return false, nil
	}
	result, matched, err := special_pricing.ResolveTaskPricing(info.OriginModelName, req)
	return applySpecialPricingResult(c, info, priceData, result, matched, err)
}

func applySpecialPricingResult(c *gin.Context, info *relaycommon.RelayInfo, priceData *types.PriceData, result special_pricing.MatchResult, matched bool, err error) (bool, error) {
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
	priceData.QuotaToPreConsume = quota
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

func specialTaskReqFromRequest(request any) (relaycommon.TaskSubmitReq, bool) {
	switch req := request.(type) {
	case *dto.ImageRequest:
		return imageTaskReq(req), true
	case dto.ImageRequest:
		return imageTaskReq(&req), true
	case *dto.AudioRequest:
		return audioTaskReq(req), true
	case dto.AudioRequest:
		return audioTaskReq(&req), true
	default:
		return relaycommon.TaskSubmitReq{}, false
	}
}

func imageTaskReq(req *dto.ImageRequest) relaycommon.TaskSubmitReq {
	metadata := map[string]interface{}{}
	for key, value := range req.Extra {
		var decoded interface{}
		if len(value) > 0 && json.Unmarshal(value, &decoded) == nil {
			metadata[key] = decoded
		}
	}
	mergeRawObject(metadata, req.ExtraFields)
	if req.Quality != "" {
		metadata["quality"] = req.Quality
	}
	images := rawArrayStrings(req.Images)
	image := rawString(req.Image)
	if image != "" && len(images) == 0 {
		images = append(images, image)
	}
	return relaycommon.TaskSubmitReq{
		Prompt:   req.Prompt,
		Model:    req.Model,
		Image:    image,
		Images:   images,
		Size:     req.Size,
		Metadata: metadata,
	}
}

func audioTaskReq(req *dto.AudioRequest) relaycommon.TaskSubmitReq {
	metadata := map[string]interface{}{}
	mergeRawObject(metadata, req.Metadata)
	mergeRawValue(metadata, "task_type", req.TaskType)
	if req.Voice != "" {
		metadata["voice"] = req.Voice
	}
	return relaycommon.TaskSubmitReq{
		Prompt:   req.Input,
		Model:    req.Model,
		Metadata: metadata,
	}
}

func mergeRawObject(dst map[string]interface{}, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var decoded map[string]interface{}
	if json.Unmarshal(raw, &decoded) != nil {
		return
	}
	for key, value := range decoded {
		dst[key] = value
	}
}

func mergeRawValue(dst map[string]interface{}, key string, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var decoded interface{}
	if json.Unmarshal(raw, &decoded) == nil {
		dst[key] = decoded
	}
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return ""
}

func rawArrayStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var strings []string
	if json.Unmarshal(raw, &strings) == nil {
		return strings
	}
	var values []interface{}
	if json.Unmarshal(raw, &values) != nil {
		return nil
	}
	result := make([]string, 0, len(values))
	for range values {
		result = append(result, "image")
	}
	return result
}
