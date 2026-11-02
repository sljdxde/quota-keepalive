package tokenspeed

import (
	"encoding/json"
	"math"
	"strconv"
	"time"
)

type workbuddyFileState struct {
	responses map[string]responseRecord
}

type workbuddyEnvelope struct {
	Type         string            `json:"type"`
	Role         string            `json:"role"`
	Timestamp    json.RawMessage   `json:"timestamp"`
	ProviderData workbuddyProvider `json:"providerData"`
}

type workbuddyProvider struct {
	MessageID            string          `json:"messageId"`
	Model                string          `json:"model"`
	RequestModel         string          `json:"requestModelId"`
	StartsNewUserRequest bool            `json:"startsNewUserRequest"`
	Usage                *workbuddyUsage `json:"usage"`
}

type workbuddyUsage struct {
	InputTokens       json.Number        `json:"inputTokens"`
	OutputTokens      json.Number        `json:"outputTokens"`
	TotalTokens       json.Number        `json:"totalTokens"`
	OldInputTokens    json.Number        `json:"input_tokens"`
	OldOutputTokens   json.Number        `json:"output_tokens"`
	CacheCreation     json.Number        `json:"cacheCreationTokens"`
	OldCacheCreation  json.Number        `json:"cache_creation_input_tokens"`
	InputTokenDetails []workbuddyDetails `json:"inputTokensDetails"`
	OldInputDetails   []workbuddyDetails `json:"input_tokens_details"`
}

type workbuddyDetails struct {
	CachedTokens json.Number `json:"cached_tokens"`
}

func (s *Service) ingestWorkBuddy(source, fileID string, state *fileState, line []byte) {
	var envelope workbuddyEnvelope
	if json.Unmarshal(line, &envelope) != nil {
		return
	}
	ts, ok := parseFlexibleTimestamp(envelope.Timestamp)
	if !ok {
		return
	}
	state.lastLineMS = ts
	switch envelope.Type {
	case "message":
		if envelope.Role == "user" {
			resetOutputTiming(state)
			state.pendingMS = ts
			state.hasPending = true
			state.inFlightMS = ts
			state.active = true
			return
		}
		if envelope.Role == "assistant" {
			markOutputTiming(state, ts, true, false)
			state.active = false
			state.inFlightMS = 0
			state.pendingMS = 0
			state.hasPending = false
		}
	case "reasoning":
		markOutputTiming(state, ts, false, true)
		state.active = true
		return
	case "function_call":
		markOutputTiming(state, ts, false, false)
	case "function_call_result":
		markOutputTiming(state, ts, false, false)
		state.inFlightMS = ts
		state.active = true
		return
	}
	usageView := envelope.ProviderData.Usage
	if usageView == nil {
		return
	}
	normalized := usageView.normalized()
	if normalized.total <= 0 {
		return
	}
	key := envelope.ProviderData.MessageID
	if key == "" {
		key = strconv.FormatInt(ts, 10)
	}
	recordKey := source + ":" + key
	claim := s.claim(recordKey, fileID)
	if claim == dedupOtherFile {
		return
	}
	if state.workbuddy.responses == nil {
		state.workbuddy.responses = make(map[string]responseRecord)
	}
	record, exists := state.workbuddy.responses[recordKey]
	if exists {
		record.endMS = maxInt64(record.endMS, ts)
		record.usage = mergeUsage(record.usage, normalized)
		record.model = firstNonEmpty(
			envelope.ProviderData.Model,
			firstNonEmpty(envelope.ProviderData.RequestModel, record.model),
		)
	} else {
		startMS := state.inFlightMS
		if startMS == 0 {
			startMS = state.pendingMS
		}
		hasStart := startMS > 0 && startMS <= ts &&
			ts-startMS <= (15*time.Minute).Milliseconds()
		if !hasStart {
			startMS = 0
		}
		record = responseRecord{
			key:            recordKey,
			source:         source,
			agentID:        fileID,
			model:          firstNonEmpty(envelope.ProviderData.Model, envelope.ProviderData.RequestModel),
			startMS:        startMS,
			hasStart:       hasStart,
			decodeStartMS:  state.firstOutputMS,
			hasDecodeStart: state.firstOutputMS > 0,
			firstTextMS:    state.firstTextMS,
			hasFirstText:   state.firstTextMS > 0,
			firstReasonMS:  state.firstReasonMS,
			hasFirstReason: state.firstReasonMS > 0,
			flowMS:         contentFlowMS(state),
			endMS:          ts,
			final:          true,
			usage:          normalized,
			updatedAt:      time.UnixMilli(ts),
		}
	}
	record.updatedAt = time.UnixMilli(ts)
	state.workbuddy.responses[recordKey] = record
	state.active = false
	state.inFlightMS = 0
	state.pendingMS = 0
	state.hasPending = false
	resetOutputTiming(state)
	s.upsertRecord(record)
}

func (u *workbuddyUsage) normalized() usage {
	input := numberTokens(u.InputTokens)
	if input == 0 {
		input = numberTokens(u.OldInputTokens)
	}
	output := numberTokens(u.OutputTokens)
	if output == 0 {
		output = numberTokens(u.OldOutputTokens)
	}
	cached := sumWorkBuddyDetails(u.InputTokenDetails) + sumWorkBuddyDetails(u.OldInputDetails)
	cacheNew := numberTokens(u.CacheCreation)
	if cacheNew == 0 {
		cacheNew = numberTokens(u.OldCacheCreation)
	}
	total := numberTokens(u.TotalTokens)
	if total < input+output {
		total = input + output
	}
	return usage{
		input:     input,
		cached:    cached,
		cacheNew:  cacheNew,
		output:    output,
		reasoning: 0,
		total:     total,
	}
}

func sumWorkBuddyDetails(details []workbuddyDetails) int64 {
	var total int64
	for _, detail := range details {
		total += numberTokens(detail.CachedTokens)
	}
	return total
}

func parseFlexibleTimestamp(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if parsed, ok := parseTimestamp(text); ok {
			return parsed, true
		}
		raw = json.RawMessage(text)
	}
	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return 0, false
	}
	value, err := number.Float64()
	if err != nil || value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	switch {
	case value >= 1e12:
		return int64(value), true
	case value >= 1e9:
		return int64(value * 1000), true
	default:
		return 0, false
	}
}
