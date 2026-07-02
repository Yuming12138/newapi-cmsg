package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupQuotaDataNodeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	oldDB := DB
	oldCache := CacheQuotaData
	DB = db
	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()
	t.Cleanup(func() {
		DB = oldDB
		CacheQuotaDataLock.Lock()
		CacheQuotaData = oldCache
		CacheQuotaDataLock.Unlock()
	})

	require.NoError(t, db.AutoMigrate(&QuotaData{}))
	return db
}

func TestQuotaDataKeepsNodeDimensionInStorage(t *testing.T) {
	db := setupQuotaDataNodeTestDB(t)

	LogQuotaData(QuotaDataLogParams{UserID: 1, Username: "alice", ModelName: "gpt-test", Quota: 10, CreatedAt: 1782709417, TokenUsed: 3, UseGroup: "vip", TokenID: 10, ChannelID: 100, NodeName: "submit-a"})
	LogQuotaData(QuotaDataLogParams{UserID: 1, Username: "alice", ModelName: "gpt-test", Quota: 20, CreatedAt: 1782709500, TokenUsed: 5, UseGroup: "vip", TokenID: 10, ChannelID: 100, NodeName: "submit-b"})
	LogQuotaData(QuotaDataLogParams{UserID: 1, Username: "alice", ModelName: "gpt-test", Quota: 7, CreatedAt: 1782709600, TokenUsed: 2, UseGroup: "default", TokenID: 11, ChannelID: 101, NodeName: "submit-a"})
	SaveQuotaDataCache()

	var rows []QuotaData
	require.NoError(t, db.Order("node_name asc, use_group asc, token_id asc, channel_id asc").Find(&rows).Error)
	require.Len(t, rows, 3)

	require.Equal(t, "submit-a", rows[0].NodeName)
	require.Equal(t, "default", rows[0].UseGroup)
	require.EqualValues(t, 11, rows[0].TokenID)
	require.EqualValues(t, 101, rows[0].ChannelID)
	require.EqualValues(t, 1, rows[0].Count)
	require.EqualValues(t, 7, rows[0].Quota)
	require.EqualValues(t, 2, rows[0].TokenUsed)
	require.EqualValues(t, 1782709200, rows[0].CreatedAt)

	require.Equal(t, "submit-a", rows[1].NodeName)
	require.Equal(t, "vip", rows[1].UseGroup)
	require.EqualValues(t, 10, rows[1].TokenID)
	require.EqualValues(t, 100, rows[1].ChannelID)
	require.EqualValues(t, 1, rows[1].Count)
	require.EqualValues(t, 10, rows[1].Quota)
	require.EqualValues(t, 3, rows[1].TokenUsed)

	require.Equal(t, "submit-b", rows[2].NodeName)
	require.Equal(t, "vip", rows[2].UseGroup)
	require.EqualValues(t, 10, rows[2].TokenID)
	require.EqualValues(t, 100, rows[2].ChannelID)
	require.EqualValues(t, 1, rows[2].Count)
	require.EqualValues(t, 20, rows[2].Quota)
	require.EqualValues(t, 5, rows[2].TokenUsed)
}

func TestQuotaDataUserQueriesAggregateAcrossNodes(t *testing.T) {
	setupQuotaDataNodeTestDB(t)

	require.NoError(t, DB.Create(&[]QuotaData{
		{UserID: 1, Username: "alice", ModelName: "gpt-test", CreatedAt: 1782709200, Count: 2, Quota: 17, TokenUsed: 5, NodeName: "submit-a"},
		{UserID: 1, Username: "alice", ModelName: "gpt-test", CreatedAt: 1782709200, Count: 1, Quota: 20, TokenUsed: 5, NodeName: "submit-b"},
		{UserID: 1, Username: "alice", ModelName: "other-model", CreatedAt: 1782709200, Count: 1, Quota: 3, TokenUsed: 1, NodeName: "submit-b"},
	}).Error)

	byUserID, err := GetQuotaDataByUserId(1, 1782709000, 1782710000)
	require.NoError(t, err)
	require.Len(t, byUserID, 2)

	var gptRow *QuotaData
	for _, row := range byUserID {
		if row.ModelName == "gpt-test" {
			gptRow = row
		}
	}
	require.NotNil(t, gptRow)
	require.EqualValues(t, 3, gptRow.Count)
	require.EqualValues(t, 37, gptRow.Quota)
	require.EqualValues(t, 10, gptRow.TokenUsed)
	require.Empty(t, gptRow.NodeName)

	byUsername, err := GetQuotaDataByUsername("alice", 1782709000, 1782710000)
	require.NoError(t, err)
	require.Len(t, byUsername, 2)
}
