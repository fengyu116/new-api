package model

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAppendAutoRouteOther(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// nil ctx 不 panic、不修改
	require.Nil(t, appendAutoRouteOther(nil, nil))

	// 未走 auto 路由：不加标记
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	other := map[string]interface{}{"frt": 1.2}
	result := appendAutoRouteOther(ctx, other)
	require.NotContains(t, result, "auto_route")
	require.Equal(t, 1.2, result["frt"])

	// auto 路由命中：加标记，nil map 自动创建
	ctx2, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx2, constant.ContextKeyAutoGroup, "vip")
	result = appendAutoRouteOther(ctx2, nil)
	require.Equal(t, true, result["auto_route"])

	// 已有 map 时保留原内容
	result = appendAutoRouteOther(ctx2, map[string]interface{}{"group_ratio": 0.5})
	require.Equal(t, true, result["auto_route"])
	require.Equal(t, 0.5, result["group_ratio"])
}
