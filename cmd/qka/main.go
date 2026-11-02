package main

import (
	"context"
	"os"

	"qka/internal/cli"
)

var (
	runCLI      = cli.Run
	defaultEnv  = cli.DefaultEnv
	exitProcess = os.Exit
)

func main() {
	exitProcess(run(context.Background()))
}

func run(ctx context.Context) int {
	return runCLI(ctx, defaultEnv())
}
