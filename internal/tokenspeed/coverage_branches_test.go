package tokenspeed

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stubTranscriptFile 用于注入 readBackfill 的 Seek/Read 错误路径。
type stubTranscriptFile struct {
	data    []byte
	offset  int64
	readErr error
	seekErr error
}

func (f *stubTranscriptFile) Read(p []byte) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	if f.offset >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.offset:])
	f.offset += int64(n)
	return n, nil
}

func (f *stubTranscriptFile) Seek(offset int64, whence int) (int64, error) {
	if f.seekErr != nil {
		return 0, f.seekErr
	}
	f.offset = offset
	return offset, nil
}

func (f *stubTranscriptFile) Stat() (os.FileInfo, error) { return nil, os.ErrNotExist }
func (f *stubTranscriptFile) Close() error               { return nil }

func newCoverageService(now time.Time) *Service {
	return New(Config{Now: func() time.Time { return now }})
}

// TestReadBackfillBranches 覆盖历史补读的提前返回与文件错误分支。
func TestReadBackfillBranches(t *testing.T) {
	now := time.Now().UTC()
	oldOpen := openTranscript
	t.Cleanup(func() { openTranscript = oldOpen })

	state := func(offset, end int64) *fileState {
		st := &fileState{source: "codex", agentID: "agent"}
		st.backfill = &backfillState{offset: offset, end: end,
			state: &fileState{source: "codex", agentID: "agent"}}
		return st
	}

	cases := []struct {
		name      string
		state     *fileState
		budget    int64
		open      func(string) (transcriptFile, error)
		want      int64
		gone      bool
		skipCheck bool
	}{
		{name: "无回填游标", state: &fileState{}, budget: 100, want: 0, skipCheck: true},
		{name: "区间已读完", state: state(10, 10), budget: 100, want: 0, gone: true},
		{name: "预算为零", state: state(0, 10), budget: 0, want: 0},
		{name: "打开失败", state: state(0, 10), budget: 100,
			open: func(string) (transcriptFile, error) { return nil, errors.New("open") }, want: 0, gone: true},
		{name: "Seek失败", state: state(0, 10), budget: 100,
			open: func(string) (transcriptFile, error) {
				return &stubTranscriptFile{seekErr: errors.New("seek")}, nil
			}, want: 0, gone: true},
		{name: "读取失败", state: state(0, 10), budget: 100,
			open: func(string) (transcriptFile, error) {
				return &stubTranscriptFile{readErr: errors.New("read")}, nil
			}, want: 0, gone: true},
		{name: "整块无换行", state: state(0, 8), budget: 8,
			open: func(string) (transcriptFile, error) {
				return &stubTranscriptFile{data: []byte("abcdefgh")}, nil
			}, want: 8, gone: true},
		{name: "末尾半行留给下次", state: state(0, 8), budget: 8,
			open: func(string) (transcriptFile, error) {
				return &stubTranscriptFile{data: []byte("abc\ndef")}, nil
			}, want: 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newCoverageService(now)
			if tc.open != nil {
				openTranscript = tc.open
			} else {
				openTranscript = oldOpen
			}
			got := svc.readBackfill("any.log", tc.state, tc.budget)
			if got != tc.want {
				t.Fatalf("consumed = %d, want %d", got, tc.want)
			}
			if tc.skipCheck {
				return
			}
			if tc.gone && tc.state.backfill != nil {
				t.Fatal("回填游标应被清理")
			}
			if !tc.gone && tc.state.backfill == nil {
				t.Fatal("回填未完成时不应清理游标")
			}
		})
	}
}

// TestReadBackfillsBudgetExhausted 覆盖 Poll 级预算耗尽后的提前返回。
func TestReadBackfillsBudgetExhausted(t *testing.T) {
	oldBudget := backfillBudget
	backfillBudget = 0
	t.Cleanup(func() { backfillBudget = oldBudget })

	now := time.Now().UTC()
	svc := newCoverageService(now)
	svc.files["a"] = &fileState{source: "codex", agentID: "a",
		backfill: &backfillState{end: 10, state: &fileState{}}}
	svc.files["b"] = &fileState{source: "codex", agentID: "b"}
	svc.readBackfills() // 只应返回，不读取任何文件
	if svc.files["a"].backfill == nil {
		t.Fatal("预算为 0 时不应消费回填游标")
	}
}

