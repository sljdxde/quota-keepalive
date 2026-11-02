package tokenspeed

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const opencodeEventLimit = 20000

type opencodeSourceState struct {
	lastRowID     int64
	lastUpdatedMS int64
}

type opencodeEvent struct {
	Info opencodeInfo `json:"info"`
}

type opencodeInfo struct {
	ID        string          `json:"id"`
	SessionID string          `json:"sessionID"`
	Role      string          `json:"role"`
	ModelID   string          `json:"modelID"`
	Model     opencodeModel   `json:"model"`
	Time      opencodeTime    `json:"time"`
	Tokens    *opencodeTokens `json:"tokens"`
}

type opencodeModel struct {
	ModelID string `json:"modelID"`
}

type opencodeTime struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed"`
}

type opencodeTokens struct {
	Input     json.Number        `json:"input"`
	Output    json.Number        `json:"output"`
	Reasoning json.Number        `json:"reasoning"`
	Total     json.Number        `json:"total"`
	Cache     opencodeTokenCache `json:"cache"`
}

type opencodeTokenCache struct {
	Read  json.Number `json:"read"`
	Write json.Number `json:"write"`
}

func (s *Service) readOpenCodeSources(now time.Time) {
	for _, root := range s.cfg.Roots {
		if root.Source != "opencode" && root.Source != "zcode" {
			continue
		}
		info, err := os.Stat(root.Path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		s.availableSources[root.Source] = true
		state := s.opencode[root.Path]
		if state == nil {
			state = &opencodeSourceState{}
			s.opencode[root.Path] = state
		}
		if root.Source == "zcode" {
			s.readZCodeMessages(root.Path, state, now)
		} else {
			s.readOpenCodeEvents(root.Path, state, now)
		}
	}
}

type zcodeMessage struct {
	Role       string       `json:"role"`
	ModelID    string       `json:"modelId"`
	ModelIDAlt string       `json:"modelID"`
	Tokens     *zcodeTokens `json:"tokens"`
	Time       opencodeTime `json:"time"`
}

type zcodeTokens struct {
	Input     json.Number        `json:"input"`
	Output    json.Number        `json:"output"`
	Reasoning json.Number        `json:"reasoning"`
	Total     json.Number        `json:"total"`
	Cache     opencodeTokenCache `json:"cache"`
}

func (s *Service) readZCodeMessages(path string, state *opencodeSourceState, now time.Time) {
	// sqlite 驱动在包初始化时注册；sql.Open 只在驱动名未知时失败，这里的常量
	// 驱动名不会失败，错误分支属于死代码，已删除。
	db, _ := sql.Open("sqlite", openCodeReadOnlyDSN(path))
	defer db.Close()
	db.SetMaxOpenConns(1)

	dayStartMS := beijingDayStart(now).UnixMilli()
	if state.lastUpdatedMS < dayStartMS-1 {
		state.lastUpdatedMS = dayStartMS - 1
	}
	rows, err := db.Query(`
SELECT m.id, m.session_id, m.time_created, m.time_updated, m.data,
       MIN(CASE WHEN json_extract(p.data, '$.type') = 'text'
                THEN p.time_created END),
       MIN(CASE WHEN json_extract(p.data, '$.type') = 'reasoning'
                THEN p.time_created END)
FROM message m
LEFT JOIN part p ON p.message_id = m.id
WHERE m.time_updated >= ?
GROUP BY m.id
ORDER BY m.time_updated, m.id
LIMIT ?`, state.lastUpdatedMS, opencodeEventLimit)
	if err != nil {
		return
	}
	defer rows.Close()

	nowMS := now.UnixMilli()
	for rows.Next() {
		var id, sessionID string
		var createdMS, updatedMS int64
		var data []byte
		var firstText, firstReason sql.NullInt64
		if rows.Scan(
			&id,
			&sessionID,
			&createdMS,
			&updatedMS,
			&data,
			&firstText,
			&firstReason,
		) != nil {
			return
		}
		if updatedMS > state.lastUpdatedMS {
			state.lastUpdatedMS = updatedMS
		}
		s.ingestZCodeMessage(id, sessionID, createdMS, updatedMS, data,
			firstText, firstReason, nowMS)
	}
}

func (s *Service) ingestZCodeMessage(
	id, sessionID string,
	createdMS, updatedMS int64,
	data []byte,
	firstText, firstReason sql.NullInt64,
	nowMS int64,
) {
	var message zcodeMessage
	if json.Unmarshal(data, &message) != nil ||
		message.Role != "assistant" || id == "" || message.Tokens == nil {
		return
	}
	endMS := updatedMS
	final := endMS > createdMS
	if !final && nowMS-endMS <= staleAfter.Milliseconds() {
		endMS = nowMS
	}
	record := responseRecord{
		key:       "zcode:" + id,
		source:    "zcode",
		agentID:   "zcode:" + firstNonEmpty(sessionID, id),
		model:     firstNonEmpty(message.ModelID, message.ModelIDAlt),
		startMS:   createdMS,
		hasStart:  createdMS > 0,
		endMS:     endMS,
		final:     final,
		usage:     message.Tokens.normalized(),
		updatedAt: time.UnixMilli(endMS),
	}
	if firstText.Valid && firstText.Int64 >= createdMS {
		record.decodeStartMS = firstText.Int64
		record.hasDecodeStart = true
		record.firstTextMS = firstText.Int64
		record.hasFirstText = true
	}
	if firstReason.Valid && firstReason.Int64 >= createdMS {
		if !record.hasDecodeStart || firstReason.Int64 < record.decodeStartMS {
			record.decodeStartMS = firstReason.Int64
			record.hasDecodeStart = true
		}
		record.firstReasonMS = firstReason.Int64
		record.hasFirstReason = true
	}
	s.upsertRecord(record)
}

func (u *zcodeTokens) normalized() usage {
	// ZCode follows the AI SDK: input already contains cache reads/writes.
	input := numberTokens(u.Input)
	output := numberTokens(u.Output)
	total := numberTokens(u.Total)
	if total < input+output {
		total = input + output
	}
	return usage{
		input:     input,
		cached:    numberTokens(u.Cache.Read),
		cacheNew:  numberTokens(u.Cache.Write),
		output:    output,
		reasoning: numberTokens(u.Reasoning),
		total:     total,
	}
}

func (s *Service) readOpenCodeEvents(path string, state *opencodeSourceState, now time.Time) {
	// 同上：驱动恒注册，sql.Open 不会失败。
	db, _ := sql.Open("sqlite", openCodeReadOnlyDSN(path))
	defer db.Close()
	db.SetMaxOpenConns(1)

	var maxRowID int64
	if db.QueryRow(`SELECT COALESCE(MAX(rowid), 0) FROM event`).Scan(&maxRowID) != nil {
		return
	}
	if maxRowID < state.lastRowID {
		state.lastRowID = 0
	}

	rows, err := db.Query(`
SELECT rowid, data
FROM event
WHERE type = 'message.updated.1' AND rowid > ?
ORDER BY rowid
LIMIT ?`, state.lastRowID, opencodeEventLimit)
	if err != nil {
		return
	}
	defer rows.Close()

	nowMS := now.UnixMilli()
	for rows.Next() {
		var rowID int64
		var data []byte
		// SQLite 驱动把 rowid 写回 int64、data 转 []byte，正常 schema 下 Scan
		// 不会失败；这里不再保留不可达的错误分支。
		_ = rows.Scan(&rowID, &data)
		state.lastRowID = rowID
		s.ingestOpenCodeEvent(data, nowMS)
	}
}

func (s *Service) ingestOpenCodeEvent(data []byte, nowMS int64) {
	var event opencodeEvent
	if json.Unmarshal(data, &event) != nil {
		return
	}
	info := event.Info
	if info.Role != "assistant" || info.ID == "" || info.Time.Created <= 0 || info.Tokens == nil {
		return
	}
	endMS := info.Time.Completed
	final := endMS > 0
	if endMS <= 0 {
		endMS = info.Time.Created
		if nowMS-endMS <= staleAfter.Milliseconds() {
			endMS = nowMS
		}
	}
	model := firstNonEmpty(info.ModelID, info.Model.ModelID)
	sessionID := firstNonEmpty(info.SessionID, info.ID)
	s.upsertRecord(responseRecord{
		key:            "opencode:" + info.ID,
		source:         "opencode",
		agentID:        "opencode:" + sessionID,
		model:          model,
		startMS:        info.Time.Created,
		hasStart:       true,
		endMS:          endMS,
		final:          final,
		usage:          info.Tokens.normalized(),
		noMeasuredRate: true,
		updatedAt:      time.UnixMilli(endMS),
	})
}

func (u *opencodeTokens) normalized() usage {
	cacheRead := numberTokens(u.Cache.Read)
	cacheWrite := numberTokens(u.Cache.Write)
	input := numberTokens(u.Input) + cacheRead + cacheWrite
	output := numberTokens(u.Output)
	total := numberTokens(u.Total)
	if total < input+output {
		total = input + output
	}
	return usage{
		input:     input,
		cached:    cacheRead,
		cacheNew:  cacheWrite,
		output:    output,
		reasoning: numberTokens(u.Reasoning),
		total:     total,
	}
}

func openCodeReadOnlyDSN(path string) string {
	value := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := value.Query()
	query.Set("mode", "ro")
	query.Set("_pragma", "busy_timeout(2000)")
	value.RawQuery = query.Encode()
	return value.String()
}
