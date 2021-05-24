// +build !wasm

package genji

import (
	"context"

	"github.com/jhchabran/tmp-genji-release/document/encoding/msgpack"
	"github.com/jhchabran/tmp-genji-release/engine"
	"github.com/jhchabran/tmp-genji-release/internal/database"
)

// New initializes the DB using the given engine.
func New(ctx context.Context, ng engine.Engine) (*DB, error) {
	return newDatabase(ctx, ng, database.Options{Codec: msgpack.NewCodec()})
}
