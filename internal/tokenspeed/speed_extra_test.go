package tokenspeed

import (
	"fmt"
	"testing"
	"time"
)

func TestAsTimedValidationAndClamp(t *testing.T) {
	if _, ok := asTimed(responseRecord{startMS: 1, endMS: 2}); ok {
		t.Fatal("record without start marker should not be timed")
	}
	if _, ok := asTimed(responseRecord{hasStart: true, startMS: 10, endMS: 5}); ok {
		t.Fatal("negative duration should be rejected")
	}
	if _, ok := asTimed(responseRecord{
		hasStart: true,
		startMS:  0,
		endMS:    (15 * time.Minute).Milliseconds() + 1,
	}); ok {
		t.Fatal("duration over the maximum should be rejected")
	}
	if _, ok := asTimed(responseRecord{
		hasStart: true, startMS: 100, endMS: 199,
	}); !ok {
		t.Fatal("asTimed should retain short responses so decode and floor gates can decide")
	}
	timed, ok := asTimed(responseRecord{
		hasStart: true, startMS: 100, hasDecodeStart: true,
		decodeStartMS: 1000, endMS: 2000,
	})
	if !ok || timed.durationMS != 1900 || timed.startMS != 100 {
		t.Fatalf("request start should define the wall-clock window: %+v ok=%v", timed, ok)
	}
	timed, ok = asTimed(responseRecord{
		hasStart: true, startMS: 100, hasDecodeStart: true,
		decodeStartMS: 1950, endMS: 2000,
	})
	if !ok || timed.durationMS != 1900 || timed.startMS != 100 {
		t.Fatalf("short first-output gap should remain available for the burst gate: %+v ok=%v", timed, ok)
	}
	timed, ok = asTimed(responseRecord{hasStart: true, startMS: 100, endMS: 1100})
	if !ok || timed.durationMS != 1000 || timed.startMS != 100 {
		t.Fatalf("normal duration = %+v ok=%v", timed, ok)
	}
}

func TestBuildReferencesCapsAndLookback(t *testing.T) {
	now := time.Now()
	nowMS := now.UnixMilli()
	measured := []timedResponse{
		timedFor("too-old", "codex", nowMS-fallback.Milliseconds()-1000, 1000, 1, 2, 1000),
	}
	for range recentCount + 1 {
		measured = append(measured,
			timedFor("recent", "codex", nowMS-5*time.Minute.Milliseconds(), 1000, 200, 200, 1000))
	}
	for i := range fallbackCount + 1 {
		measured = append(measured,
			timedFor(fmt.Sprintf("fallback-%d", i), "claude",
				nowMS-15*time.Minute.Milliseconds(), 1000, 4, 8, 1000))
	}

	recent, fallback := buildReferences(measured, nowMS)
	if got := recent["recent"]; got.output != 200 || got.total != 200 {
		t.Fatalf("recent per-agent reference = %+v", got)
	}
	if got := fallback["codex"]; got.output != 200 || got.total != 200 {
		t.Fatalf("fallback codex reference = %+v", got)
	}
	if got := fallback["claude"]; got.output != 4 || got.total != 4 {
		t.Fatalf("fallback claude reference = %+v", got)
	}
}

func TestComputeSnapshotUnmeasuredBranches(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	nowMS := now.UnixMilli()
	service := New(Config{Now: func() time.Time { return now }})
	reference := timedFor(
		"measured", "codex", nowMS-1000, 1000, 100, 200, 1000,
	).responseRecord
	reference.final = true
	service.records = map[string]responseRecord{
		"reference": reference,
		"no-reference": {
			key: "no-reference", agentID: "unknown", source: "claude",
			startMS: nowMS - 2000, hasStart: true, endMS: nowMS - 1000,
			usage: usage{output: 10, total: 20},
		},
		"outside-window": {
			key: "outside-window", agentID: "measured", source: "codex",
			startMS: nowMS - 10_000, hasStart: true, endMS: nowMS - 9_000,
			usage: usage{output: 10, total: 20},
		},
		"estimate": {
			key: "estimate", agentID: "measured", source: "codex",
			startMS: nowMS - 2000, hasStart: true, endMS: nowMS - 100,
			usage: usage{output: 10, total: 20},
		},
		"not-timed": {
			key: "not-timed", agentID: "unused", source: "codex",
			endMS: nowMS - 1000, usage: usage{output: 1, total: 2},
		},
	}

	snapshot := service.computeSnapshot(now)
	if len(snapshot.Sessions) == 0 {
		t.Fatal("estimated unmeasured response should create a session")
	}
}

