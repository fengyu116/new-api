package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

// 本文件为二开新增：消费日志的 auto 路由标记。

// appendAutoRouteOther 在 auto 分组路由成功时给日志 Other 加 auto_route 标记，
// 便于前端区分「auto 路由命中」与「直接指定分组」。
func appendAutoRouteOther(c *gin.Context, other map[string]interface{}) map[string]interface{} {
	if c == nil {
		return other
	}
	if _, exists := common.GetContextKey(c, constant.ContextKeyAutoGroup); !exists {
		return other
	}
	if other == nil {
		other = make(map[string]interface{})
	}
	other["auto_route"] = true
	return other
}
