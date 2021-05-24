package genji

import (
	"context"
	"strings"

	"github.com/jhchabran/tmp-genji-release/document"
	"github.com/jhchabran/tmp-genji-release/engine"
	errs "github.com/jhchabran/tmp-genji-release/errors"
	"github.com/jhchabran/tmp-genji-release/internal/database"
	"github.com/jhchabran/tmp-genji-release/internal/query"
	"github.com/jhchabran/tmp-genji-release/internal/sql/parser"
	"github.com/jhchabran/tmp-genji-release/internal/stream"
	"github.com/jhchabran/tmp-genji-release/internal/stringutil"
)

// DB represents a collection of tables stored in the underlying engine.
type DB struct {
	db  *database.Database
	ctx context.Context
}

// WithContext creates a new database handle using the given context for every operation.
func (db *DB) WithContext(ctx context.Context) *DB {
	return &DB{
		db:  db.db,
		ctx: ctx,
	}
}

// Close the database.
func (db *DB) Close() error {
	return db.db.Close()
}

// Begin starts a new transaction.
// The returned transaction must be closed either by calling Rollback or Commit.
func (db *DB) Begin(writable bool) (*Tx, error) {
	tx, err := db.db.BeginTx(db.ctx, &database.TxOptions{
		ReadOnly: !writable,
	})
	if err != nil {
		return nil, err
	}

	return &Tx{
		tx: tx,
	}, nil
}

// View starts a read only transaction, runs fn and automatically rolls it back.
func (db *DB) View(fn func(tx *Tx) error) error {
	tx, err := db.Begin(false)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	return fn(tx)
}