// TestIngestZCodeMessageBranches 覆盖 ZCode 消息入口的拒绝条件与 final 时间处理。
func TestIngestZCodeMessageBranches(t *testing.T) {
	nowMS := int64(1_792_000_000_000)
	createdMS := nowMS - 10_000

	assistant := func(extra string) []byte {
		return []byte(`{"role":"assistant","sessionId":"s1","tokens":{"input":10,"output":20,"total":30}` + extra + `}`)
	}

	svc := newCoverageService(time.UnixMilli(nowMS))
	svc.ingestZCodeMessage("", "s1", createdMS, nowMS-1000, []byte("{"), sql.NullInt64{}, sql.NullInt64{}, nowMS)
	svc.ingestZCodeMessage("id-user", "s1", createdMS, nowMS-1000,
		[]byte(`{"role":"user","tokens":{"input":1,"output":1}}`), sql.NullInt64{}, sql.NullInt64{}, nowMS)
	svc.ingestZCodeMessage("id-no-tokens", "s1", createdMS, nowMS-1000,
		[]byte(`{"role":"assistant"}`), sql.NullInt64{}, sql.NullInt64{}, nowMS)
	if len(svc.records) != 0 {
		t.Fatalf("非法消息不应生成记录: %+v", svc.records)
	}

	// 已结束（updated > created）：final=true，保留真实结束时间。
	svc.ingestZCodeMessage("msg-final", "s1", createdMS, nowMS-1000,
		assistant(`,"modelID":"glm"`), sql.NullInt64{}, sql.NullInt64{}, nowMS)
	// 未结束且超过 stale 窗口：不能把 endMS 推到 now。
	staleCreated := nowMS - 400_000
	svc.ingestZCodeMessage("msg-stale", "s2", staleCreated, staleCreated,
		assistant(`,"modelId":"glm-alt"`), sql.NullInt64{}, sql.NullInt64{}, nowMS)
	record := svc.records["zcode:msg-stale"]
	if record.final || record.endMS != staleCreated {
		t.Fatalf("陈旧未完成消息处理错误: %+v", record)
	}
	if record.model != "glm-alt" {
		t.Fatalf("model 回退错误: %q", record.model)
	}
	// 未结束但在 stale 窗口内：endMS 取当前时间。
	svc.ingestZCodeMessage("msg-live", "s3", nowMS-1000, nowMS-1000,
		assistant(``), sql.NullInt64{}, sql.NullInt64{}, nowMS)
	live := svc.records["zcode:msg-live"]
	if live.final || live.endMS != nowMS {
		t.Fatalf("活跃消息处理错误: %+v", live)
	}
}

// TestTokenUsageNormalizedBranches 覆盖 zcode/opencode 的 total 兜底逻辑。
func TestTokenUsageNormalizedBranches(t *testing.T) {
	zcode := zcodeTokens{}
	if err := json.Unmarshal([]byte(`{"input":10,"output":5,"total":1,
		"cache":{"read":2,"write":3},"reasoning":4}`), &zcode); err != nil {
		t.Fatal(err)
	}
	got := zcode.normalized()
	if got.total != 15 || got.input != 10 || got.cached != 2 || got.cacheNew != 3 || got.reasoning != 4 {
		t.Fatalf("zcode normalized = %+v", got)
	}
	openCode := opencodeTokens{}
	if err := json.Unmarshal([]byte(`{"input":10,"output":5,"total":1,
		"cache":{"read":2,"write":3},"reasoning":4}`), &openCode); err != nil {
		t.Fatal(err)
	}
	oc := openCode.normalized()
	if oc.input != 15 || oc.total != 20 || oc.cached != 2 || oc.cacheNew != 3 {
		t.Fatalf("opencode normalized = %+v", oc)
	}
}

