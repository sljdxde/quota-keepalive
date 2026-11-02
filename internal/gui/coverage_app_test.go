package gui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"qka/internal/settings"
	"qka/internal/tokenspeed"
)

func waitForAsyncWork(d time.Duration) {
	time.Sleep(d)
}

func forceAppStarting(t *testing.T, app *application.App) {
	t.Helper()
	field := reflect.ValueOf(app).Elem().FieldByName("starting")
	if !field.IsValid() {
		t.Fatal("wails application has no starting field")
	}
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().SetBool(true)
}

func trayMenu(t *testing.T, tray *application.SystemTray) *application.Menu {
	t.Helper()
	field := reflect.ValueOf(tray).Elem().FieldByName("menu")
	if !field.IsValid() {
		t.Fatal("system tray has no menu field")
	}
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface().(*application.Menu)
}

func clickMenuItem(t *testing.T, item *application.MenuItem) {
	t.Helper()
	field := reflect.ValueOf(item).Elem().FieldByName("callback")
	if !field.IsValid() || field.IsNil() {
		t.Fatal("menu item has no click callback")
	}
	callback := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).
		Elem().Interface().(func(*application.Context))
	callback(nil)
}

func TestAssetHandlerServesEmbeddedFrontend(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/main.html", nil)
	rec := httptest.NewRecorder()
	assetHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("embedded asset status = %d", rec.Code)
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "<!doctype html>") {
		t.Fatal("embedded frontend was not served")
	}
}

func TestNewWiresWindowsTrayAndCallbacks(t *testing.T) {
	t.Setenv("QKA_DEBUG_SHOW", "")
	app := New(Deps{})
	if app.panel != nil || app.main != nil || app.tray == nil {
		t.Fatal("New should defer window creation and build the tray")
	}
	app.hideRestoredWindows()
	app.showPanel()
	app.togglePanel()
	app.hidePanel()
	app.ensureMainWindow()
	app.ensureMainWindow()

	current := settings.Default()
	if err := current.Set("show_dock_icon", false); err != nil {
		t.Fatal(err)
	}
	if err := current.SetString("theme", "dark"); err != nil {
		t.Fatal(err)
	}
	app = New(Deps{Settings: func() *settings.Settings { return current }})
	app.ensureMainWindow()
	app.onApplicationStarted(nil)

	app.main.HandleWindowEvent(uint(events.Common.WindowClosing))
	app.wails.Event.Emit("collapse-panel")
	app.wails.Event.Emit("open-settings")
	app.wails.Event.Emit("open-checkin")
	app.wails.Event.Emit("open-main")
	waitForAsyncWork(80 * time.Millisecond)

	menu := trayMenu(t, app.tray)
	menuField := reflect.ValueOf(menu).Elem().FieldByName("items")
	items := reflect.NewAt(menuField.Type(), unsafe.Pointer(menuField.UnsafeAddr())).
		Elem().Interface().([]*application.MenuItem)
	for _, item := range items {
		if !item.IsSeparator() {
			clickMenuItem(t, item)
		}
	}
	waitForAsyncWork(80 * time.Millisecond)

	app.SetFailureState(true)
	app.SetFailureState(false)
	app.wails.Event.Emit("quit")
	waitForAsyncWork(30 * time.Millisecond)
}

func TestNewDebugShowBranches(t *testing.T) {
	for _, value := range []string{"panel", "main", "settings", "both", "unknown"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("QKA_DEBUG_SHOW", value)
			_ = New(Deps{})
			waitForAsyncWork(820 * time.Millisecond)
		})
	}
}

func TestMonitorTokenSpeedAndRunLifecycle(t *testing.T) {
	app := New(Deps{})
	service := tokenspeed.New(tokenspeed.Config{PollInterval: time.Hour})
	app.tokenSpeed = service

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.monitorTokenSpeed(ctx)
	}()
	waitForAsyncWork(1600 * time.Millisecond)
	cancel()
	<-done

	cancelled, stop := context.WithCancel(context.Background())
	stop()
	app.monitorTokenSpeed(cancelled)

	forceAppStarting(t, app.wails)
	if err := app.Run(); err == nil {
		t.Fatal("Run should return the underlying application error")
	}

	noTokenApp := &App{wails: app.wails, tokenSpeed: nil}
	if err := noTokenApp.Run(); err == nil {
		t.Fatal("Run without token speed should propagate the application error")
	}
}

func TestNewMacNotifierUsesDefaultCommandRunner(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "notified")
	script := "#!/bin/sh\nprintf '%s' \"$*\" > \"$QKA_NOTIFY_MARKER\"\n"
	if err := os.WriteFile(filepath.Join(dir, "osascript"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("QKA_NOTIFY_MARKER", marker)

	if err := NewMacNotifier().Notify(context.Background(), "QKA", "完成"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || len(data) == 0 {
		t.Fatalf("default notifier command did not run: %q %v", data, err)
	}
}

func TestSetDockIconVisible(t *testing.T) {
	setDockIconVisible(true)
	setDockIconVisible(false)
}
