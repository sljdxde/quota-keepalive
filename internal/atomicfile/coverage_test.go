package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAtomicFileFailureBranches(t *testing.T) {
	t.Run("ensure dir mkdir", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := EnsureDir(filepath.Join(parent, "child"), 0o700); err == nil {
			t.Fatal("expected MkdirAll failure")
		}
	})

	t.Run("ensure dir chmod", func(t *testing.T) {
		want := errors.New("chmod failed")
		old := chmodPath
		chmodPath = func(string, fs.FileMode) error { return want }
		t.Cleanup(func() { chmodPath = old })
		if err := EnsureDir(t.TempDir(), 0o700); !errors.Is(err, want) {
			t.Fatalf("chmod error = %v, want %v", err, want)
		}
	})

	t.Run("create temp", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "missing", "state.json")
		if err := WriteFile(target, []byte("x"), 0o600); err == nil {
			t.Fatal("expected CreateTemp failure")
		}
	})

	operationError := errors.New("temporary file operation failed")
	cases := []struct {
		name     string
		chmodErr error
		writeErr error
		syncErr  error
		closeErr error
	}{
		{name: "chmod", chmodErr: operationError},
		{name: "write", writeErr: operationError},
		{name: "sync", syncErr: operationError},
		{name: "close", closeErr: operationError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := &fakeTemporaryFile{
				name:     filepath.Join(t.TempDir(), ".state-test.tmp"),
				chmodErr: tc.chmodErr,
				writeErr: tc.writeErr,
				syncErr:  tc.syncErr,
				closeErr: tc.closeErr,
			}
			old := createTempFile
			createTempFile = func(string, string) (temporaryFile, error) {
				return tmp, nil
			}
			t.Cleanup(func() { createTempFile = old })

			err := WriteFile(filepath.Join(t.TempDir(), "state.json"), []byte("x"), 0o600)
			if !errors.Is(err, operationError) {
				t.Fatalf("WriteFile error = %v, want %v", err, operationError)
			}
		})
	}

	t.Run("rename", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "state.json")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := WriteFile(target, []byte("x"), 0o600); err == nil {
			t.Fatal("expected rename failure")
		}
	})
}

func TestEndpointFailureBranches(t *testing.T) {
	t.Run("save ensure dir", func(t *testing.T) {
		config := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(config, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := SaveEndpoint(config, Endpoint{}); err == nil {
			t.Fatal("expected directory failure")
		}
	})

	t.Run("save marshal", func(t *testing.T) {
		want := errors.New("marshal failed")
		restoreMarshal(t, func(any, string, string) ([]byte, error) {
			return nil, want
		})
		if err := SaveEndpoint(t.TempDir(), Endpoint{}); !errors.Is(err, want) {
			t.Fatalf("marshal error = %v, want %v", err, want)
		}
	})

	t.Run("save write", func(t *testing.T) {
		config := t.TempDir()
		if err := os.Mkdir(filepath.Join(config, "endpoint.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := SaveEndpoint(config, Endpoint{}); err == nil {
			t.Fatal("expected endpoint write failure")
		}
	})

	t.Run("load invalid json", func(t *testing.T) {
		config := t.TempDir()
		if err := os.WriteFile(filepath.Join(config, "endpoint.json"), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadEndpoint(config); err == nil {
			t.Fatal("expected JSON decode failure")
		}
	})
}

func TestSnapshotFailureBranches(t *testing.T) {
	now := time.Unix(1700000000, 0)

	t.Run("take missing", func(t *testing.T) {
		snap, err := TakeSnapshot(t.TempDir(), now)
		if err != nil {
			t.Fatal(err)
		}
		if len(snap.Files) != 0 {
			t.Fatalf("missing files should be skipped: %+v", snap.Files)
		}
	})

	t.Run("take read error", func(t *testing.T) {
		config := t.TempDir()
		if err := os.Mkdir(filepath.Join(config, "endpoint.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := TakeSnapshot(config, now); err == nil {
			t.Fatal("expected snapshot read failure")
		}
	})

	t.Run("save ensure dir", func(t *testing.T) {
		config := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(config, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := (Snapshot{CreatedAt: now}).Save(config); err == nil {
			t.Fatal("expected snapshots directory failure")
		}
	})

	t.Run("save marshal", func(t *testing.T) {
		want := errors.New("marshal failed")
		restoreMarshal(t, func(any, string, string) ([]byte, error) {
			return nil, want
		})
		if err := (Snapshot{CreatedAt: now}).Save(t.TempDir()); !errors.Is(err, want) {
			t.Fatalf("marshal error = %v, want %v", err, want)
		}
	})

	t.Run("save write", func(t *testing.T) {
		config := t.TempDir()
		snapshotPath := filepath.Join(config, "snapshots", "snapshot-1700000000.json")
		if err := os.MkdirAll(snapshotPath, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := (Snapshot{CreatedAt: now}).Save(config); err == nil {
			t.Fatal("expected snapshot write failure")
		}
	})

	t.Run("restore missing dir", func(t *testing.T) {
		if err := RestoreLatestSnapshot(t.TempDir()); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("restore error = %v, want not exist", err)
		}
	})

	t.Run("restore no files", func(t *testing.T) {
		config := t.TempDir()
		if err := os.Mkdir(filepath.Join(config, "snapshots"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := RestoreLatestSnapshot(config); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("restore error = %v, want not exist", err)
		}
	})

	t.Run("restore ignores directories", func(t *testing.T) {
		config := t.TempDir()
		if err := SaveEndpoint(config, Endpoint{Host: "127.0.0.1", Port: 1}); err != nil {
			t.Fatal(err)
		}
		snap, err := TakeSnapshot(config, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := snap.Save(config); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(config, "snapshots", "zzz-dir"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := RestoreLatestSnapshot(config); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("restore unreadable latest", func(t *testing.T) {
		config := t.TempDir()
		dir := filepath.Join(config, "snapshots")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(config, "missing"), filepath.Join(dir, "snapshot-9.json")); err != nil {
			t.Fatal(err)
		}
		if err := RestoreLatestSnapshot(config); err == nil {
			t.Fatal("expected latest snapshot read failure")
		}
	})

	t.Run("restore invalid json", func(t *testing.T) {
		config := t.TempDir()
		dir := filepath.Join(config, "snapshots")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "snapshot-9.json"), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := RestoreLatestSnapshot(config); err == nil {
			t.Fatal("expected snapshot decode failure")
		}
	})

	t.Run("restore write", func(t *testing.T) {
		config := t.TempDir()
		snap := Snapshot{
			CreatedAt: now,
			Files:     map[string][]byte{"missing/state.json": []byte("x")},
		}
		if err := snap.Save(config); err != nil {
			t.Fatal(err)
		}
		if err := RestoreLatestSnapshot(config); err == nil {
			t.Fatal("expected restore write failure")
		}
	})
}

func restoreMarshal(
	t *testing.T,
	fn func(any, string, string) ([]byte, error),
) {
	t.Helper()
	old := marshalIndent
	marshalIndent = fn
	t.Cleanup(func() { marshalIndent = old })
}

type fakeTemporaryFile struct {
	name     string
	chmodErr error
	writeErr error
	syncErr  error
	closeErr error
}

func (f *fakeTemporaryFile) Name() string {
	return f.name
}

func (f *fakeTemporaryFile) Chmod(fs.FileMode) error {
	return f.chmodErr
}

func (f *fakeTemporaryFile) Write(data []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(data), nil
}

func (f *fakeTemporaryFile) Sync() error {
	return f.syncErr
}

func (f *fakeTemporaryFile) Close() error {
	return f.closeErr
}
