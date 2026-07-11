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
	if tx == nil || common.UsingSQLite || (tx.Dialector != nil && tx.Dialector.Name() == common.DatabaseTypeSQLite) {
		return tx
	}
	return tx.Clauses(clause.Locking{Strength: "UPDATE"})
}
