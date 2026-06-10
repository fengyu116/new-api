package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTokenGetAutoGroups(t *testing.T) {
	require.Nil(t, (&Token{}).GetAutoGroups())
	require.Nil(t, (&Token{AutoGroups: "not-json"}).GetAutoGroups())
	require.Equal(t, []string{"vip", "default"}, (&Token{AutoGroups: `["vip","default"]`}).GetAutoGroups())
}
