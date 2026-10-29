package engine

import (
	"testing"
	"time"
)

func TestBeijingDayIndependentOfMachineZone(t *testing.T) {
	// UTC 16:30 = 北京次日 00:30。
	utc := time.Date(2026, 10, 28, 16, 30, 0, 0, time.UTC)
	if day := BeijingDay(utc); day != "2026-10-29" {
		t.Errorf("期望 2026-10-29，实际 %s", day)
	}
}

func TestBeijingDayBeforeMidnightBoundary(t *testing.T) {
	// 北京 23:59:59 仍属当日。
	b := time.Date(2026, 10, 28, 23, 59, 59, 0, Beijing)
	if day := BeijingDay(b); day != "2026-10-28" {
		t.Errorf("期望 2026-10-28，实际 %s", day)
	}
}

func TestBeijingDayAfterMidnightBoundary(t *testing.T) {
	// 北京次日 00:00:01 属次日。
	b := time.Date(2026, 10, 29, 0, 0, 1, 0, Beijing)
	if day := BeijingDay(b); day != "2026-10-29" {
		t.Errorf("期望 2026-10-29，实际 %s", day)
	}
}

func TestBeijingDayFromWesternHemisphereZone(t *testing.T) {
	// 纽约 EDT（UTC-4）10-28 12:00 = 北京 10-29 00:00。
	ny := time.FixedZone("EDT", -4*3600)
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, ny)
	if day := BeijingDay(t0); day != "2026-10-29" {
		t.Errorf("期望 2026-10-29，实际 %s", day)
	}
}
