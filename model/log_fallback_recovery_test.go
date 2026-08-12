package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttachFallbackRecoveriesAnnotatesOnlyEarlierErrors(t *testing.T) {
	requestIds := []string{
		"test-fallback-recovered",
		"test-fallback-failed",
		"test-fallback-error-after-success",
	}
	t.Cleanup(func() {
		require.NoError(t, LOG_DB.Where("request_id IN ?", requestIds).Delete(&Log{}).Error)
	})
	require.NoError(t, LOG_DB.Where("request_id IN ?", requestIds).Delete(&Log{}).Error)

	require.NoError(t, LOG_DB.Create(&[]Log{
		{CreatedAt: 100, Type: LogTypeError, RequestId: requestIds[0], ChannelId: 12},
		{CreatedAt: 160, Type: LogTypeConsume, RequestId: requestIds[0], ChannelId: 27},
		{CreatedAt: 200, Type: LogTypeError, RequestId: requestIds[1], ChannelId: 12},
		{CreatedAt: 300, Type: LogTypeConsume, RequestId: requestIds[2], ChannelId: 27},
		{CreatedAt: 320, Type: LogTypeError, RequestId: requestIds[2], ChannelId: 12},
	}).Error)

	logs := []*Log{
		{CreatedAt: 100, Type: LogTypeError, RequestId: requestIds[0], ChannelId: 12},
		{CreatedAt: 200, Type: LogTypeError, RequestId: requestIds[1], ChannelId: 12},
		{CreatedAt: 320, Type: LogTypeError, RequestId: requestIds[2], ChannelId: 12},
	}
	require.NoError(t, attachFallbackRecoveries(logs))

	assert.True(t, logs[0].FallbackRecovered)
	assert.Equal(t, 27, logs[0].RecoveredChannelId)
	assert.EqualValues(t, 160, logs[0].RecoveredAt)
	assert.False(t, logs[1].FallbackRecovered)
	assert.False(t, logs[2].FallbackRecovered)
}
