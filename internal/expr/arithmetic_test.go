package expr_test

import (
	"path/filepath"
	"testing"

	"github.com/jhchabran/tmp-genji-release/internal/testutil"
)

func TestArithmetic(t *testing.T) {
	testutil.ExprRunner(t, filepath.Join("testdata", "arithmetic.sql"))
}
