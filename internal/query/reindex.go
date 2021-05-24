package query

import (
	"errors"

	errs "github.com/jhchabran/tmp-genji-release/errors"
	"github.com/jhchabran/tmp-genji-release/internal/database"
	"github.com/jhchabran/tmp-genji-release/internal/expr"
)

// ReIndexStmt is a DSL that allows creating a full REINDEX statement.
type ReIndexStmt struct {
	TableOrIndexName string
}

// IsReadOnly always returns false. It implements the Statement interface.
func (stmt ReIndexStmt) IsReadOnly() bool {
	return false
}

// Run runs the Reindex statement in the given transaction.
// It implements the Statement interface.
func (stmt ReIndexStmt) Run(tx *database.Transaction, args []expr.Param) (Result, error) {
	var res Result

	if stmt.TableOrIndexName == "" {
		return res, tx.Catalog.ReIndexAll(tx)
	}

	_, err := tx.Catalog.GetTable(tx, stmt.TableOrIndexName)
	if err == nil {
		for _, idxName := range tx.Catalog.ListIndexes(stmt.TableOrIndexName) {
			err = tx.Catalog.ReIndex(tx, idxName)
			if err != nil {
				return res, err
			}
		}

		return res, nil
	}
	if !errors.Is(err, errs.ErrTableNotFound) {
		return res, err
	}

	err = tx.Catalog.ReIndex(tx, stmt.TableOrIndexName)
	return res, err
}
