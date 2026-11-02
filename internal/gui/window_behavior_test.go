//go:build darwin && !ios

package gui

import (
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func TestTogglePanelWindowCoversShowAndHide(t *testing.T) {
	shown := &fakePanelController{}
	togglePanelWindow(shown)
	if !shown.shown {
		t.Fatal("hidden panel should be shown")
	}

	hidden := &fakePanelController{shownInitially: true}
	togglePanelWindow(hidden)
	if !hidden.hidden {
		t.Fatal("visible panel should be hidden")
	}
}

type fakePanelController struct {
	shownInitially bool
	shown          bool
	hidden         bool
}

func (f *fakePanelController) visible() bool { return f.shownInitially }
func (f *fakePanelController) show()         { f.shown = true }
func (f *fakePanelController) hide()         { f.hidden = true }

func TestPanelActionsForwardToNativeWindow(t *testing.T) {
	app := New(Deps{})
	actions := panelActions{window: app.ensurePanel()}
	if actions.visible() {
		t.Fatal("new panel should start hidden")
	}
	actions.show()
	actions.hide()
}

func TestMacReopenRoutesOnlyAfterStartupGrace(t *testing.T) {
	app := New(Deps{})
	app.startedAt.Store(0)
	if app.canHandleReopen(time.Now()) {
		t.Fatal("reopen before ApplicationStarted should be ignored")
	}

	now := time.Now()
	app.startedAt.Store(now.Add(-reopenStartupGrace).UnixNano())
	if !app.canHandleReopen(now) {
		t.Fatal("reopen after startup grace should open the main window")
	}

	event := &application.ApplicationEvent{Id: uint(events.Mac.ApplicationShouldHandleReopen)}
	app.handleApplicationReopen(event)
	if !event.IsCancelled() || app.main == nil {
		t.Fatal("late reopen should cancel framework restore and show main")
	}

	app.showPanel()
	app.startedAt.Store(0)
	event = &application.ApplicationEvent{Id: uint(events.Mac.ApplicationShouldHandleReopen)}
	app.handleApplicationReopen(event)
	if !event.IsCancelled() || app.panel == nil || app.panel.IsVisible() {
		t.Fatal("early reopen should cancel framework restore and keep windows hidden")
	}
}
