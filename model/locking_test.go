package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/utils/tests"
)

type namedDialector struct {
	tests.DummyDialector
	name string
}

func (d namedDialector) Name() string {
	return d.name
}

// lockForUpdate must emit FOR UPDATE on databases that support row locking and
// skip it on SQLite, where the syntax is invalid.
//
// Named dummy dialectors are intentional: they exercise the helper's dialect
// selection without opening real database connections, while retaining a SQL
// builder that exposes whether the locking clause was added.
func TestLockForUpdateEmitsSupportedDialectClause(t *testing.T) {
	buildSQL := func(dialectName string) string {
		db, err := gorm.Open(namedDialector{name: dialectName}, &gorm.Config{DryRun: true})
		require.NoError(t, err)
		var rows []Redemption
		return lockForUpdate(db).Where("id = ?", 1).Find(&rows).Statement.SQL.String()
	}

	// TestMain leaves common.UsingSQLite=true. The recognized transaction
	// dialector must take precedence over that stale global value.
	assert.Contains(t, buildSQL(common.DatabaseTypeMySQL), "FOR UPDATE")
	assert.Contains(t, buildSQL(common.DatabaseTypePostgreSQL), "FOR UPDATE")
	assert.NotContains(t, buildSQL(common.DatabaseTypeSQLite), "FOR UPDATE")
}

func TestLockForUpdateHandlesNilTransaction(t *testing.T) {
	assert.Nil(t, lockForUpdate(nil))
}
