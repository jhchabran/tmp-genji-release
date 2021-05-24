module github.com/jhchabran/tmp-genji-release/engine/badgerengine

go 1.16

require (
	github.com/dgraph-io/badger/v3 v3.2011.1
	github.com/jhchabran/tmp-genji-release v0.2.0
	github.com/stretchr/testify v1.7.0
)

replace github.com/jhchabran/tmp-genji-release => ../../
