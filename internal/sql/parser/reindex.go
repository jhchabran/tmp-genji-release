package parser

import (
	"github.com/jhchabran/tmp-genji-release/internal/query"
	"github.com/jhchabran/tmp-genji-release/internal/sql/scanner"
)

// parseReindexStatement parses a reindex statement.
// This function assumes the REINDEX token has already been consumed.
func (p *Parser) parseReIndexStatement() (query.Statement, error) {
	var stmt query.ReIndexStmt
	var err error

	tok, _, lit := p.ScanIgnoreWhitespace()
	if tok == scanner.IDENT {
		stmt.TableOrIndexName = lit
	} else {
		p.Unscan()
	}
	return stmt, err
}
