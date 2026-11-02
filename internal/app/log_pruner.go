package app

import (
	"context"
	"time"

	"qka/internal/logtail"
)

const logPruneInterval = time.Hour

// startLogPruner removes expired audit rows on startup and then rolls the
// window forward hourly while the app is running.
func (a *App) startLogPruner() {
	if a == nil || a.store == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.logPruneCancel = cancel
	a.logPruneDone = done

	a.pruneLogs(ctx)
	go a.runLogPruner(ctx, done, logPruneInterval)
}

func (a *App) runLogPruner(ctx context.Context, done chan struct{}, interval time.Duration) {
	defer close(done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.pruneLogs(ctx)
		}
	}
}

func (a *App) stopLogPruner() {
	if a == nil || a.logPruneCancel == nil {
		return
	}
	a.logPruneCancel()
	a.logPruneCancel = nil
	if a.logPruneDone != nil {
		<-a.logPruneDone
		a.logPruneDone = nil
	}
}

func (a *App) pruneLogs(parent context.Context) {
	if a == nil || a.store == nil || a.store.DB() == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	_ = logtail.Prune(ctx, a.store.DB(), a.now())
}