func TestComputeSnapshotGeneratingStartBranches(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	nowMS := now.UnixMilli()
	service := New(Config{Now: func() time.Time { return now }})
	service.records = map[string]responseRecord{
		"one":   timedFor("one", "codex", nowMS-2000, 1000, 10, 20, 1000).responseRecord,
		"two":   timedFor("two", "codex", nowMS-2000, 1000, 10, 20, 1000).responseRecord,
		"three": timedFor("three", "codex", nowMS-100, 1900, 10, 20, 1900).responseRecord,
	}
	for key, record := range service.records {
		record.final = true
		record.hasStart = true
		service.records[key] = record
	}
	service.files = map[string]*fileState{
		"one": {
			source: "codex", agentID: "one", active: true,
			pendingMS: nowMS - 2000, lastLineMS: nowMS - 1000,
		},
		"two": {
			source: "codex", agentID: "two", active: true,
			lastLineMS: nowMS - 2000,
		},
		"three": {
			source: "codex", agentID: "three", active: true,
			inFlightMS: nowMS - 2000, lastLineMS: nowMS - 1000,
		},
		"future": {
			source: "codex", agentID: "future", active: true,
			inFlightMS: nowMS + 5000, lastLineMS: nowMS - 500,
		},
	}

	snapshot := service.computeSnapshot(now)
	if snapshot.GeneratingAgents != 3 {
		t.Fatalf("generating agents = %d, want 3", snapshot.GeneratingAgents)
	}
}

func TestSourceOrderingAndSessionLimit(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	nowMS := now.UnixMilli()
	tie := New(Config{Now: func() time.Time { return now }})
	tie.records = map[string]responseRecord{
		"codex":  timedFor("codex", "codex", nowMS-2000, 1000, 100, 200, 1000).responseRecord,
		"claude": timedFor("claude", "claude", nowMS-2000, 1000, 100, 200, 1000).responseRecord,
	}
	for key, record := range tie.records {
		record.final = true
		record.hasStart = true
		tie.records[key] = record
	}
	if sources := tie.computeSnapshot(now).Sources; len(sources) != 2 || sources[0].Source != "claude" {
		t.Fatalf("equal-rate sources should sort by name: %+v", sources)
	}

	different := New(Config{Now: func() time.Time { return now }})
	different.records = map[string]responseRecord{
		"codex":  timedFor("codex", "codex", nowMS-2000, 1000, 200, 300, 1000).responseRecord,
		"claude": timedFor("claude", "claude", nowMS-2000, 1000, 100, 200, 1000).responseRecord,
	}
	for key, record := range different.records {
		record.final = true
		record.hasStart = true
		different.records[key] = record
	}
	if sources := different.computeSnapshot(now).Sources; len(sources) != 2 || sources[0].Source != "codex" {
		t.Fatalf("higher-rate source should sort first: %+v", sources)
	}

	limited := New(Config{Now: func() time.Time { return now }})
	limited.files = make(map[string]*fileState)
	for i := range maxSessions + 1 {
		agent := fmt.Sprintf("idle-%02d", i)
		limited.files[agent] = &fileState{
			source: "codex", agentID: agent, lastLineMS: nowMS - int64(i),
		}
	}
	if sessions := limited.computeSnapshot(now).Sessions; len(sessions) != maxSessions {
		t.Fatalf("sessions = %d, want limit %d", len(sessions), maxSessions)
	}
}

func TestLessSessionRowAllTieBreakers(t *testing.T) {
	base := sessionRow{
		view: SessionSnapshot{
			Generating: true, OutputTokensPerSecond: 10, ID: "b",
		},
		lastMS: 10, hasSpeed: true,
	}
	if !lessSessionRow(base, sessionRow{}) {
		t.Fatal("generating should sort before idle")
	}
	noSpeed := base
	noSpeed.hasSpeed = false
	if !lessSessionRow(base, noSpeed) {
		t.Fatal("session with speed should sort first")
	}
	lowerRate := base
	lowerRate.view.OutputTokensPerSecond = 9
	if !lessSessionRow(base, lowerRate) {
		t.Fatal("higher rate should sort first")
	}
	older := base
	older.lastMS = 9
	if !lessSessionRow(base, older) {
		t.Fatal("more recent session should sort first")
	}
	higherID := base
	higherID.view.ID = "c"
	if !lessSessionRow(base, higherID) {
		t.Fatal("ID should provide a deterministic final tie-breaker")
	}
}

func timedFor(
	agentID, source string,
	endMS, durationMS int64,
	output, total, recordDuration int64,
) timedResponse {
	startMS := endMS - recordDuration
	decodeStartMS := endMS - durationMS
	return timedResponse{
		responseRecord: responseRecord{
			key: agentID, agentID: agentID, source: source,
			startMS: startMS, hasStart: true, endMS: endMS,
			decodeStartMS: decodeStartMS, hasDecodeStart: true,
			firstTextMS: decodeStartMS, hasFirstText: true,
			usage: usage{output: output, total: total},
		},
		startMS:    startMS,
		durationMS: durationMS,
	}
}
