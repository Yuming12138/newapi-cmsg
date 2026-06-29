package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetChannelQuotaDatesAggregatesConsumeLogs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	oldDB := DB
	oldLogDB := LOG_DB
	DB = db
	LOG_DB = db
	t.Cleanup(func() {
		DB = oldDB
		LOG_DB = oldLogDB
	})

	require.NoError(t, db.AutoMigrate(&Log{}, &Channel{}))
	require.NoError(t, db.Create(&Channel{
		Id:    1,
		Name:  "asxs-cgm-1.2",
		Group: "asxs",
	}).Error)
	require.NoError(t, db.Create(&Channel{
		Id:    12,
		Name:  "cliproxy-codex-pool",
		Group: "cliproxy-codex",
	}).Error)

	require.NoError(t, db.Create(&[]Log{
		{
			CreatedAt:        1782709417,
			Type:             LogTypeConsume,
			Username:         "alice",
			ChannelId:        1,
			Quota:            100,
			PromptTokens:     10,
			CompletionTokens: 5,
		},
		{
			CreatedAt:        1782709500,
			Type:             LogTypeConsume,
			Username:         "alice",
			ChannelId:        1,
			Quota:            50,
			PromptTokens:     4,
			CompletionTokens: 1,
		},
		{
			CreatedAt:        1782713100,
			Type:             LogTypeConsume,
			Username:         "bob",
			ChannelId:        12,
			Quota:            30,
			PromptTokens:     2,
			CompletionTokens: 3,
		},
		{
			CreatedAt: 1782713100,
			Type:      LogTypeError,
			ChannelId: 12,
			Quota:     999,
		},
	}).Error)

	rows, err := GetChannelQuotaDates(1782709000, 1782714000, "")
	require.NoError(t, err)
	require.Len(t, rows, 2)

	require.Equal(t, 1, rows[0].ChannelID)
	require.Equal(t, "asxs-cgm-1.2", rows[0].ChannelName)
	require.Equal(t, "asxs", rows[0].Group)
	require.EqualValues(t, 1782709200, rows[0].CreatedAt)
	require.EqualValues(t, 2, rows[0].Count)
	require.EqualValues(t, 150, rows[0].Quota)
	require.EqualValues(t, 20, rows[0].TokenUsed)

	require.Equal(t, 12, rows[1].ChannelID)
	require.Equal(t, "cliproxy-codex-pool", rows[1].ChannelName)
	require.Equal(t, "cliproxy-codex", rows[1].Group)
	require.EqualValues(t, 1, rows[1].Count)
	require.EqualValues(t, 30, rows[1].Quota)
	require.EqualValues(t, 5, rows[1].TokenUsed)

	rows, err = GetChannelQuotaDates(1782709000, 1782714000, "alice")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 1, rows[0].ChannelID)
	require.EqualValues(t, 150, rows[0].Quota)
}
