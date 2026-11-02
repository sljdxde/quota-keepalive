package tokenspeed

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type codexEnvelope struct {
	Timestamp string       `json:"timestamp"`
	Ordinal   *int64       `json:"ordinal"`
	Type      string       `json:"type"`
	Payload   codexPayload `json:"payload"`
}

type codexPayload struct {
	Type                        string          `json:"type"`
	Role                        string          `json:"role"`
	ID                          string          `json:"id"`
	Cwd                         string          `json:"cwd"`
	Model                       string          `json:"model"`
	ResponseID                  string          `json:"response_id"`
	ThreadID                    string          `json:"thread_id"`
	ThreadSource                string          `json:"thread_source"`
	SubagentHistoryStartOrdinal *json.Number    `json:"subagent_history_start_ordinal"`
	Usage                       *codexUsage     `json:"usage"`
	Info                        *codexTokenInfo `json:"info"`
}

type codexUsage struct {
	InputTokens       json.Number `json:"input_tokens"`
	CachedInputTokens json.Number `json:"cached_input_tokens"`
	CacheWriteInput   json.Number `json:"cache_write_input_tokens"`
	OutputTokens      json.Number `json:"output_tokens"`
	ReasoningOutput   json.Number `json:"reasoning_output_tokens"`
	TotalTokens       json.Number `json:"total_tokens"`
}

type codexTokenInfo struct {
	TotalTokenUsage *codexUsage `json:"total_token_usage"`
	LastTokenUsage  *codexUsage `json:"last_token_usage"`
}

func (s *Service) ingestCodex(fileID string, state *fileState, line []byte) {
	var envelope codexEnvelope
	if json.Unmarshal(line, &envelope) != nil {
		return
	}
	ts, ok := parseTimestamp(envelope.Timestamp)
	if !ok {
		return
	}
	if envelope.Type == "session_meta" {
		if state.createdMS != 0 {
			return
		}
		state.createdMS = ts
		state.model = envelope.Payload.Model
		if envelope.Payload.SubagentHistoryStartOrdinal != nil {
			if ordinal, err := envelope.Payload.SubagentHistoryStartOrdinal.Int64(); err == nil && ordinal >= 0 {
				state.subagentOrd = &ordinal
			}
		}
		state.lastLineMS = ts
		return
	}
	if state.createdMS != 0 && ts < state.createdMS-2000 {
		return
	}
	if state.subagentOrd != nil && envelope.Ordinal != nil && *envelope.Ordinal < *state.subagentOrd {
		return
	}
	state.lastLineMS = ts

	switch envelope.Type {
	case "turn_context":
		if envelope.Payload.Model != "" {
			state.model = envelope.Payload.Model
		}
	case "token_usage_record":
		if envelope.Payload.Usage != nil {
			state.modernUsage = true
			key := envelope.Payload.ResponseID
			if key == "" {
				key = envelope.Payload.ThreadID
			}
			if key == "" {
				key = fileID + ":" + strconv.FormatInt(ts, 10)
			}
			if s.claim("codex:"+key, fileID) != dedupNew {
				return
			}
			record := responseRecord{
				key:            "codex:" + key,
				source:         "codex",
				agentID:        fileID,
				model:          state.model,
				startMS:        state.pendingMS,
				hasStart:       state.hasPending,
				decodeStartMS:  state.firstOutputMS,
				hasDecodeStart: state.firstOutputMS > 0,
				firstTextMS:    state.firstTextMS,
				hasFirstText:   state.firstTextMS > 0,
				firstReasonMS:  state.firstReasonMS,
				hasFirstReason: state.firstReasonMS > 0,
				flowMS:         contentFlowMS(state),
				endMS:          ts,
				final:          true,
				usage:          envelope.Payload.Usage.normalized(),
				updatedAt:      time.UnixMilli(ts),
			}
			s.upsertRecord(record)
			state.pendingMS = ts
			state.hasPending = true
			state.streaming = false
			state.active = false
			state.inFlightMS = 0
			resetOutputTiming(state)
		}
	case "event_msg":
		switch envelope.Payload.Type {
		case "task_started", "user_message":
			codexInput(state, ts)
		case "token_count":
			s.ingestCodexLegacy(fileID, state, ts, envelope.Payload.Info)
		case "task_complete", "turn_aborted":
			state.active = false
			state.streaming = false
			state.hasPending = false
			state.inFlightMS = 0
			resetOutputTiming(state)
		}
	case "response_item":
		kind := envelope.Payload.Type
		if strings.HasSuffix(kind, "_output") {
			codexInput(state, ts)
			return
		}
		switch kind {
		case "message":
			switch envelope.Payload.Role {
			case "user", "developer", "system":
				codexInput(state, ts)
			case "assistant":
				codexOutput(state, ts, true, false)
			}
		case "reasoning":
			codexOutput(state, ts, false, true)
		case "magpie_reasoning":
			codexOutput(state, ts, false, true)
		default:
			if strings.HasSuffix(kind, "_call") {
				codexOutput(state, ts, false, false)
			}
		}
	}
}

