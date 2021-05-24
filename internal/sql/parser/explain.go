package parser

import (
	"github.com/jhchabran/tmp-genji-release/internal/query"
	"github.com/jhchabran/tmp-genji-release/internal/sql/scanner"
)

// parseExplainStatement parses any statement and returns an ExplainStmt object.
// This function assumes the EXPLAIN token has already been consumed.
func (p *Parser) parseExplainStatement() (query.Statement, error) {
	// ensure we don't have multiple EXPLAIN keywords
	tok, pos, lit := p.ScanIgnoreWhitespace()
	if tok != scanner.SELECT && tok != scanner.UPDATE && tok != scanner.DELETE && tok != scanner.INSERT {
		return nil, newParseError(scanner.Tokstr(tok, lit), []string{"INSERT", "SELECT", "UPDATE", "DELETE"}, pos)
	}
	p.Unscan()

	innerStmt, err := p.ParseStatement()
	if err != nil {
		return nil, err
	}

	return &query.ExplainStmt{Statement: innerStmt}, nil
}