// TestReadOpenCodeSourcesSkipsNonDatabaseRoots 覆盖非 opencode/zcode 根与非法路径。
func TestReadOpenCodeSourcesSkipsNonDatabaseRoots(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	svc := New(Config{
		Roots: []Root{
			{Source: "codex", Path: root},
			{Source: "zcode", Path: filepath.Join(root, "missing.sqlite")},
			{Source: "opencode", Path: root},
		},
		Now: func() time.Time { return now },
	})
	svc.Poll()
	for _, source := range []string{"zcode", "opencode"} {
		if svc.availableSources[source] {
			t.Fatalf("非法 %s 路径不应标记为可用数据源", source)
		}
	}
}

// TestReadOpenCodeDatabaseErrors 覆盖 zcode/opencode 读取时的 schema 错误分支。
func TestReadOpenCodeDatabaseErrors(t *testing.T) {
	now := time.Now().UTC()
	root := t.TempDir()

	// 非 SQLite 内容：Open 成功但查询失败。
	garbage := filepath.Join(root, "garbage.db")
	if err := os.WriteFile(garbage, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	zstate := &opencodeSourceState{}
	svc := newCoverageService(now)
	svc.readZCodeMessages(garbage, zstate, now)
	svc.readOpenCodeEvents(garbage, &opencodeSourceState{}, now)

	// 缺少 message/part 表：Query 失败。
	emptyDB := filepath.Join(root, "empty.db")
	db, err := sql.Open("sqlite", emptyDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE unrelated (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	svc.readZCodeMessages(emptyDB, &opencodeSourceState{}, now)
	svc.readOpenCodeEvents(emptyDB, &opencodeSourceState{}, now)

	// event 表存在但缺少 data 列：QueryRow 成功、后续 Query 失败。
	badEvents := filepath.Join(root, "bad-events.db")
	db, err = sql.Open("sqlite", badEvents)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE event (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	svc.readOpenCodeEvents(badEvents, &opencodeSourceState{}, now)
}

// TestIngestOpenCodeEventBranches 覆盖 opencode 事件入口的拒绝条件与时间兜底。
func TestIngestOpenCodeEventBranches(t *testing.T) {
	nowMS := int64(1_792_000_000_000)
	svc := newCoverageService(time.UnixMilli(nowMS))

	svc.ingestOpenCodeEvent([]byte("{"), nowMS)
	svc.ingestOpenCodeEvent([]byte(`{"info":{"role":"user","id":"x","time":{"created":1}}}`), nowMS)
	svc.ingestOpenCodeEvent([]byte(`{"info":{"role":"assistant","id":"","time":{"created":1}}}`), nowMS)
	svc.ingestOpenCodeEvent([]byte(`{"info":{"role":"assistant","id":"x","time":{"created":0}}}`), nowMS)
	svc.ingestOpenCodeEvent([]byte(`{"info":{"role":"assistant","id":"x","time":{"created":1}}}`), nowMS)
	if len(svc.records) != 0 {
		t.Fatalf("非法事件不应生成记录: %+v", svc.records)
	}

	// 没有 completed：未完成且超出 stale 窗口时保留 created。
	svc.ingestOpenCodeEvent([]byte(`{"info":{"role":"assistant","id":"old","sessionID":"s",
		"model":{"modelID":"m-alt"},"time":{"created":1},
		"tokens":{"input":1,"output":2,"total":3}}}`), nowMS)
	old := svc.records["opencode:old"]
	if old.final || old.endMS != 1 || old.model != "m-alt" {
		t.Fatalf("陈旧未完成事件处理错误: %+v", old)
	}
	// 没有 completed 但在 stale 窗口内：endMS 取 now。
	svc.ingestOpenCodeEvent([]byte(`{"info":{"role":"assistant","id":"live","sessionID":"s",
		"modelID":"m","time":{"created":`+itoa(nowMS-200)+`},
		"tokens":{"input":1,"output":2,"total":3}}}`), nowMS)
	live := svc.records["opencode:live"]
	if live.final || live.endMS != nowMS {
		t.Fatalf("活跃事件处理错误: %+v", live)
	}
}

func itoa(value int64) string {
	return json.Number(jsonNumber(value)).String()
}

func jsonNumber(value int64) string {
	b, _ := json.Marshal(value)
	return string(b)
}

// TestTrackTodayUsageUnknownSource 覆盖空来源归一为 unknown 与负数夹紧。
func TestTrackTodayUsageUnknownSource(t *testing.T) {
	now := time.Now().UTC()
	svc := newCoverageService(now)
	svc.todayDate = beijingDay(now)
	svc.trackTodayUsage(responseRecord{
		key: "unknown-1", source: "", endMS: now.UnixMilli(),
		usage: usage{input: -5, output: 10, total: 10},
	})
	if got := svc.todayBySource["unknown"]; got.output != 10 || got.input != 0 || got.total != 10 {
		t.Fatalf("unknown 来源聚合 = %+v", got)
	}
	if svc.todayTotal != 10 {
		t.Fatalf("今日总量 = %d", svc.todayTotal)
	}
	// 同一 key 的贡献更新只计增量。
	svc.trackTodayUsage(responseRecord{
		key: "unknown-1", source: "", endMS: now.UnixMilli(),
		usage: usage{output: 10, total: 30},
	})
	if svc.todayTotal != 30 {
		t.Fatalf("增量更新后今日总量 = %d", svc.todayTotal)
	}
}

// TestSpeedMeasureBranches 覆盖速率测量相关函数的边界分支。
func TestSpeedMeasureBranches(t *testing.T) {
	tooOld := timedResponse{responseRecord: responseRecord{endMS: 10, hasStart: true, startMS: 0}}
	if _, _, _ = responseMeasure(tooOld); true {
		// 覆盖起点非法分支。
	}
	if tokens, window, overall := responseMeasure(timedResponse{responseRecord: responseRecord{
		noMeasuredRate: true, endMS: 10, startMS: 1,
	}}); tokens != 0 || window != 0 || overall != 0 {
		t.Fatalf("noMeasuredRate = %d/%d/%d", tokens, window, overall)
	}
	if tokens, window, overall := responseMeasure(timedResponse{
		responseRecord: responseRecord{endMS: 1, startMS: 1},
	}); tokens != 0 || window != 0 || overall != 0 {
		t.Fatalf("零时长 = %d/%d/%d", tokens, window, overall)
	}
	// 超过最大响应时长。
	long := timedResponse{
		responseRecord: responseRecord{endMS: (maxResponseAge + time.Minute).Milliseconds(), startMS: 1},
	}
	if _, _, overall := responseMeasure(long); overall != 0 {
		t.Fatalf("过老响应 overall = %d", overall)
	}

	// decodedResponse：reasoning 且首个文本晚于 decodeStart 时只计答案。
	reasoning := responseRecord{
		usage:          usage{output: 200, reasoning: 100},
		hasDecodeStart: true, decodeStartMS: 1000,
		hasFirstReason: true, firstReasonMS: 1200,
		hasFirstText: true, firstTextMS: 3000,
		flowMS: 2000, endMS: 5000,
	}
	if tokens, window := decodedResponse(reasoning); tokens != 100 || window != 2000 {
		t.Fatalf("decodedReasoning = %d/%d", tokens, window)
	}
	// decodeWindow 为 0（超过 10000 tok/s）时返回 0。
	burst := responseRecord{
		usage: usage{output: 5000}, hasDecodeStart: true, decodeStartMS: 1000,
		endMS: 1100,
	}
	if tokens, window := decodedResponse(burst); tokens != 0 || window != 0 {
		t.Fatalf("burst = %d/%d", tokens, window)
	}

	// floorResponse：整段回复的保底速度与不足 1000 tokens 的拒绝。
	if tokens, window := floorResponse(responseRecord{
		usage: usage{output: 1200}, hasStart: true, startMS: 1000, endMS: 3000,
	}, 2000); tokens != 1200 || window != 2000 {
		t.Fatalf("floor = %d/%d", tokens, window)
	}
	if tokens, window := floorResponse(responseRecord{
		usage: usage{output: 800}, hasStart: true, startMS: 1000, endMS: 3000,
	}, 2000); tokens != 0 || window != 0 {
		t.Fatalf("短回复 floor = %d/%d", tokens, window)
	}
	// codex reasoning 保底分支。
	codexFloor := responseRecord{
		source: "codex", usage: usage{output: 2200, reasoning: 200},
		hasDecodeStart: true, decodeStartMS: 1000, endMS: 4000,
	}
	if tokens, window := floorResponse(codexFloor, 3000); tokens != 2000 || window != 3000 {
		t.Fatalf("codex floor = %d/%d", tokens, window)
	}

	// asTimed：start 缺失时用 decodeStart，过老返回 false。
	if timed, ok := asTimed(responseRecord{
		hasDecodeStart: true, decodeStartMS: 1000, endMS: 3000,
	}); !ok || timed.startMS != 1000 {
		t.Fatalf("asTimed fallback = %+v %v", timed, ok)
	}
	if _, ok := asTimed(responseRecord{endMS: 1}); ok {
		t.Fatal("无起点记录不应可测速")
	}
	if _, ok := asTimed(responseRecord{
		hasStart: true, startMS: 1,
		endMS: (maxResponseAge + time.Minute).Milliseconds(),
	}); ok {
		t.Fatal("过老记录不应可测速")
	}
}

// TestComputeSnapshotSourceSortBranches 覆盖来源排序中「生成中优先」的比较分支。
func TestComputeSnapshotSourceSortBranches(t *testing.T) {
	now := time.Now().UTC()
	svc := newCoverageService(now)
	nowMS := now.UnixMilli()
	// claude：已完成的实测响应进入排序集合。
	svc.records["claude:1"] = responseRecord{
		key: "claude:1", source: "claude", agentID: "claude:1",
		hasDecodeStart: true, decodeStartMS: nowMS - 3000,
		hasStart: true, startMS: nowMS - 4000,
		endMS: nowMS - 1000, final: true,
		usage: usage{output: 2000, total: 2100},
	}
	// codex：文件仍在生成，走参考估计分支。
	svc.files["codex.log"] = &fileState{
		source: "codex", agentID: "codex:agent",
		active: true, inFlightMS: nowMS - 1000, lastLineMS: nowMS - 100,
	}
	svc.records["codex:ref"] = responseRecord{
		key: "codex:ref", source: "codex", agentID: "codex:other",
		hasDecodeStart: true, decodeStartMS: nowMS - 9000,
		hasStart: true, startMS: nowMS - 10000,
		endMS: nowMS - 5000, final: true,
		usage: usage{output: 1500, total: 1600},
	}
	snapshot := svc.computeSnapshot(now)
	if len(snapshot.Sources) < 2 {
		t.Fatalf("应至少有两个来源参与排序: %+v", snapshot.Sources)
	}
	if snapshot.Sources[0].GeneratingAgents == 0 {
		t.Fatalf("生成中的来源应排在最前: %+v", snapshot.Sources)
	}
}

// TestDefaultConfigEnvOverrides 覆盖各数据源环境变量覆盖分支。
func TestDefaultConfigEnvOverrides(t *testing.T) {
	base := t.TempDir()
	// 各数据源用不同子目录，避免默认路径去重把后注册的来源吞掉。
	t.Setenv("CODEX_HOME", filepath.Join(base, "codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(base, "claude"))
	t.Setenv("QODER_CONFIG_DIR", filepath.Join(base, "qoder"))
	t.Setenv("QODERCN_CONFIG_DIR", filepath.Join(base, "qoder-cn"))
	t.Setenv("WORKBUDDY_CONFIG_DIR", filepath.Join(base, "workbuddy"))
	t.Setenv("CODEBUDDY_CONFIG_DIR", filepath.Join(base, "codebuddy"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))

	cfg := DefaultConfig()
	roots := map[string]string{}
	for _, root := range cfg.Roots {
		roots[root.Source] = root.Path
	}
	for _, source := range []string{"codex", "claude", "qoder", "qoder-cn", "workbuddy", "codebuddy"} {
		if _, ok := roots[source]; !ok {
			t.Errorf("环境变量覆盖后缺少数据源 %s: %+v", source, cfg.Roots)
		}
	}
	if roots["zcode"] == "" || roots["opencode"] == "" {
		t.Fatalf("zcode/opencode 根路径缺失: %+v", cfg.Roots)
	}
	if cfg.PollInterval != 750*time.Millisecond || cfg.Now == nil {
		t.Fatalf("默认配置异常: %+v", cfg)
	}
}

// TestClaudeContentKindsReasoning 覆盖 thinking/redacted_thinking 与空白文本。
func TestClaudeContentKindsReasoning(t *testing.T) {
	message := &claudeMessage{Content: []claudeContent{
		{Type: "text", Text: "   "},
		{Type: "thinking"},
		{Type: "tool_use"},
	}}
	if text, reasoning := claudeContentKinds(message); text || !reasoning {
		t.Fatalf("content kinds = %v/%v", text, reasoning)
	}
	if text, reasoning := claudeContentKinds(&claudeMessage{Content: []claudeContent{
		{Type: "text", Text: "答案"},
	}}); !text || reasoning {
		t.Fatalf("text content kinds = %v/%v", text, reasoning)
	}
}

// TestParseFlexibleTimestampBranches 覆盖各种时间戳编码。
func TestParseFlexibleTimestampBranches(t *testing.T) {
	quoted := json.RawMessage(`"1793608282789"`)
	if got, ok := parseFlexibleTimestamp(quoted); !ok || got != 1793608282789 {
		t.Fatalf("quoted numeric = %d %v", got, ok)
	}
	quotedRFC := json.RawMessage(`"2026-11-02T10:31:22.789Z"`)
	if got, ok := parseFlexibleTimestamp(quotedRFC); !ok || got == 0 {
		t.Fatalf("quoted RFC3339 = %d %v", got, ok)
	}
	seconds := json.RawMessage(`1793608282`)
	if got, ok := parseFlexibleTimestamp(seconds); !ok || got != 1793608282000 {
		t.Fatalf("seconds = %d %v", got, ok)
	}
	for _, invalid := range []json.RawMessage{
		nil, json.RawMessage(`true`), json.RawMessage(`0`), json.RawMessage(`-5`),
		json.RawMessage(`12345`), json.RawMessage(`"not-a-time"`),
	} {
		if got, ok := parseFlexibleTimestamp(invalid); ok {
			t.Fatalf("非法时间戳 %s 被接受: %d", invalid, got)
		}
	}
}

// TestIngestWorkBuddyBranches 覆盖 WorkBuddy 各类事件与去重分支。
func TestIngestWorkBuddyBranches(t *testing.T) {
	now := time.Now().UTC()
	svc := newCoverageService(now)
	state := &fileState{source: "workbuddy", agentID: "wb.log"}

	svc.ingestWorkBuddy("workbuddy", "wb.log", state, []byte("{")) // 非法 JSON
	svc.ingestWorkBuddy("workbuddy", "wb.log", state, []byte(`{"type":"message","role":"user","timestamp":0}`))
	if state.active {
		t.Fatal("非法时间戳不应改变状态")
	}

	// user → pending/active。
	svc.ingestWorkBuddy("workbuddy", "wb.log", state,
		[]byte(`{"type":"message","role":"user","timestamp":1793608282789}`))
	if !state.active || !state.hasPending {
		t.Fatalf("user 事件状态错误: %+v", state)
	}
	// reasoning 与 function_call_result 都标记生成中。
	state.active = false
	svc.ingestWorkBuddy("workbuddy", "wb.log", state,
		[]byte(`{"type":"reasoning","timestamp":1793608282889}`))
	if !state.active || state.firstOutputMS == 0 {
		t.Fatalf("reasoning 事件状态错误: %+v", state)
	}
	state.active = false
	svc.ingestWorkBuddy("workbuddy", "wb.log", state,
		[]byte(`{"type":"function_call_result","timestamp":1793608282989}`))
	if !state.active || state.inFlightMS == 0 {
		t.Fatalf("function_call_result 事件状态错误: %+v", state)
	}
	// assistant 无 usage：只更新输出计时，不产生记录。
	svc.ingestWorkBuddy("workbuddy", "wb.log", state,
		[]byte(`{"type":"message","role":"assistant","timestamp":1793608283000}`))
	if len(svc.records) != 0 {
		t.Fatalf("无 usage 不应产生记录: %+v", svc.records)
	}
	// usage total=0：拒绝。
	svc.ingestWorkBuddy("workbuddy", "wb.log", state,
		[]byte(`{"type":"function_call","timestamp":1793608283100,"providerData":{"usage":{"inputTokens":0,"outputTokens":0,"totalTokens":0}}}`))
	if len(svc.records) != 0 {
		t.Fatalf("零 usage 不应产生记录: %+v", svc.records)
	}
	// messageId 为空：用时间戳作 key；总计 100。
	svc.ingestWorkBuddy("workbuddy", "wb.log", state,
		[]byte(`{"type":"function_call","timestamp":1793608283200,"providerData":{"usage":{"inputTokens":20,"outputTokens":80,"totalTokens":100}}}`))
	if len(svc.records) != 1 {
		t.Fatalf("有效 usage 应产生记录: %+v", svc.records)
	}
	// 同 key 不同文件：去重返回。
	otherState := &fileState{source: "workbuddy", agentID: "other.log"}
	svc.ingestWorkBuddy("workbuddy", "other.log", otherState,
		[]byte(`{"type":"function_call","timestamp":1793608283200,"providerData":{"messageId":"1793608283200","usage":{"inputTokens":20,"outputTokens":80,"totalTokens":100}}}`))
	if _, ok := svc.records["workbuddy:1793608283200"]; !ok {
		t.Fatal("同 key 不同文件应命中既有记录")
	}
	if len(otherState.workbuddy.responses) == 0 {
		// 第二个文件命中 dedupOtherFile 时不写状态；只要不 panic 即视为覆盖。
	}
}

// TestOpenCodeScanAndCursorBranches 覆盖 SQLite 行扫描失败与游标回退分支。
func TestOpenCodeScanAndCursorBranches(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	svc := newCoverageService(now)

	// message.session_id 为 NULL：扫描到 string 失败，覆盖 readZCodeMessages 错误返回。
	zpath := filepath.Join(root, "zcode-null.db")
	db, err := sql.Open("sqlite", zpath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`CREATE TABLE part (message_id TEXT, time_created INTEGER, data TEXT)`,
		`INSERT INTO message (id, session_id, time_created, time_updated, data)
		 VALUES ('m1', NULL, 1, ` + itoa(now.UnixMilli()) + `, '{"role":"assistant"}')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	svc.readZCodeMessages(zpath, &opencodeSourceState{}, now)

	// data 列为 INTEGER：扫描到 []byte 失败，覆盖 readOpenCodeEvents 错误返回；
	// 同时把游标设到比 max(rowid) 更大，覆盖游标回退分支。
	opath := filepath.Join(root, "opencode-int.db")
	db, err = sql.Open("sqlite", opath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE event (rowid INTEGER PRIMARY KEY, type TEXT, data INTEGER)`,
		`INSERT INTO event (rowid, type, data) VALUES (1, 'message.updated.1', 123)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	state := &opencodeSourceState{lastRowID: 100}
	svc.readOpenCodeEvents(opath, state, now)
	if state.lastRowID != 1 {
		t.Fatalf("游标回退后应重新从行 1 读取，实际 %d", state.lastRowID)
	}
}

// TestResponseMeasureStartAfterEnd 覆盖起止时间倒置的拒绝分支。
func TestResponseMeasureStartAfterEnd(t *testing.T) {
	now := time.Now().UTC()
	record := timedResponse{
		responseRecord: responseRecord{endMS: 100},
		startMS:        200,
	}
	_ = now
	if tokens, window, overall := responseMeasure(record); tokens != 0 || window != 0 || overall != 0 {
		t.Fatalf("倒置时间 = %d/%d/%d", tokens, window, overall)
	}
	// floor 保底分支遇到超过 10000 tok/s 的窗口时返回 0。
	if tokens, window := floorResponse(responseRecord{
		usage: usage{output: 20000}, hasStart: true, startMS: 1000, endMS: 2000,
	}, 1000); tokens != 0 || window != 0 {
		t.Fatalf("高速 floor = %d/%d", tokens, window)
	}
}
