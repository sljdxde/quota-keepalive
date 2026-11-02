package tokenspeed

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceStartWaitAndTicker(t *testing.T) {
	service := New(Config{PollInterval: time.Millisecond})
	service.Wait()

	ctx, cancel := context.WithCancel(context.Background())
	service.Start(ctx)
	service.Start(ctx)
	time.Sleep(5 * time.Millisecond)
	cancel()
	service.Wait()
}

func TestDiscoverSkipsAndPrunesTranscripts(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	valid := filepath.Join(root, "valid.jsonl")
	if err := os.WriteFile(valid, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignored.txt"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(valid, filepath.Join(root, "linked.jsonl")); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(root, "old.jsonl")
	if err := os.WriteFile(old, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := beijingDayStart(now).Add(-time.Minute)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	notDir := filepath.Join(root, "not-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(Config{
		Roots: []Root{
			{Source: "codex", Path: filepath.Join(root, "missing")},
			{Source: "codex", Path: notDir},
			{Source: "codex", Path: root},
		},
		Now: func() time.Time { return now },
	})
	service.Poll()
	if service.files[valid] == nil {
		t.Fatal("recent JSONL transcript should be discovered")
	}
	if service.files[old] != nil {
		t.Fatal("stale transcript should be skipped")
	}

	if err := os.Remove(valid); err != nil {
		t.Fatal(err)
	}
	service.Poll()
	if service.files[valid] != nil {
		t.Fatal("removed transcript should be pruned from file state")
	}
}

func TestDiscoverStopsAtConfiguredFileLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := maxFilesPerRoot
	maxFilesPerRoot = 0
	t.Cleanup(func() { maxFilesPerRoot = old })

	service := New(Config{Roots: []Root{{Source: "codex", Path: root}}})
	service.Poll()
	if len(service.files) != 0 {
		t.Fatalf("file limit should stop discovery: %+v", service.files)
	}
}

func TestDiscoverIgnoresWalkErrors(t *testing.T) {
	root := t.TempDir()
	old := walkDir
	walkDir = func(path string, fn fs.WalkDirFunc) error {
		return fn(path+"/broken", nil, errors.New("walk failed"))
	}
	t.Cleanup(func() { walkDir = old })

	service := New(Config{Roots: []Root{{Source: "codex", Path: root}}})
	service.Poll()
}

func TestReadFileOpenStatAndReadErrors(t *testing.T) {
	service := New(Config{})
	t.Run("open", func(t *testing.T) {
		service.readFile(filepath.Join(t.TempDir(), "missing.jsonl"), "codex", time.Now())
	})

	t.Run("non-regular", func(t *testing.T) {
		service.readFile(t.TempDir(), "codex", time.Now())
	})

	t.Run("stat", func(t *testing.T) {
		want := errors.New("stat failed")
		restoreOpen(t, func(string) (transcriptFile, error) {
			return &fakeTranscriptFile{statErr: want}, nil
		})
		service.readFile("stat.jsonl", "codex", time.Now())
	})

	t.Run("seek", func(t *testing.T) {
		want := errors.New("seek failed")
		restoreOpen(t, func(string) (transcriptFile, error) {
			return &fakeTranscriptFile{
				info:    fakeFileInfo{size: 1, mode: 0o600},
				read:    bytes.NewReader([]byte("x")),
				seekErr: want,
			}, nil
		})
		service.files["seek.jsonl"] = &fileState{offset: 1}
		service.readFile("seek.jsonl", "codex", time.Now())
	})

	t.Run("read", func(t *testing.T) {
		want := errors.New("read failed")
		restoreOpen(t, func(string) (transcriptFile, error) {
			return &fakeTranscriptFile{
				info:    fakeFileInfo{size: 1, mode: 0o600},
				read:    errorReader{err: want},
				seekErr: nil,
			}, nil
		})
		service.readFile("read.jsonl", "codex", time.Now())
	})

	t.Run("no remaining bytes", func(t *testing.T) {
		restoreOpen(t, func(string) (transcriptFile, error) {
			return &fakeTranscriptFile{
				info: fakeFileInfo{size: 1, mode: 0o600},
				read: bytes.NewReader(nil),
			}, nil
		})
		service.files["empty.jsonl"] = &fileState{offset: 1}
		service.readFile("empty.jsonl", "codex", time.Now())
	})
}

