package main

import (
	"context"
	"testing"

	"qka/internal/cli"
)

func TestRunUsesDefaultEnvironment(t *testing.T) {
	oldRun, oldEnv := runCLI, defaultEnv
	t.Cleanup(func() {
		runCLI, defaultEnv = oldRun, oldEnv
	})

	wantCtx := context.Background()
	wantEnv := &cli.Env{}
	runCLI = func(ctx context.Context, env *cli.Env) int {
		if ctx != wantCtx {
			t.Fatal("上下文应原样传递")
		}
		if env != wantEnv {
			t.Fatalf("应使用默认环境，实际 %#v", env)
		}
		return 17
	}
	defaultEnv = func() *cli.Env {
		return wantEnv
	}
	if got := run(wantCtx); got != 17 {
		t.Fatalf("应返回 CLI 退出码 17，实际 %d", got)
	}
}

func TestMainDelegatesExitCode(t *testing.T) {
	oldRunCLI, oldEnv, oldExit := runCLI, defaultEnv, exitProcess
	t.Cleanup(func() {
		runCLI, defaultEnv, exitProcess = oldRunCLI, oldEnv, oldExit
	})

	runCLI = func(context.Context, *cli.Env) int { return 23 }
	defaultEnv = func() *cli.Env { return &cli.Env{} }
	var got int
	exitProcess = func(code int) { got = code }
	main()
	if got != 23 {
		t.Fatalf("main 应传递退出码，实际 %d", got)
	}
}
