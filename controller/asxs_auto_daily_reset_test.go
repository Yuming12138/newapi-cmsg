package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestASXSAutoDailyResetControlMissingIsUnconfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.json")
	t.Setenv("ASXS_AUTO_DAILY_RESET_CONTROL_PATH", path)

	control, configured, err := readASXSAutoDailyResetControl()

	require.NoError(t, err)
	require.False(t, configured)
	require.False(t, control.Enabled)
}

func TestASXSAutoDailyResetControlRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "control.json")
	t.Setenv("ASXS_AUTO_DAILY_RESET_CONTROL_PATH", path)
	want := asxsAutoDailyResetControl{
		SchemaVersion: 1,
		SiteID:        asxsAutoDailyResetControlSiteID,
		Enabled:       true,
		UpdatedAt:     time.Now().Unix(),
	}

	require.NoError(t, writeASXSAutoDailyResetControl(want))
	got, configured, err := readASXSAutoDailyResetControl()

	require.NoError(t, err)
	require.True(t, configured)
	require.Equal(t, want, got)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestASXSAutoDailyResetControlRejectsWrongSite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.json")
	t.Setenv("ASXS_AUTO_DAILY_RESET_CONTROL_PATH", path)
	require.NoError(t, os.WriteFile(path, []byte(`{"schema_version":1,"site_id":"campus","enabled":true,"updated_at":1}`), 0o600))

	_, configured, err := readASXSAutoDailyResetControl()

	require.Error(t, err)
	require.False(t, configured)
}

func TestUpdateASXSAutoDailyResetControlHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := filepath.Join(t.TempDir(), "control.json")
	t.Setenv("ASXS_AUTO_DAILY_RESET_CONTROL_PATH", path)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPut, "/api/option/asxs_auto_daily_reset", strings.NewReader(`{"enabled":true}`))
	context.Request.Header.Set("Content-Type", "application/json")

	UpdateASXSAutoDailyResetControl(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Configured bool `json:"configured"`
			Enabled    bool `json:"enabled"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.True(t, response.Data.Configured)
	require.True(t, response.Data.Enabled)
	control, configured, err := readASXSAutoDailyResetControl()
	require.NoError(t, err)
	require.True(t, configured)
	require.True(t, control.Enabled)
}
