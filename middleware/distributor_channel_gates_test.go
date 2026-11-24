package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDistributeAdminHonorsChannelAvailability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, i18n.Init())
	db, err := gorm.Open(sqlite.Open("file:distributor-channel-gates?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	previousDB, previousCacheEnabled := model.DB, common.MemoryCacheEnabled
	model.DB, common.MemoryCacheEnabled = db, true
	t.Cleanup(func() {
		model.DB, common.MemoryCacheEnabled = previousDB, previousCacheEnabled
		if previousDB != nil {
			model.InitChannelCache()
		}
	})

	const group = "channel-gates-test"
	const modelName = "gpt-6.1-sol"
	const preferredID, fallbackID = 6971, 6972
	channels := []model.Channel{
		{Id: preferredID, Name: "preferred", Group: group, Models: modelName, Status: common.ChannelStatusEnabled, Key: "test", Priority: common.GetPointer(int64(10))},
		{Id: fallbackID, Name: "fallback", Group: group, Models: modelName, Status: common.ChannelStatusEnabled, Key: "test", Priority: common.GetPointer(int64(10))},
	}
	require.NoError(t, db.Create(&channels).Error)
	for _, channel := range channels {
		require.NoError(t, channel.AddAbilities(db))
	}

	quotaBlock := fmt.Sprintf(`{"quota_source":{"spendable":false,"block":{"http_status":429,"code":"channel_daily_protected_budget_exhausted","retry_at":%d}}}`, time.Now().Add(time.Hour).Unix())
	for _, tc := range []struct {
		name           string
		status         int
		abilityEnabled bool
		otherInfo      string
		cooldown       bool
		pinStatus      int
	}{
		{name: "manual_disable", status: common.ChannelStatusManuallyDisabled, pinStatus: http.StatusForbidden},
		{name: "automatic_disable", status: common.ChannelStatusAutoDisabled, pinStatus: http.StatusForbidden},
		{name: "disabled_model", status: common.ChannelStatusEnabled, pinStatus: http.StatusForbidden},
		{name: "cooldown", status: common.ChannelStatusEnabled, abilityEnabled: true, cooldown: true, pinStatus: http.StatusServiceUnavailable},
		{name: "channel_quota", status: common.ChannelStatusAutoDisabled, otherInfo: quotaBlock, pinStatus: http.StatusTooManyRequests},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newRequest := func() (*gin.Context, *httptest.ResponseRecorder) {
				rec := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(rec)
				body := fmt.Sprintf(`{"model":%q,"prompt_cache_key":%q}`, modelName, t.Name())
				ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
				ctx.Request.Header.Set("Content-Type", "application/json")
				common.SetContextKey(ctx, constant.ContextKeyUsingGroup, group)
				setAdminAPIUnlimitedContext(ctx, common.RoleAdminUser)
				t.Cleanup(func() { common.CleanupBodyStorage(ctx) })
				return ctx, rec
			}
			updatePreferred := func(status int, modelEnabled bool, otherInfo string) {
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", preferredID).Updates(map[string]any{
					"status": status, "other_info": otherInfo,
				}).Error)
				require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", preferredID).Update("enabled", modelEnabled).Error)
				model.InitChannelCache()
			}

			// Establish affinity before adding a healthy channel of the same priority.
			require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", fallbackID).Update("enabled", false).Error)
			updatePreferred(common.ChannelStatusEnabled, true, "")
			warm, _ := newRequest()
			setAdminAPIUnlimitedContext(warm, common.RoleCommonUser)
			Distribute()(warm)
			require.Equal(t, preferredID, common.GetContextKeyInt(warm, constant.ContextKeyChannelId))
			affinity, _ := newRequest()
			id, found := service.GetPreferredChannelByAffinity(affinity, modelName, group)
			require.True(t, found)
			require.Equal(t, preferredID, id)
			t.Cleanup(func() { service.ClearChannelAffinityForRequest(affinity, "test cleanup") })
			healthyPin, _ := newRequest()
			common.SetContextKey(healthyPin, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(preferredID))
			Distribute()(healthyPin)
			require.False(t, healthyPin.IsAborted())
			require.Equal(t, preferredID, common.GetContextKeyInt(healthyPin, constant.ContextKeyChannelId))

			require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", fallbackID).Update("enabled", true).Error)
			updatePreferred(tc.status, tc.abilityEnabled, tc.otherInfo)
			if tc.cooldown {
				require.NoError(t, model.MarkChannelTemporarilyUnschedulable(preferredID, time.Minute, model.ChannelTemporaryUnschedulable{Reason: "rate_limit"}))
				t.Cleanup(func() { model.ClearChannelTemporarilyUnschedulable(preferredID) })
			}

			pinned, rec := newRequest()
			common.SetContextKey(pinned, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(preferredID))
			Distribute()(pinned)
			require.Equal(t, tc.pinStatus, rec.Code)
			require.True(t, pinned.IsAborted())
			require.Zero(t, common.GetContextKeyInt(pinned, constant.ContextKeyChannelId))

			followUp, _ := newRequest()
			Distribute()(followUp)
			require.False(t, followUp.IsAborted())
			require.Equal(t, fallbackID, common.GetContextKeyInt(followUp, constant.ContextKeyChannelId))
		})
	}
}
