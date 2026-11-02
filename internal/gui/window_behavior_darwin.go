//go:build darwin && !ios

package gui

import (
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const reopenStartupGrace = 800 * time.Millisecond

func (a *App) installPlatformWindowBehavior() {
	// Wails normally opens every hidden window on Dock reopen. Cancel that
	// behavior so QKA can keep the menu-bar panel behind the tray icon and
	// route an explicit Dock activation to the five-tab main window.
	a.wails.Event.RegisterApplicationEventHook(
		events.Mac.ApplicationShouldHandleReopen,
		a.handleApplicationReopen,
	)
}

func (a *App) handleApplicationReopen(event *application.ApplicationEvent) {
	event.Cancel()
	if a.canHandleReopen(time.Now()) {
		a.showMain()
		return
	}
	a.hideRestoredWindows()
}

func (a *App) canHandleReopen(now time.Time) bool {
	started := a.startedAt.Load()
	return started > 0 && now.Sub(time.Unix(0, started)) >= reopenStartupGrace
}
