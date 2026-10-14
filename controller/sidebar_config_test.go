package controller

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestDefaultSidebarExcludesImageWorkbench(t *testing.T) {
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		t.Run(strconv.Itoa(role), func(t *testing.T) {
			var config map[string]map[string]bool
			require.NoError(t, common.Unmarshal([]byte(generateDefaultSidebarConfig(role)), &config))
			require.Equal(t, map[string]bool{
				"enabled": true, "playground": true, "chat": true,
			}, config["chat"])
			require.True(t, config["console"]["token"])
		})
	}
}