func codexInput(state *fileState, ts int64) {
	if state.streaming {
		return
	}
	state.pendingMS = ts
	state.hasPending = true
	state.inFlightMS = ts
	state.active = true
	resetOutputTiming(state)
}

func codexOutput(state *fileState, ts int64, text, reasoning bool) {
	state.streaming = true
	state.active = true
	markOutputTiming(state, ts, text, reasoning)
	if !state.hasPending {
		state.pendingMS = ts
		state.hasPending = true
	}
	if state.inFlightMS == 0 {
		state.inFlightMS = state.pendingMS
	}
}

func (s *Service) ingestCodexLegacy(fileID string, state *fileState, ts int64, info *codexTokenInfo) {
	if state.modernUsage {
		if state.streaming {
			state.active = false
			state.streaming = false
			state.pendingMS = ts
			state.hasPending = true
		}
		return
	}
	if info == nil || info.TotalTokenUsage == nil {
		return
	}
	total := info.TotalTokenUsage.normalized()
	tokens := total
	if state.prevTotals != nil {
		delta, negative := subtractUsage(total, *state.prevTotals)
		if delta.total == 0 {
			return
		}
		if negative && info.LastTokenUsage != nil {
			tokens = info.LastTokenUsage.normalized()
		} else {
			tokens = delta
		}
	} else if info.LastTokenUsage != nil {
		tokens = info.LastTokenUsage.normalized()
	}
	state.prevTotals = &total
	key := "legacy:" + fileID + ":" + strconv.FormatInt(total.total, 10)
	if s.claim(key, fileID) != dedupNew {
		return
	}
	decodeStartMS := state.firstOutputMS
	if decodeStartMS == 0 {
		decodeStartMS = state.pendingMS
	}
	s.upsertRecord(responseRecord{
		key:            key,
		source:         "codex",
		agentID:        fileID,
		model:          state.model,
		startMS:        state.pendingMS,
		hasStart:       state.hasPending,
		decodeStartMS:  decodeStartMS,
		hasDecodeStart: state.firstOutputMS > 0 || state.hasPending,
		firstTextMS:    state.firstTextMS,
		hasFirstText:   state.firstTextMS > 0,
		firstReasonMS:  state.firstReasonMS,
		hasFirstReason: state.firstReasonMS > 0,
		flowMS:         contentFlowMS(state),
		endMS:          ts,
		final:          true,
		usage:          tokens,
		updatedAt:      time.UnixMilli(ts),
	})
	state.active = false
	state.streaming = false
	state.pendingMS = ts
	state.hasPending = true
	resetOutputTiming(state)
}

func contentFlowMS(state *fileState) int64 {
	if state.firstOutputMS <= 0 || state.lastContentMS <= state.firstOutputMS {
		return 0
	}
	return state.lastContentMS - state.firstOutputMS
}

func (u *codexUsage) normalized() usage {
	total := usage{
		input:     numberTokens(u.InputTokens),
		cached:    numberTokens(u.CachedInputTokens),
		cacheNew:  numberTokens(u.CacheWriteInput),
		output:    numberTokens(u.OutputTokens),
		reasoning: numberTokens(u.ReasoningOutput),
	}
	total.total = numberTokens(u.TotalTokens)
	if total.total == 0 {
		total.total = total.input + total.output
	}
	if total.total < total.input+total.output {
		total.total = total.input + total.output
	}
	return total
}

func subtractUsage(current, previous usage) (usage, bool) {
	delta := usage{
		input:     current.input - previous.input,
		cached:    current.cached - previous.cached,
		cacheNew:  current.cacheNew - previous.cacheNew,
		output:    current.output - previous.output,
		reasoning: current.reasoning - previous.reasoning,
		total:     current.total - previous.total,
	}
	negative := delta.input < 0 || delta.cached < 0 || delta.cacheNew < 0 ||
		delta.output < 0 || delta.reasoning < 0 || delta.total < 0
	return delta, negative
}

func numberTokens(value json.Number) int64 {
	if value == "" {
		return 0
	}
	number, err := value.Float64()
	if err != nil {
		return 0
	}
	return clampTokens(number)
}

func parseTimestamp(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0, false
	}
	return parsed.UnixMilli(), true
}
