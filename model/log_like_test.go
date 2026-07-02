package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBuildLogLikeConditionUsesStandardEscape(t *testing.T) {
	originalLogSqlType := common.LogSqlType
	originalUsingClickHouse := common.UsingClickHouse
	t.Cleanup(func() {
		common.LogSqlType = originalLogSqlType
		common.UsingClickHouse = originalUsingClickHouse
	})
	common.LogSqlType = common.DatabaseTypeSQLite
	common.UsingClickHouse = false

	condition, pattern, err := buildLogLikeCondition("logs.model_name", "gpt_4%")

	require.NoError(t, err)
	assert.Equal(t, "logs.model_name LIKE ? ESCAPE '!'", condition)
	assert.Equal(t, "gpt!_4%", pattern)
}

func TestBuildLogLikeConditionUsesClickHouseEscaping(t *testing.T) {
	originalLogSqlType := common.LogSqlType
	originalUsingClickHouse := common.UsingClickHouse
	t.Cleanup(func() {
		common.LogSqlType = originalLogSqlType
		common.UsingClickHouse = originalUsingClickHouse
	})
	common.LogSqlType = common.DatabaseTypeClickHouse
	common.UsingClickHouse = true

	condition, pattern, err := buildLogLikeCondition("logs.model_name", `gpt_4\mini%`)

	require.NoError(t, err)
	assert.Equal(t, "logs.model_name LIKE ?", condition)
	assert.Equal(t, `gpt\_4\\mini%`, pattern)
}

func TestApplyExplicitLogTextFilterUsesExactMatchByDefault(t *testing.T) {
	tx := DB.Session(&gorm.Session{DryRun: true}).Model(&Log{})

	tx, err := applyExplicitLogTextFilter(tx, "logs.model_name", "gpt-4")

	require.NoError(t, err)
	stmt := tx.Find(&[]Log{}).Statement
	assert.Contains(t, stmt.SQL.String(), "logs.model_name = ?")
	assert.Equal(t, "gpt-4", stmt.Vars[0])
}

func TestApplyExplicitLogTextFilterUsesLikeWhenWildcardIsExplicit(t *testing.T) {
	originalLogSqlType := common.LogSqlType
	originalUsingClickHouse := common.UsingClickHouse
	t.Cleanup(func() {
		common.LogSqlType = originalLogSqlType
		common.UsingClickHouse = originalUsingClickHouse
	})
	common.LogSqlType = common.DatabaseTypeSQLite
	common.UsingClickHouse = false

	tx := DB.Session(&gorm.Session{DryRun: true}).Model(&Log{})

	tx, err := applyExplicitLogTextFilter(tx, "logs.model_name", "gpt%")

	require.NoError(t, err)
	stmt := tx.Find(&[]Log{}).Statement
	assert.Contains(t, stmt.SQL.String(), "logs.model_name LIKE ? ESCAPE '!'")
	assert.Equal(t, "gpt%", stmt.Vars[0])
}

func TestDeleteOldLogKeepsBatchDeletionForStandardDatabases(t *testing.T) {
	originalLogSqlType := common.LogSqlType
	originalUsingClickHouse := common.UsingClickHouse
	t.Cleanup(func() {
		common.LogSqlType = originalLogSqlType
		common.UsingClickHouse = originalUsingClickHouse
		require.NoError(t, LOG_DB.Exec("DELETE FROM logs").Error)
	})
	common.LogSqlType = common.DatabaseTypeSQLite
	common.UsingClickHouse = false

	require.NoError(t, LOG_DB.Exec("DELETE FROM logs").Error)
	require.NoError(t, LOG_DB.Create(&[]Log{
		{CreatedAt: 100, Type: LogTypeConsume, ModelName: "old-a"},
		{CreatedAt: 150, Type: LogTypeConsume, ModelName: "old-b"},
		{CreatedAt: 199, Type: LogTypeConsume, ModelName: "old-c"},
		{CreatedAt: 200, Type: LogTypeConsume, ModelName: "boundary"},
		{CreatedAt: 300, Type: LogTypeConsume, ModelName: "new"},
	}).Error)

	deleted, err := DeleteOldLog(context.Background(), 200, 2)

	require.NoError(t, err)
	assert.EqualValues(t, 3, deleted)

	var remaining []Log
	require.NoError(t, LOG_DB.Order("created_at asc").Find(&remaining).Error)
	require.Len(t, remaining, 2)
	assert.EqualValues(t, 200, remaining[0].CreatedAt)
	assert.Equal(t, "boundary", remaining[0].ModelName)
	assert.EqualValues(t, 300, remaining[1].CreatedAt)
	assert.Equal(t, "new", remaining[1].ModelName)
}
