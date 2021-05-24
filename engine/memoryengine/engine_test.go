package memoryengine_test

import (
	"testing"

	"github.com/jhchabran/tmp-genji-release/engine"
	"github.com/jhchabran/tmp-genji-release/engine/enginetest"
	"github.com/jhchabran/tmp-genji-release/engine/memoryengine"
)

func builder() (engine.Engine, func()) {
	ng := memoryengine.NewEngine()
	return ng, func() { ng.Close() }
}

func TestMemoryEngine(t *testing.T) {
	enginetest.TestSuite(t, builder)
}

func BenchmarkMemoryEngineStorePut(b *testing.B) {
	enginetest.BenchmarkStorePut(b, builder)
}

func BenchmarkMemoryEngineStoreScan(b *testing.B) {
	enginetest.BenchmarkStoreScan(b, builder)
}