// Update starts a read-write transaction, runs fn and automatically commits it.
func (db *DB) Update(fn func(tx *Tx) error) error {
	tx, err := db.Begin(true)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	err = fn(tx)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// Query the database and return the result.
// The returned result must always be closed after usage.
func (db *DB) Query(q string, args ...interface{}) (*Result, error) {
	stmt, err := db.Prepare(q)
	if err != nil {
		return nil, err
	}

	return stmt.Query(args...)
}

// QueryDocument runs the query and returns the first document.
// If the query returns no error, QueryDocument returns errs.ErrDocumentNotFound.
func (db *DB) QueryDocument(q string, args ...interface{}) (document.Document, error) {
	stmt, err := db.Prepare(q)
	if err != nil {
		return nil, err
	}

	return stmt.QueryDocument(args...)
}

// Exec a query against the database without returning the result.
func (db *DB) Exec(q string, args ...interface{}) error {
	stmt, err := db.Prepare(q)
	if err != nil {
		return err
	}

	return stmt.Exec(args...)
}

// Prepare parses the query and returns a prepared statement.
func (db *DB) Prepare(q string) (*Statement, error) {
	pq, err := parser.ParseQuery(q)
	if err != nil {
		return nil, err
	}

	return &Statement{
		pq: pq,
		db: db,
	}, nil
}

// Tx represents a database transaction. It provides methods for managing the
// collection of tables and the transaction itself.
// Tx is either read-only or read/write. Read-only can be used to read tables
// and read/write can be used to read, create, delete and modify tables.
type Tx struct {
	tx *database.Transaction
}

// Rollback the transaction. Can be used safely after commit.
func (tx *Tx) Rollback() error {
	return tx.tx.Rollback()
}

// Commit the transaction. Calling this method on read-only transactions
// will return an error.
func (tx *Tx) Commit() error {
	return tx.tx.Commit()
}

// Query the database withing the transaction and returns the result.
// Closing the returned result after usage is not mandatory.
func (tx *Tx) Query(q string, args ...interface{}) (*Result, error) {
	stmt, err := tx.Prepare(q)
	if err != nil {
		return nil, err
	}

	return stmt.Query(args...)
}

// QueryDocument runs the query and returns the first document.
// If the query returns no error, QueryDocument returns errs.ErrDocumentNotFound.
func (tx *Tx) QueryDocument(q string, args ...interface{}) (document.Document, error) {
	stmt, err := tx.Prepare(q)
	if err != nil {
		return nil, err
	}

	return stmt.QueryDocument(args...)
}

// Exec a query against the database within tx and without returning the result.
func (tx *Tx) Exec(q string, args ...interface{}) (err error) {
	stmt, err := tx.Prepare(q)
	if err != nil {
		return err
	}

	return stmt.Exec(args...)
}

// Prepare parses the query and returns a prepared statement.
func (tx *Tx) Prepare(q string) (*Statement, error) {
	pq, err := parser.ParseQuery(q)
	if err != nil {
		return nil, err
	}

	return &Statement{
		pq: pq,
		tx: tx,
	}, nil
}

// Statement is a prepared statement. If Statement has been created on a Tx,
// it will only be valid until Tx closes. If it has been created on a DB, it
// is valid until the DB closes.
// It's safe for concurrent use by multiple goroutines.
type Statement struct {
	pq query.Query
	db *DB
	tx *Tx
}

// Query the database and return the result.
// The returned result must always be closed after usage.
func (s *Statement) Query(args ...interface{}) (*Result, error) {
	var r *query.Result
	var err error

	if s.tx != nil {
		r, err = s.pq.Exec(s.tx.tx, argsToParams(args))
	} else {
		r, err = s.pq.Run(s.db.ctx, s.db.db, argsToParams(args))
	}

	if err != nil {
		return nil, err
	}

	return &Result{result: r}, nil
}

// QueryDocument runs the query and returns the first document.
// If the query returns no error, QueryDocument returns errs.ErrDocumentNotFound.
func (s *Statement) QueryDocument(args ...interface{}) (d document.Document, err error) {
	res, err := s.Query(args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		er := res.Close()
		if err == nil {
			err = er
		}
	}()

	return scanDocument(res)
}

func scanDocument(res *Result) (document.Document, error) {
	var d document.Document
	err := res.Iterate(func(doc document.Document) error {
		d = doc
		return stream.ErrStreamClosed
	})
	if err != nil {
		return nil, err
	}

	if d == nil {
		return nil, errs.ErrDocumentNotFound
	}

	fb := document.NewFieldBuffer()
	err = fb.Copy(d)
	return fb, err
}

// Exec a query against the database without returning the result.
func (s *Statement) Exec(args ...interface{}) (err error) {
	res, err := s.Query(args...)
	if err != nil {
		return err
	}
	defer func() {
		er := res.Close()
		if err == nil {
			err = er
		}
	}()

	return res.Iterate(func(d document.Document) error {
		return nil
	})
}

// Result of a query.
type Result struct {
	result *query.Result
}

func (r *Result) Iterate(fn func(d document.Document) error) error {
	return r.result.Iterate(fn)
}

func (r *Result) Fields() []string {
	if r.result.Iterator == nil {
		return nil
	}

	stmt, ok := r.result.Iterator.(*query.StreamStmtIterator)
	if !ok || stmt.Stream.Op == nil {
		return nil
	}

	// Search for the ProjectOperator. If found, extract the projected expression list
	for op := stmt.Stream.First(); op != nil; op = op.GetNext() {
		if po, ok := op.(*stream.ProjectOperator); ok {
			// if there are no projected expression, it's a wildcard
			if len(po.Exprs) == 0 {
				break
			}

			fields := make([]string, len(po.Exprs))
			for i := range po.Exprs {
				fields[i] = stringutil.Sprintf("%s", po.Exprs[i])
			}

			return fields
		}
	}

	// the stream will output documents in a single field
	return []string{"*"}
}

// Close the result stream.
func (r *Result) Close() (err error) {
	if r == nil {
		return nil
	}

	return r.result.Close()
}

func newDatabase(ctx context.Context, ng engine.Engine, opts database.Options) (*DB, error) {
	db, err := database.New(ctx, ng, opts)
	if err != nil {
		return nil, err
	}

	tx, err := db.Begin(true)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	err = loadCatalog(tx)
	if err != nil {
		return nil, err
	}

	return &DB{
		db:  db,
		ctx: context.Background(),
	}, nil
}

func loadCatalog(tx *database.Transaction) error {
	tables, err := loadCatalogTables(tx)
	if err != nil {
		return err
	}

	indexes, err := loadCatalogIndexes(tx)
	if err != nil {
		return err
	}

	tx.Catalog.Load(tables, indexes)
	return nil
}

func loadCatalogTables(tx *database.Transaction) ([]database.TableInfo, error) {
	tb := database.GetTableStore(tx)

	var tables []database.TableInfo
	err := tb.AscendGreaterOrEqual(document.Value{}, func(d document.Document) error {
		s, err := d.GetByField("sql")
		if err != nil {
			return err
		}

		stmt, err := parser.NewParser(strings.NewReader(s.V.(string))).ParseStatement()
		if err != nil {
			return err
		}

		ti := stmt.(query.CreateTableStmt).Info

		v, err := d.GetByField("store_name")
		if err != nil {
			return err
		}
		ti.StoreName = v.V.([]byte)

		tables = append(tables, ti)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return tables, nil
}

func loadCatalogIndexes(tx *database.Transaction) ([]database.IndexInfo, error) {
	tb := database.GetIndexStore(tx)

	var indexes []database.IndexInfo
	err := tb.AscendGreaterOrEqual(document.Value{}, func(d document.Document) error {
		s, err := d.GetByField("sql")
		if err != nil {
			return err
		}

		stmt, err := parser.NewParser(strings.NewReader(s.V.(string))).ParseStatement()
		if err != nil {
			return err
		}

		indexes = append(indexes, stmt.(query.CreateIndexStmt).Info)
		return nil
	})

	return indexes, err
}
