module github.com/jhchabran/tmp-genji-release/cmd/genji

go 1.16

require (
	github.com/agnivade/levenshtein v1.1.0
	github.com/c-bata/go-prompt v0.2.5
	github.com/dgraph-io/badger/v3 v3.2011.1
	github.com/jhchabran/tmp-genji-release v0.12.0
	github.com/jhchabran/tmp-genji-release/engine/badgerengine v0.12.0
	github.com/stretchr/testify v1.7.0
	github.com/urfave/cli/v2 v2.3.0
	go.etcd.io/bbolt v1.3.5
	go.uber.org/multierr v1.6.0
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
)

replace (
	github.com/jhchabran/tmp-genji-release => ../../
	github.com/jhchabran/tmp-genji-release/engine/badgerengine => ../../engine/badgerengine/
)
