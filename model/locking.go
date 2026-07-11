package model

import (
	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// lockForUpdate makes the next query emit SELECT ... FOR UPDATE so the matched
// rows stay locked until the surrounding transaction ends.
//
// GORM v2 silently ignores the legacy `Set("gorm:query_option", "FOR UPDATE")`
// from GORM v1, so standard locking reads must use this helper instead.
//
// SQLite has no FOR UPDATE syntax. Its transactions and single-writer model
// provide the write serialization, while flows that can otherwise double-apply
// a state transition must also use a compare-and-swap update.
func lockForUpdate(tx *gorm.DB) *gorm.DB {
	if tx == nil {
		return tx
	}

	// Prefer the transaction's own dialector. Global database flags can remain
	// stale in tests, during initialization, or when a caller supplies a scoped
	// DB, and must not make a real MySQL/PostgreSQL transaction skip its lock.
	if tx.Dialector != nil {
		switch tx.Dialector.Name() {
		case common.DatabaseTypeSQLite:
			return tx
		case common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL:
			return tx.Clauses(clause.Locking{Strength: "UPDATE"})
		}
	}

	// Unknown/custom dialectors fall back to the configured main database type.
	// If neither source is recognizable, skip the clause rather than emitting
	// syntax that the database may not support.
	switch {
	case common.UsingSQLite:
		return tx
	case common.UsingMySQL || common.UsingPostgreSQL:
		return tx.Clauses(clause.Locking{Strength: "UPDATE"})
	default:
		return tx
	}
}
