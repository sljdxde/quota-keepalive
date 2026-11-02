package tokenspeed

import (
	"encoding/json"
	"strings"
	"time"
)

type claudeFileState struct {
	preciseStart bool
	responses    map[string]responseRecord
}

type claudeEnvelope struct {
	Type       string         `json:"type"`
	Timestamp  string         `json:"timestamp"`
	UUID       string         `json:"uuid"`
	RequestID  string         `json:"requestId"`
	Entrypoint string         `json:"entrypoint"`
	Cwd        string         `json:"cwd"`
	Sidechain  bool           `json:"isSidechain"`
	ToolEnds   bool           `json:"toolEndsTurn"`
	Message    *claudeMessage `json:"message"`
}

type claudeMessage struct {
	ID         string          `json:"id"`
	Model      string          `json:"model"`
	StopReason *string         `json:"stop_reason"`
	Usage      *claudeUsage    `json:"usage"`
	Content    []claudeContent `json:"content"`
}

type claudeContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeUsage struct {
	InputTokens    json.Number `json:"input_tokens"`
	CacheCreation  json.Number `json:"cache_creation_input_tokens"`
	CacheRead      json.Number `json:"cache_read_input_tokens"`
	OutputTokens   json.Number `json:"output_tokens"`
	ThinkingTokens json.Number `json:"thinking_tokens"`
}

type claudeOutputDetails struct {
	ThinkingTokens json.Number `json:"thinking_tokens"`
}

func (s *Service) ingestClaude(fileID string, state *fileState, line []byte) {
	s.ingestClaudeSource("claude", fileID, state, line)
}

func (s *Service) ingestClaudeSource(source, fileID string, state *fileState, line []byte) {
	var envelope claudeEnvelope
	if json.Unmarshal(line, &envelope) != nil {
		return
	}
	ts, ok := parseTimestamp(envelope.Timestamp)
	if !ok {
		return
	}
	state.lastLineMS = ts
	switch envelope.Type {
	case "api-request":
		resetOutputTiming(state)
		state.pendingMS = ts
		state.hasPending = true
		state.inFlightMS = ts
		state.active = true
		state.claude.preciseStart = true
	case "user":
		if claudeInterrupted(envelope) || envelope.ToolEnds {
			state.active = false
			state.hasPending = false
			state.inFlightMS = 0
			state.claude.preciseStart = false
			resetOutputTiming(state)
			return
		}
		if !state.claude.preciseStart {
			resetOutputTiming(state)
			state.pendingMS = ts
			state.hasPending = true
		}
		state.inFlightMS = state.pendingMS
		state.active = true
	case "assistant":
		s.ingestClaudeAssistant(source, fileID, state, ts, envelope)
	}
}

func (s *Service) ingestClaudeAssistant(source, fileID string, state *fileState, ts int64, envelope claudeEnvelope) {
	message := envelope.Message
	if message == nil || message.Model == "<synthetic>" || message.Usage == nil {
		return
	}
	hasText, hasReasoning := claudeContentKinds(message)
	markOutputTiming(state, ts, hasText, hasReasoning)
	key := message.ID
	if key == "" {
		key = envelope.RequestID
	}
	if key == "" {
		key = envelope.UUID
	}
	if key == "" {
		return
	}
	recordKey := source + ":" + key
	claim := s.claim(recordKey, fileID)
	if claim == dedupOtherFile {
		return
	}
	if state.claude.responses == nil {
		state.claude.responses = make(map[string]responseRecord)
	}
	normalized := message.Usage.normalized(message)
	final := message.StopReason != nil
	internalKey := recordKey
	record, exists := state.claude.responses[internalKey]
	if exists {
		record.endMS = maxInt64(record.endMS, ts)
		record.usage = mergeUsage(record.usage, normalized)
		record.final = record.final || final
		record.model = firstNonEmpty(message.Model, record.model)
	} else {
		record = responseRecord{
			key:            internalKey,
			source:         source,
			agentID:        fileID,
			model:          message.Model,
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
			final:          final,
			usage:          normalized,
			updatedAt:      time.UnixMilli(ts),
		}
		state.pendingMS = 0
		state.hasPending = false
		state.claude.preciseStart = false
	}
	record.decodeStartMS = firstPositive(record.decodeStartMS, state.firstOutputMS)
	record.hasDecodeStart = record.decodeStartMS > 0
	record.firstTextMS = firstPositive(record.firstTextMS, state.firstTextMS)
	record.hasFirstText = record.firstTextMS > 0
	record.firstReasonMS = firstPositive(record.firstReasonMS, state.firstReasonMS)
	record.hasFirstReason = record.firstReasonMS > 0
	record.flowMS = maxInt64(record.flowMS, contentFlowMS(state))
	record.updatedAt = time.UnixMilli(ts)
	state.claude.responses[internalKey] = record
	s.upsertRecord(record)

	if message.StopReason == nil && !claudeHasToolUse(message) {
		state.active = true
		state.inFlightMS = record.startMS
		if !record.hasStart {
			state.inFlightMS = ts
		}
	} else {
		state.active = false
		state.inFlightMS = 0
	}
}

func claudeContentKinds(message *claudeMessage) (text, reasoning bool) {
	for _, content := range message.Content {
		switch content.Type {
		case "text":
			text = text || strings.TrimSpace(content.Text) != ""
		case "thinking", "redacted_thinking":
			reasoning = true
		}
	}
	return text, reasoning
}

func firstPositive(first, second int64) int64 {
	if first > 0 {
		return first
	}
	return second
}

func (u *claudeUsage) normalized(message *claudeMessage) usage {
	input := numberTokens(u.InputTokens) + numberTokens(u.CacheCreation) + numberTokens(u.CacheRead)
	output := numberTokens(u.OutputTokens)
	reasoning := numberTokens(u.ThinkingTokens)
	// Claude Code stores thinking token details inside message.usage in some
	// versions, which is covered by OutputTokens. Do not add them again.
	return usage{
		input:     input,
		cached:    numberTokens(u.CacheRead),
		cacheNew:  numberTokens(u.CacheCreation),
		output:    output,
		reasoning: reasoning,
		total:     input + output,
	}
}

func mergeUsage(previous, next usage) usage {
	return usage{
		input:     maxInt64(previous.input, next.input),
		cached:    maxInt64(previous.cached, next.cached),
		cacheNew:  maxInt64(previous.cacheNew, next.cacheNew),
		output:    maxInt64(previous.output, next.output),
		reasoning: maxInt64(previous.reasoning, next.reasoning),
		total:     maxInt64(previous.total, next.total),
	}
}

func claudeInterrupted(envelope claudeEnvelope) bool {
	if envelope.Message == nil {
		return false
	}
	for _, content := range envelope.Message.Content {
		if strings.HasPrefix(content.Text, "[Request interrupted by user") {
			return true
		}
	}
	return false
}

func claudeHasToolUse(message *claudeMessage) bool {
	if message == nil {
		return false
	}
	for _, content := range message.Content {
		if content.Type == "tool_use" {
			return true
		}
	}
	return false
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func firstNonEmpty(first, second string) string {
	if first != "" {
		return first
	}
	return second
}
