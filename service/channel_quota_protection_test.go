package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFindChannelQuotaProtectionBlock(t *testing.T) {
	originalDB := model.DB
	db, err := gorm.Open(sqlite.Open("file:channel-quota-protection?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	t.Cleanup(func() { model.DB = originalDB })

	retryAt := time.Now().Add(2 * time.Hour).Unix()
	otherInfo := fmt.Sprintf(`{
  "quota_source": {
    "spendable": false,
    "status": "quota_exhausted",
    "updated_at": 1800000000,
    "block": {
      "kind": "daily_protected_budget",
      "code": "channel_daily_protected_budget_exhausted",
      "reason": "dynamic_daily_budget_exhausted",
      "http_status": 429,
      "retry_at": %d,
      "retry_after_seconds": 7200,
      "timezone": "Asia/Shanghai"
    }
  }
}`, retryAt)
	priority := int64(0)
	channel := model.Channel{
		Id:        12,
		Name:      "cliproxy-codex-pool",
		Group:     "cliproxy-codex",
		Models:    "gpt-5.6-luna",
		Status:    common.ChannelStatusAutoDisabled,
		Key:       "test",
		Priority:  &priority,
		OtherInfo: otherInfo,
	}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "cliproxy-codex", Model: "gpt-5.6-luna", ChannelId: 12, Enabled: false, Priority: &priority,
	}).Error)

	block, err := FindChannelQuotaProtectionBlock(
		context.Background(),
		[]string{"cliproxy-codex"},
		"gpt-5.6-luna",
		"/v1/responses",
	)

	require.NoError(t, err)
	require.NotNil(t, block)
	require.Equal(t, 12, block.ChannelID)
	require.Equal(t, "channel_daily_protected_budget_exhausted", block.Code)
	require.Equal(t, retryAt, block.RetryAt)
	require.Equal(t, "Asia/Shanghai", block.Timezone)
	require.NotEmpty(t, block.RecoveryTime())

	unrelated, err := FindChannelQuotaProtectionBlock(
		context.Background(),
		[]string{"cliproxy-codex"},
		"gpt-5.6-sol",
		"/v1/responses",
	)
	require.NoError(t, err)
	require.Nil(t, unrelated)
}

func TestGetChannelQuotaProtectionBlockRejectsNonAutoDisabledChannel(t *testing.T) {
	channel := &model.Channel{
		Id:        12,
		Status:    common.ChannelStatusManuallyDisabled,
		OtherInfo: `{"quota_source":{"spendable":false,"block":{"http_status":429,"code":"channel_daily_protected_budget_exhausted","retry_at":1800000000}}}`,
	}

	require.Nil(t, GetChannelQuotaProtectionBlock(channel))
}
