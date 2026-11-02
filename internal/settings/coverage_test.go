package settings

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValuesReturnsSchemaProjection(t *testing.T) {
	s := Default()
	if err := s.SetString("theme", "dark"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("launch_at_login", true); err != nil {
		t.Fatal(err)
	}
	values := s.Values()
	if values["theme"] != "dark" || values["launch_at_login"] != true {
		t.Fatalf("unexpected settings projection: %#v", values)
	}
	if len(values) != len(Schema) {
		t.Fatalf("projection has %d fields, want %d", len(values), len(Schema))
	}
}

func TestSetStringRejectsUnknownKey(t *testing.T) {
	if err := Default().SetString("missing", "value"); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown string key returned %v", err)
	}
}

func TestValidateRejectsEveryInvalidShape(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   error
	}{
		{name: "unknown key", values: map[string]any{"missing": true}, want: ErrUnknownKey},
		{name: "bool wrong type", values: map[string]any{"launch_at_login": "true"}, want: ErrWrongType},
		{name: "select wrong type", values: map[string]any{"theme": true}, want: ErrWrongType},
		{name: "select invalid option", values: map[string]any{"theme": "sepia"}, want: ErrInvalidOption},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := (&Settings{values: tc.values}).validate(); !errors.Is(err, tc.want) {
				t.Fatalf("validate returned %v, want %v", err, tc.want)
			}
		})
	}

	original := Schema
	Schema = append(append([]Field(nil), original...), Field{Key: "future_type", Type: "future"})
	t.Cleanup(func() { Schema = original })
	if err := (&Settings{values: map[string]any{"future_type": "value"}}).validate(); !errors.Is(err, ErrWrongType) {
		t.Fatalf("unsupported field type returned %v", err)
	}
}

func TestLoadRejectsReadJSONAndSchemaErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(file); err == nil {
		t.Fatal("a path below a regular file should fail to load")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("invalid JSON should fail to load")
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"theme":"sepia"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("invalid saved value returned %v", err)
	}
}

func TestRetiredValueNormalizationKeepsUnsupportedShapes(t *testing.T) {
	if got := normalizeRetiredValue("theme", "animals"); got != "animals" {
		t.Fatalf("non-title key changed to %v", got)
	}
	if got := normalizeRetiredValue("title_theme", true); got != true {
		t.Fatalf("non-string title theme changed to %v", got)
	}
	if got := normalizeRetiredValue("title_theme", "unknown"); got != "unknown" {
		t.Fatalf("unknown title theme changed to %v", got)
	}
}

func TestSaveRejectsInvalidSettingsBeforeLocking(t *testing.T) {
	s := &Settings{values: map[string]any{"missing": true}}
	if err := s.Save(t.TempDir()); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("invalid settings save returned %v", err)
	}
}

func TestSavePropagatesMarshalError(t *testing.T) {
	boom := errors.New("marshal")
	original := marshalSettings
	marshalSettings = func(any, string, string) ([]byte, error) {
		return nil, boom
	}
	t.Cleanup(func() { marshalSettings = original })

	if err := Default().Save(t.TempDir()); !errors.Is(err, boom) {
		t.Fatalf("marshal error returned %v", err)
	}
}

func TestWithConfigLockFailurePaths(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := withConfigLock(filepath.Join(file, "child"), func() error {
		called = true
		return nil
	}); err == nil {
		t.Fatal("EnsureDir failure should be returned")
	}
	if called {
		t.Fatal("callback should not run when EnsureDir fails")
	}

	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "settings.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := withConfigLock(dir, func() error { return nil }); err == nil {
		t.Fatal("OpenFile failure should be returned")
	}
}
