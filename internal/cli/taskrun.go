package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"qka/internal/app"
	"qka/internal/runlock"
	"qka/internal/schedule"
)

type taskRunRunner interface {
	RunAllActionSummary(context.Context, string) (app.RunSummary, error)
	Close() error
}

type taskRunRecorder interface {
	RecordTaskRun(context.Context, app.RunSummary) error
}

type taskRunDeps struct {
	LoadSchedule func(string) (schedule.Config, error)
	SaveLastRun  func(string, schedule.LastRun) error
	AcquireLock  func(string) (io.Closer, error)
	Build        func(context.Context, string) (taskRunRunner, error)
	Now          func() time.Time
}

func defaultTaskRunDeps(env *Env) taskRunDeps {
	if env == nil {
		env = DefaultEnv()
	}
	return taskRunDeps{
		LoadSchedule: schedule.Load,
		SaveLastRun:  schedule.RecordLastRun,
		AcquireLock: func(configDir string) (io.Closer, error) {
			return runlock.Acquire(configDir)
		},
		Build: func(ctx context.Context, configDir string) (taskRunRunner, error) {
			return (&commonFlags{}).buildAppAt(ctx, env, configDir)
		},
		Now: time.Now,
	}
}

type taskRunOutput struct {
	Status          string `json:"status"`
	AutoRunEnabled  bool   `json:"auto_run_enabled"`
	IntervalMinutes int    `json:"interval_minutes"`
	Action          string `json:"action"`
	Attempted       int    `json:"attempted"`
	Succeeded       int    `json:"succeeded"`
	NeedsAttention  int    `json:"needs_attention"`
	Skipped         int    `json:"skipped"`
	FinishedAt      string `json:"finished_at,omitempty"`
}

func taskRunHandler(ctx context.Context, env *Env, args []string) (*Response, error) {
	fs := flag.NewFlagSet("task-run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	scheduled := fs.Bool("scheduled", false, "由系统调度器启动")
	jsonOut := fs.Bool("json", false, "以稳定 JSON 输出")
	configDirFlag := fs.String("config-dir", "", "QKA 配置目录")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	_ = scheduled // 保留给系统任务；执行语义与手动 task-run 相同
	configDir := ResolveConfigDir(env.Getenv)
	if *configDirFlag != "" {
		configDir = *configDirFlag
	}
	resp, err := runTaskRun(ctx, configDir, defaultTaskRunDeps(env))
	if err != nil {
		return nil, err
	}
	if !*jsonOut {
		data, _ := resp.Data.(taskRunOutput)
		if data.Status == "disabled" {
			fmt.Fprintln(env.Stdout, "自动执行未启用，本次未执行。")
		} else {
			fmt.Fprintf(env.Stdout,
				"自动执行：成功 %d，需处理 %d，跳过 %d。\n",
				data.Succeeded, data.NeedsAttention, data.Skipped)
		}
	}
	return resp, nil
}

func runTaskRun(ctx context.Context, configDir string, deps taskRunDeps) (*Response, error) {
	if deps.LoadSchedule == nil || deps.SaveLastRun == nil || deps.AcquireLock == nil ||
		deps.Build == nil || deps.Now == nil {
		return nil, fmt.Errorf("task-run: incomplete dependencies")
	}

	// Fast path: a disabled schedule performs no lock creation, database write or
	// network call. If enabled, re-read under the cross-process lock to close the
	// toggle race.
	preflight, err := deps.LoadSchedule(configDir)
	if err != nil {
		return nil, err
	}
	if !preflight.Enabled {
		return disabledTaskRunResponse(), nil
	}

	lock, err := deps.AcquireLock(configDir)
	if err != nil {
		return nil, err
	}
	defer lock.Close()

	cfg, err := deps.LoadSchedule(configDir)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return disabledTaskRunResponse(), nil
	}

	runner, err := deps.Build(ctx, configDir)
	if err != nil {
		now := deps.Now()
		if saveErr := deps.SaveLastRun(configDir, schedule.LastRun{
			At:      now.Format(time.RFC3339),
			Result:  "failed",
			Message: "无法启动任务执行，详情请查看脱敏日志",
		}); saveErr != nil {
			return nil, saveErr
		}
		out := taskRunOutput{
			Status: "failed", AutoRunEnabled: true, IntervalMinutes: cfg.IntervalMinutes,
			Action:         cfg.Action,
			NeedsAttention: 1, FinishedAt: now.Format(time.RFC3339),
		}
		return &Response{Status: "failed", Data: out, ExitCode: 1}, nil
	}
	defer runner.Close()

	summary, runErr := runner.RunAllActionSummary(ctx, cfg.Action)
	if runErr != nil && summary.NeedsAttention == 0 {
		summary.NeedsAttention = 1
	}
	if recorder, ok := runner.(taskRunRecorder); ok {
		// Execution results remain authoritative even if best-effort logging fails.
		_ = recorder.RecordTaskRun(ctx, summary)
	}
	result := "success"
	if summary.NeedsAttention > 0 {
		result = "needs_attention"
	}
	now := deps.Now()
	last := schedule.LastRun{
		At:             now.Format(time.RFC3339),
		Result:         result,
		Attempted:      summary.Attempted,
		Succeeded:      summary.Succeeded,
		NeedsAttention: summary.NeedsAttention,
		Skipped:        summary.Skipped,
	}
	if runErr != nil {
		last.Message = "部分账号执行失败，详情请查看脱敏日志"
	}
	if err := deps.SaveLastRun(configDir, last); err != nil {
		return nil, err
	}
	exitCode := 0
	if summary.NeedsAttention > 0 {
		exitCode = 1
	}
	out := taskRunOutput{
		Status:          result,
		AutoRunEnabled:  true,
		IntervalMinutes: cfg.IntervalMinutes,
		Action:          cfg.Action,
		Attempted:       summary.Attempted,
		Succeeded:       summary.Succeeded,
		NeedsAttention:  summary.NeedsAttention,
		Skipped:         summary.Skipped,
		FinishedAt:      now.Format(time.RFC3339),
	}
	return &Response{Status: result, Data: out, ExitCode: exitCode}, nil
}

func disabledTaskRunResponse() *Response {
	out := taskRunOutput{
		Status: "disabled", AutoRunEnabled: false,
		IntervalMinutes: schedule.DefaultIntervalMinutes, Action: schedule.ActionCheckin,
	}
	return &Response{Status: "disabled", Data: out}
}