func TestReadFileInitialTailChunkAndOverlongLine(t *testing.T) {
	t.Run("initial tail aligns to newline", func(t *testing.T) {
		data := make([]byte, maxInitialTail+16)
		data[maxInitialTail+5] = '\n'
		restoreOpen(t, func(string) (transcriptFile, error) {
			return &fakeTranscriptFile{
				info: fakeFileInfo{size: int64(len(data)), mode: 0o600},
				read: bytes.NewReader(data),
			}, nil
		})
		service := New(Config{})
		service.readFile("large.jsonl", "codex", time.Now())
		state := service.files["large.jsonl"]
		if state == nil || state.offset <= maxInitialTail {
			t.Fatalf("initial offset should advance to the first complete line: %+v", state)
		}
	})

	t.Run("overlong chunk is skipped", func(t *testing.T) {
		data := make([]byte, maxReadChunk+1)
		restoreOpen(t, func(string) (transcriptFile, error) {
			return &fakeTranscriptFile{
				info: fakeFileInfo{size: int64(len(data) + 10), mode: 0o600},
				read: bytes.NewReader(data),
			}, nil
		})
		service := New(Config{})
		service.files["overlong.jsonl"] = &fileState{offset: 1}
		service.readFile("overlong.jsonl", "codex", time.Now())
		state := service.files["overlong.jsonl"]
		if state.offset != int64(maxReadChunk+1) {
			t.Fatalf("offset = %d, want %d", state.offset, maxReadChunk+1)
		}
	})
}

func TestAlignToNextLineBranches(t *testing.T) {
	if got := alignToNextLine(bytes.NewReader(nil), 0); got != 0 {
		t.Fatalf("zero offset = %d", got)
	}
	wantErr := errors.New("seek failed")
	if got := alignToNextLine(&fakeTranscriptFile{seekErr: wantErr}, 9); got != 9 {
		t.Fatalf("seek error offset = %d, want 9", got)
	}
	if got := alignToNextLine(bytes.NewReader([]byte("abc")), 1); got != 3 {
		t.Fatalf("EOF offset = %d, want 3", got)
	}
}

func TestClaimRecordPruneAndSort(t *testing.T) {
	service := New(Config{})
	if got := service.claim("k", "a"); got != dedupNew {
		t.Fatalf("first claim = %v", got)
	}
	if got := service.claim("k", "a"); got != dedupSameFile {
		t.Fatalf("same file claim = %v", got)
	}
	if got := service.claim("k", "b"); got != dedupOtherFile {
		t.Fatalf("other file claim = %v", got)
	}

	service.dedupRing = make([]string, deduperSize)
	for i := range service.dedupRing {
		service.dedupRing[i] = "old"
	}
	service.deduper["old"] = "owner"
	if got := service.claim("new", "owner"); got != dedupNew {
		t.Fatalf("evicting claim = %v", got)
	}
	if _, ok := service.deduper["old"]; ok {
		t.Fatal("oldest dedup key should be evicted")
	}

	service.upsertRecord(responseRecord{key: "zero", endMS: 0})
	if len(service.records) != 0 {
		t.Fatal("record without an end timestamp should not be stored")
	}
	now := time.Now()
	service.upsertRecord(responseRecord{key: "old", endMS: now.Add(-maxRecordAge - time.Second).UnixMilli()})
	service.upsertRecord(responseRecord{key: "new", endMS: now.UnixMilli()})
	service.prune(now)
	if _, ok := service.records["old"]; ok {
		t.Fatal("old record should be pruned")
	}
	if _, ok := service.records["new"]; !ok {
		t.Fatal("recent record should remain")
	}

	records := sortedRecent([]responseRecord{{endMS: 1}, {endMS: 3}, {endMS: 2}})
	if records[0].endMS != 3 || records[1].endMS != 2 || records[2].endMS != 1 {
		t.Fatalf("records not sorted descending: %+v", records)
	}
}

func restoreOpen(t *testing.T, fn func(string) (transcriptFile, error)) {
	t.Helper()
	old := openTranscript
	openTranscript = fn
	t.Cleanup(func() { openTranscript = old })
}

type fakeTranscriptFile struct {
	info    os.FileInfo
	statErr error
	read    io.Reader
	seekErr error
	closed  bool
}

func (f *fakeTranscriptFile) Read(p []byte) (int, error) {
	if f.read == nil {
		return 0, io.EOF
	}
	return f.read.Read(p)
}

func (f *fakeTranscriptFile) Seek(offset int64, whence int) (int64, error) {
	if f.seekErr != nil {
		return 0, f.seekErr
	}
	if seeker, ok := f.read.(io.Seeker); ok {
		return seeker.Seek(offset, whence)
	}
	return offset, nil
}

func (f *fakeTranscriptFile) Stat() (os.FileInfo, error) {
	return f.info, f.statErr
}

func (f *fakeTranscriptFile) Close() error {
	f.closed = true
	return nil
}

type fakeFileInfo struct {
	size int64
	mode os.FileMode
}

func (f fakeFileInfo) Name() string       { return "fake.jsonl" }
func (f fakeFileInfo) Size() int64        { return f.size }
func (f fakeFileInfo) Mode() os.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}
