package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetUserAccountGroupsUsesTopupGroupsAndCurrentGroup(t *testing.T) {
	original := common.TopupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(original))
	})
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1,"vip":0.8}`))

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/groups", GetUserAccountGroups)

	req := httptest.NewRequest(http.MethodGet, "/groups?current_group=legacy", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success bool     `json:"success"`
		Data    []string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.Equal(t, []string{"default", "legacy", "vip"}, payload.Data)
}
