package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/utils/tests"
)

// lockForUpdate must emit FOR UPDATE on databases that support row locking and
// skip it on SQLite, where the syntax is invalid.
//
// The dummy dialector is intentional: SQLite's GORM dialector strips locking
// clauses while building SQL, which would hide whether this helper added one.
func TestLockForUpdateEmitsSupportedDialectClause(t *testing.T) {
	dummyDB, err := gorm.Open(tests.DummyDialector{}, &gorm.Config{DryRun: true})
	require.NoError(t, err)

	buildSQL := func() string {
		var rows []Redemption
		return lockForUpdate(dummyDB).Where("id = ?", 1).Find(&rows).Statement.SQL.String()
	}

	previousSQLite := common.UsingSQLite
	previousMySQL := common.UsingMySQL
	previousPostgreSQL := common.UsingPostgreSQL
	t.Cleanup(func() {
		common.UsingSQLite = previousSQLite
		common.UsingMySQL = previousMySQL
		common.UsingPostgreSQL = previousPostgreSQL
	})

	common.UsingSQLite = false
	common.UsingMySQL = true
	common.UsingPostgreSQL = false
	assert.Contains(t, buildSQL(), "FOR UPDATE")

	common.UsingMySQL = false
	common.UsingPostgreSQL = true
	assert.Contains(t, buildSQL(), "FOR UPDATE")

	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	assert.NotContains(t, buildSQL(), "FOR UPDATE")
}

func TestLockForUpdateHandlesNilTransaction(t *testing.T) {
	assert.Nil(t, lockForUpdate(nil))
}
