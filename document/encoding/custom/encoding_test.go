package custom

import (
	"testing"

	"github.com/jhchabran/tmp-genji-release/document/encoding"
	"github.com/jhchabran/tmp-genji-release/document/encoding/encodingtest"
)

func TestCodec(t *testing.T) {
	encodingtest.TestCodec(t, func() encoding.Codec {
		return NewCodec()
	})
}

func BenchmarkCodec(b *testing.B) {
	encodingtest.BenchmarkCodec(b, func() encoding.Codec {
		return NewCodec()
	})
}
