package tokenspeed

import (
	"math"
	"testing"
)

func TestDecodeWindowRejectsBurstsAndImpossibleRates(t *testing.T) {
	if got := decodeWindow(100, 200, 100); got != 100 {
		t.Fatalf("100ms window = %d, want 100", got)
	}
	if got := decodeWindow(100, 199, 100); got != 0 {
		t.Fatalf("window shorter than 100ms = %d, want 0", got)
	}
	if got := decodeWindow(1000, 1100, 1000); got != 100 {
		t.Fatalf("at 10000 tok/s = %d, want 100", got)
	}
	if got := decodeWindow(1001, 1100, 1000); got != 0 {
		t.Fatalf("above 10000 tok/s = %d, want 0", got)
	}
}

func TestDecodedResponseSeparatesReasoningFromAnswer(t *testing.T) {
	record := responseRecord{
		source:         "claude",
		hasDecodeStart: true,
		decodeStartMS:  1000,
		hasFirstReason: true,
		firstReasonMS:  1000,
		hasFirstText:   true,
		firstTextMS:    2000,
		flowMS:         500,
		endMS:          3000,
		usage:          usage{output: 1500, reasoning: 1400},
	}
	tokens, windowMS := decodedResponse(record)
	if tokens != 100 || windowMS != 1000 {
		t.Fatalf("reasoned answer = %d/%dms, want 100/1000ms", tokens, windowMS)
	}
}

func TestDecodedResponseAllowsPacedVisibleReasoningOnly(t *testing.T) {
	record := responseRecord{
		source:         "claude",
		hasDecodeStart: true,
		decodeStartMS:  1000,
		hasFirstReason: true,
		firstReasonMS:  1000,
		flowMS:         12000,
		endMS:          15000,
		usage:          usage{output: 1500, reasoning: 1400},
	}
	tokens, windowMS := decodedResponse(record)
	if tokens != 1500 || windowMS != 14000 {
		t.Fatalf("visible reasoning = %d/%dms, want 1500/14000ms", tokens, windowMS)
	}
}

func TestDecodedResponseRejectsHiddenOrHeldReasoning(t *testing.T) {
	hidden := responseRecord{
		source:         "claude",
		hasDecodeStart: true,
		decodeStartMS:  1000,
		flowMS:         7000,
		endMS:          9000,
		usage:          usage{output: 120, reasoning: 100},
	}
	if tokens, windowMS := decodedResponse(hidden); tokens != 0 || windowMS != 0 {
		t.Fatalf("hidden reasoning should not invent a rate: %d/%d", tokens, windowMS)
	}

	held := responseRecord{
		source:         "claude",
		hasDecodeStart: true,
		decodeStartMS:  1000,
		hasFirstReason: true,
		firstReasonMS:  1000,
		flowMS:         1000,
		endMS:          15000,
		usage:          usage{output: 1500, reasoning: 1400},
	}
	if tokens, windowMS := decodedResponse(held); tokens != 0 || windowMS != 0 {
		t.Fatalf("held reasoning burst should not report decode speed: %d/%d", tokens, windowMS)
	}
}

func TestDecodedResponseRejectsAnswerBurstAfterSlowThinking(t *testing.T) {
	record := responseRecord{
		source:         "claude",
		hasDecodeStart: true,
		decodeStartMS:  1000,
		hasFirstReason: true,
		firstReasonMS:  1000,
		hasFirstText:   true,
		firstTextMS:    11500,
		flowMS:         10500,
		endMS:          11736,
		usage:          usage{output: 550, reasoning: 22},
	}
	if tokens, windowMS := decodedResponse(record); tokens != 0 || windowMS != 0 {
		t.Fatalf("answer burst after slow thinking should be rejected: %d/%d", tokens, windowMS)
	}
}

func TestCodexSessionThresholdsAndReasoningFloor(t *testing.T) {
	short := responseRecord{
		source:         "codex",
		hasDecodeStart: true,
		decodeStartMS:  1000,
		hasFirstText:   true,
		firstTextMS:    1000,
		endMS:          1900,
		usage:          usage{output: 99},
	}
	if tokens, windowMS := decodedResponse(short); tokens != 0 || windowMS != 0 {
		t.Fatalf("short Codex call should not report decode speed: %d/%d", tokens, windowMS)
	}

	threshold := short
	threshold.endMS = 2000
	threshold.usage.output = 100
	if tokens, windowMS := decodedResponse(threshold); tokens != 100 || windowMS != 1000 {
		t.Fatalf("Codex threshold = %d/%dms, want 100/1000ms", tokens, windowMS)
	}

	reasoned := responseRecord{
		source:         "codex",
		startMS:        1,
		hasStart:       true,
		hasDecodeStart: true,
		decodeStartMS:  1000,
		endMS:          3000,
		usage:          usage{output: 1000, reasoning: 800},
	}
	tokens, windowMS := floorResponse(reasoned, 2999)
	if tokens != 200 || windowMS != 2000 {
		t.Fatalf("Codex reasoning floor = %d/%dms, want 200/2000ms", tokens, windowMS)
	}
}

func TestGeneralFloorAndOpenCodeExclusion(t *testing.T) {
	record := responseRecord{
		source:   "claude",
		startMS:  1000,
		hasStart: true,
		endMS:    4000,
		usage:    usage{output: 1200},
	}
	tokens, windowMS := floorResponse(record, 3000)
	if tokens != 1200 || windowMS != 3000 {
		t.Fatalf("floor = %d/%dms, want 1200/3000ms", tokens, windowMS)
	}

	record.noMeasuredRate = true
	if tokens, windowMS := floorResponse(record, 3000); tokens != 0 || windowMS != 0 {
		t.Fatalf("unmeasured source should not be given a rate: %d/%d", tokens, windowMS)
	}
}

func TestResponseRateKeepsInputOutOfDecodeWindow(t *testing.T) {
	record := responseRecord{
		source:         "claude",
		startMS:        1000,
		hasStart:       true,
		hasDecodeStart: true,
		decodeStartMS:  4000,
		hasFirstText:   true,
		firstTextMS:    4000,
		endMS:          5000,
		usage:          usage{input: 5000, output: 1000, total: 6000},
	}
	rate, ok := responseRate(timedResponse{
		responseRecord: record,
		startMS:        record.startMS,
		durationMS:     record.endMS - record.startMS,
	})
	if !ok {
		t.Fatal("valid response should report a rate")
	}
	if math.Abs(rate.output-1000) > 0.001 ||
		math.Abs(rate.total-2250) > 0.001 {
		t.Fatalf("rate = %+v, input must use wall time, not the 1s decode window", rate)
	}
}
