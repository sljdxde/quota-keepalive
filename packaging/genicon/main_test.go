package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRunDefaultOutput(t *testing.T) {
	oldWrite, oldPNG := writeFile, applicationPNG
	t.Cleanup(func() {
		writeFile, applicationPNG = oldWrite, oldPNG
	})

	applicationPNG = func() []byte { return []byte("png") }
	var gotPath string
	var gotData []byte
	var gotMode os.FileMode
	writeFile = func(path string, data []byte, mode os.FileMode) error {
		gotPath, gotData, gotMode = path, data, mode
		return nil
	}
	if err := run(nil); err != nil {
		t.Fatal(err)
	}
	if gotPath != "packaging/appicon_1024.png" || string(gotData) != "png" || gotMode != 0o644 {
		t.Fatalf("默认输出参数错误: %q %q %o", gotPath, gotData, gotMode)
	}
}

func TestRunExplicitOutputAndWriteFailure(t *testing.T) {
	oldWrite, oldPNG := writeFile, applicationPNG
	t.Cleanup(func() {
		writeFile, applicationPNG = oldWrite, oldPNG
	})

	applicationPNG = func() []byte { return []byte("png") }
	writeFile = func(path string, data []byte, mode os.FileMode) error {
		if path != filepath.Join("tmp", "icon.png") {
			t.Fatalf("应使用显式路径，实际 %q", path)
		}
		return errors.New("write failed")
	}
	if err := run([]string{filepath.Join("tmp", "icon.png")}); err == nil {
		t.Fatal("写入失败应返回错误")
	}
}

func TestMainSuccessAndFailure(t *testing.T) {
	oldCommand := runCommand
	t.Cleanup(func() { runCommand = oldCommand })

	var passed []string
	runCommand = func(args []string) error {
		passed = append([]string(nil), args...)
		return nil
	}
	main()
	if len(passed) != len(os.Args)-1 {
		t.Fatalf("main 应传递命令行参数，实际 %v", passed)
	}

	runCommand = func([]string) error { return errors.New("boom") }
	defer func() {
		if recover() == nil {
			t.Fatal("run 失败时 main 应 panic")
		}
	}()
	main()
}
