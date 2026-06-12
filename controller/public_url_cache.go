package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func CreatePublicURLCache(c *gin.Context) {
	var req service.PublicURLCacheCreateRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的请求参数: " + err.Error(),
		})
		return
	}
	resp, err := service.CreatePublicURLCache(c.GetInt("id"), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"usage_id": resp.UsageID,
		"data":     resp.Data,
	})
}

func ReleasePublicURLCache(c *gin.Context) {
	var req service.PublicURLCacheReleaseRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的请求参数: " + err.Error(),
		})
		return
	}
	released, err := service.ReleasePublicURLCache(c.GetInt("id"), req.UsageID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"released": released,
	})
}

func ServePublicURLCacheAsset(c *gin.Context) {
	objectKey := c.Param("object_key")
	objectPath, mimeType, err := service.ResolvePublicURLCacheObjectPath(objectKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	c.Header("Cache-Control", "public, max-age=3600")
	c.Header("Content-Type", mimeType)
	c.File(objectPath)
}
