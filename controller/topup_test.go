package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestNormalizedTopupGroupRatio(t *testing.T) {
	original := common.TopupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(original))
	})

	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1,"vip":0.6,"invalid":0}`))

	require.Equal(t, 0.6, normalizedTopupGroupRatio("vip"))
	require.Equal(t, 1.0, normalizedTopupGroupRatio("missing"))
	require.Equal(t, 1.0, normalizedTopupGroupRatio("invalid"))
}
