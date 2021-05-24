package query

import (
	"github.com/jhchabran/tmp-genji-release/document"
	"github.com/jhchabran/tmp-genji-release/internal/database"
	"github.com/jhchabran/tmp-genji-release/internal/expr"
	"github.com/jhchabran/tmp-genji-release/internal/planner"
	"github.com/jhchabran/tmp-genji-release/internal/stream"
)

// StreamStmt is a StreamStmt using a Stream.
type StreamStmt struct {
	Stream   *stream.Stream
	ReadOnly bool
}

// Run returns a result containing the stream. The stream will be executed by calling the Iterate method of
// the result.
func (s *StreamStmt) Run(tx *database.Transaction, params []expr.Param) (Result, error) {
	st, err := planner.Optimize(s.Stream.Clone(), tx, params)
	if err != nil || st == nil {
		return Result{}, err
	}

	return Result{
		Iterator: &StreamStmtIterator{
			Stream: st,
			Tx:     tx,
			Params: params,
		},
	}, nil
}

// IsReadOnly reports whether the stream will modify the database or only read it.
func (s *StreamStmt) IsReadOnly() bool {
	return s.ReadOnly
}

func (s *StreamStmt) String() string {
	return s.Stream.String()
}

// StreamStmtIterator iterates over a stream.
type StreamStmtIterator struct {
	Stream *stream.Stream
	Tx     *database.Transaction
	Params []expr.Param
}

func (s *StreamStmtIterator) Iterate(fn func(d document.Document) error) error {
	env := expr.Environment{
		Tx:     s.Tx,
		Params: s.Params,
	}

	err := s.Stream.Iterate(&env, func(env *expr.Environment) error {
		// if there is no doc in this specific environment,
		// the last operator is not outputting anything
		// worth returning to the user.
		if env.Doc == nil {
			return nil
		}

		return fn(env.Doc)
	})
	if err == stream.ErrStreamClosed {
		err = nil
	}
	return err
}
