package parser

import (
	"github.com/genjidb/genji/internal/expr"
	"github.com/genjidb/genji/sql/scanner"
)

func (p *Parser) parseOrderBy() (expr.Path, scanner.Token, error) {
	// parse ORDER token
	ok, err := p.parseOptional(scanner.ORDER, scanner.BY)
	if err != nil || !ok {
		return nil, 0, err
	}

	// parse path
	path, err := p.parsePath()
	if err != nil {
		return nil, 0, err
	}

	// parse optional ASC or DESC
	if tok, _, _ := p.ScanIgnoreWhitespace(); tok == scanner.ASC || tok == scanner.DESC {
		return expr.Path(path), tok, nil
	}
	p.Unscan()

	return expr.Path(path), 0, nil
}

func (p *Parser) parseLimit() (expr.Expr, error) {
	// parse LIMIT token
	if tok, _, _ := p.ScanIgnoreWhitespace(); tok != scanner.LIMIT {
		p.Unscan()
		return nil, nil
	}

	e, err := p.ParseExpr()
	return e, err
}

func (p *Parser) parseOffset() (expr.Expr, error) {
	// parse OFFSET token
	if tok, _, _ := p.ScanIgnoreWhitespace(); tok != scanner.OFFSET {
		p.Unscan()
		return nil, nil
	}

	e, err := p.ParseExpr()
	return e, err
}
