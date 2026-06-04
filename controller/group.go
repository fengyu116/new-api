package controller

import (
	"net/http"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func GetGroups(c *gin.Context) {
	groupNames := make([]string, 0)
	for groupName := range ratio_setting.GetGroupRatioCopy() {
		groupNames = append(groupNames, groupName)
	}
	sort.Strings(groupNames)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    groupNames,
	})
}

func GetUserAccountGroups(c *gin.Context) {
	groupSet := make(map[string]struct{})
	for groupName := range common.GetTopupGroupRatioCopy() {
		groupSet[groupName] = struct{}{}
	}
	if currentGroup := c.Query("current_group"); currentGroup != "" {
		groupSet[currentGroup] = struct{}{}
	}
	groupNames := make([]string, 0, len(groupSet))
	for groupName := range groupSet {
		groupNames = append(groupNames, groupName)
	}
	sort.Strings(groupNames)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    groupNames,
	})
}

func GetUserGroups(c *gin.Context) {
	usableGroups := make(map[string]map[string]interface{})
	userGroup := ""
	userId := c.GetInt("id")
	userGroup, _ = model.GetUserGroup(userId, false)
	userUsableGroups := service.GetUserUsableGroups(userGroup)
	for groupName, _ := range ratio_setting.GetGroupRatioCopy() {
		// UserUsableGroups contains the groups that the user can use
		if desc, ok := userUsableGroups[groupName]; ok {
			usableGroups[groupName] = map[string]interface{}{
				"ratio": service.GetUserGroupRatio(userGroup, groupName),
				"desc":  desc,
			}
		}
	}
	if len(setting.GetAutoGroups()) > 0 {
		usableGroups["auto"] = map[string]interface{}{
			"ratio": "自动",
			"desc":  "按自动分组链路选择可用渠道，实际扣费按命中的分组倍率计算",
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    usableGroups,
	})
}
