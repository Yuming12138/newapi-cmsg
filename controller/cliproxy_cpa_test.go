package controller

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestCliproxyCPAManagementKeyUsesEnvFirst(t *testing.T) {
	t.Setenv("CLIPROXY_CPA_MANAGEMENT_KEY", " env-key ")
	t.Setenv("CLIPROXY_CPA_MANAGEMENT_KEY_FILE", "")

	key, err := cliproxyCPAManagementKey()

	require.NoError(t, err)
	require.Equal(t, "env-key", key)
}

func TestCliproxyCPAManagementKeyReadsFile(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "management-key")
	require.NoError(t, os.WriteFile(keyPath, []byte(" file-key \n"), 0o600))
	t.Setenv("CLIPROXY_CPA_MANAGEMENT_KEY", "")
	t.Setenv("CLIPROXY_CPA_MANAGEMENT_KEY_FILE", keyPath)

	key, err := cliproxyCPAManagementKey()

	require.NoError(t, err)
	require.Equal(t, "file-key", key)
}

func TestChannelHasCliproxyCPAGuard(t *testing.T) {
	require.True(t, channelHasCliproxyCPAGuard(&model.Channel{
		OtherInfo: `{"cliproxy_cpa_quota_guard":{"managed":true}}`,
	}))
	require.False(t, channelHasCliproxyCPAGuard(&model.Channel{
		OtherInfo: `{"other_guard":{"managed":true}}`,
	}))
	require.False(t, channelHasCliproxyCPAGuard(nil))
}

func TestCliproxyCPAManagementURL(t *testing.T) {
	t.Setenv("CLIPROXY_CPA_BASE_URL", "http://cliproxy-api:8317/")

	endpoint, err := cliproxyCPAManagementURL("/v0/management/dispatch-audits")

	require.NoError(t, err)
	require.Equal(t, "http://cliproxy-api:8317/v0/management/dispatch-audits", endpoint.String())
}
