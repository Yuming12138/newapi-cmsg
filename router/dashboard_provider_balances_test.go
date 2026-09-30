package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDashboardProviderBalancesAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalDB, originalRedis := model.DB, common.RedisEnabled
	common.RedisEnabled = false
	db, err := gorm.Open(sqlite.Open("file:dashboard-balances-access?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	t.Cleanup(func() {
		model.DB, common.RedisEnabled = originalDB, originalRedis
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	t.Setenv("ASXS_ACCOUNT_TOKEN_FILE", filepath.Join(t.TempDir(), "missing-token"))
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}))
	commonToken, adminToken := "common-access-token", "admin-access-token"
	users := []model.User{
		{Id: 1, Username: "balance-reader", AffCode: "reader", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AccessToken: &commonToken},
		{Id: 2, Username: "balance-admin", AffCode: "admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AccessToken: &adminToken},
	}
	require.NoError(t, db.Create(&users).Error)
	kimiURL := "https://api.moonshot.cn"
	require.NoError(t, db.Create(&model.Channel{
		Id: 33, Type: constant.ChannelTypeOpenAI, Key: "secret-upstream-key",
		BaseURL: &kimiURL, Group: "kimi", Status: common.ChannelStatusEnabled,
		Balance: 24.98 / ratio_setting.USD2RMB, BalanceUpdatedTime: 200,
	}).Error)
	engine := gin.New()
	engine.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-session-secret"))))
	SetApiRouter(engine)

	for _, user := range users {
		t.Run(fmt.Sprintf("role_%d", user.Role), func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/dashboard/provider-balances", nil)
			request.Header.Set("Authorization", "Bearer "+user.GetAccessToken())
			request.Header.Set("New-Api-User", strconv.Itoa(user.Id))
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code)
			var payload struct {
				Success bool                              `json:"success"`
				Data    service.DashboardProviderBalances `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
			require.True(t, payload.Success, response.Body.String())
			require.Equal(t, "USD", payload.Data.ASXS.Currency)
			require.Equal(t, "CNY", payload.Data.Kimi.Currency)
			require.NotNil(t, payload.Data.Kimi.Balance)
			require.InDelta(t, 24.98, *payload.Data.Kimi.Balance, 0.001)
			require.Equal(t, "CNY", payload.Data.DeepSeek.Currency)
			require.NotContains(t, response.Body.String(), "secret-upstream-key")
			require.NotContains(t, response.Body.String(), "api.moonshot.cn")
		})
	}

	t.Run("anonymous_rejected", func(t *testing.T) {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/dashboard/provider-balances", nil))
		require.Equal(t, http.StatusUnauthorized, response.Code)
		require.NotContains(t, response.Body.String(), "secret-upstream-key")
	})

	t.Run("common_user_cannot_manage_channels", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/api/channel/", nil)
		request.Header.Set("Authorization", "Bearer "+commonToken)
		request.Header.Set("New-Api-User", "1")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		var payload struct {
			Success bool `json:"success"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
		require.False(t, payload.Success)
	})
}
