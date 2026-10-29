package main

import (
	"context"
	"os"

	"qka/internal/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), cli.DefaultEnv()))
}
