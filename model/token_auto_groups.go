package model

import "github.com/QuantumNous/new-api/common"

// 本文件为二开新增：令牌级自定义 auto 候选分组顺序的解析。

// GetAutoGroups 解析令牌的自定义 auto 候选分组顺序。
// 未配置或解析失败时返回 nil（按未配置处理）。
func (token *Token) GetAutoGroups() []string {
	if token.AutoGroups == "" {
		return nil
	}
	var groups []string
	if err := common.UnmarshalJsonStr(token.AutoGroups, &groups); err != nil {
		return nil
	}
	return groups
}
