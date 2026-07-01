package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
