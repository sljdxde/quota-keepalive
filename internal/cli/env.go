package cli

import (
	"context"
	"io"
	"os"
)

// Env 持有一次命令执行的全部外部依赖，便于测试注入。
type Env struct {
	Args   []string
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
}

// DefaultEnv 从真实进程构造 Env。
func DefaultEnv() *Env {
	return &Env{
		Args:   os.Args[1:],
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Getenv: os.Getenv,
	}
}

// Handler 是一个子命令的实现。返回的 Response 决定输出与退出码。
type Handler func(ctx context.Context, env *Env, args []string) (*Response, error)
