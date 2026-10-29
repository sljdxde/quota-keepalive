package cli

import (
	"errors"
	"testing"
)

func TestBoundedBufferAcceptsWithinLimit(t *testing.T) {
	b := NewBoundedBuffer(10)
	if _, err := b.Write([]byte("hello")); err != nil {
		t.Fatalf("限额内不应报错: %v", err)
	}
	if b.String() != "hello" {
		t.Errorf("内容不符: %q", b.String())
	}
}

func TestBoundedBufferRejectsWhenExceedingLimit(t *testing.T) {
	b := NewBoundedBuffer(5)
	_, err := b.Write([]byte("this is way too long"))
	if err == nil {
		t.Fatal("超出限额应返回错误")
	}
	var overflow ErrOutputOverflow
	if !errors.As(err, &overflow) {
		t.Fatalf("期望 ErrOutputOverflow，实际 %T", err)
	}
}

func TestBoundedBufferRejectsSecondWriteThatCrossesLimit(t *testing.T) {
	b := NewBoundedBuffer(8)
	_, _ = b.Write([]byte("abcd"))
	_, err := b.Write([]byte("efghij"))
	if err == nil {
		t.Fatal("累计超限应返回错误")
	}
}
