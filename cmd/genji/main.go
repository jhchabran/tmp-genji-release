package main

import (
	"fmt"
	"os"

	"github.com/jhchabran/tmp-genji-release/cmd/genji/commands"
)

func main() {
	app := commands.NewApp()

	err := app.Run(os.Args)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stdout, "error: %v\n", err)
		os.Exit(2)
	}
}
