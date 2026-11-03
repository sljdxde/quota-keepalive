package tokenspeed

import (
	"fmt"
	"testing"
	"time"
)

// dshTimedAssistantEvent 构造带 data.stream 逐 chunk 时间线的真实 assistant/message
// 事件：time0 为首 chunk 到达毫秒，dt 为后续 chunk 相对前一个的间隔（毫秒）。
// 这正是 DSH 会话文件里记录的真实结构，速率必须从这些数据实测得出。
func dshTimedAssistantEvent(id string, endMS int64, firstChunk, firstReason, firstText, lastChunk int64, input, reasoning, output, total int64) string {
	return fmt.Sprintf(
		`{"type":"assistant/message","seq":42,"time":%d,"data":{"turn":1,"step":1,`+
			`"message":{"role":"assistant","content":[{"type":"reasoning","text":"..."},{"type":"text","text":"ok"}]},`+
			`"source":{"kind":"model","provider":"magpie","model":"deepseek-v4-pro"},`+
			`"id":%q,`+
			`"stream":[`+
			`{"type":"chunk","time":%d,"chunk":{"type":"block-start","index":0,"blockType":"reasoning"}},`+
			`{"type":"reasoning-chunks","time0":%d,"index":0,"dt":[250,250,141]},`+
			`{"type":"chunk","time":%d,"chunk":{"type":"block-start","index":1,"blockType":"text"}},`+
			`{"type":"text-chunks","time0":%d,"index":1,"dt":[40]},`+
			`{"type":"chunk","time":%d,"chunk":{"type":"block-stop","index":1}}],`+
			`"usage":{"inputTokens":%d,"outputTokens":%d,"totalTokens":%d,`+
			`"cacheReadTokens":0,"cacheWriteTokens":0,"reasoningTokens":%d}}}`,
		endMS, id,
		firstChunk, firstReason, firstReason+1, firstText, lastChunk,
		input, output, total, reasoning)
}

func dshStepStart(turn, step int64, atMS int64) string {
	return fmt.Sprintf(`{"type":"step/start","seq":41,"time":%d,"data":{"turn":%d,"step":%d}}`,
		atMS, turn, step)
}

// TestDSHMeasuresSpeedFromStreamTimeline 验证实测速率：
// 正文段窗口 = 消息结束 - 首个正文 chunk（6500→7100 = 600ms 场景见下），
// reasoning token 从输出中扣除（100-30=70），得到 70/0.5s = 140 tok/s。
// 无 stream 时间线的事件仍退回「只出总量」。
func TestDSHMeasuresSpeedFromStreamTimeline(t *testing.T) {
	root := t.TempDir()
	now := dshFixedNow()
	endBase := now.UnixMilli()
	// 首 chunk = end-1100，reasoning 段 600ms，正文首 chunk = end-500。
	path := dshSessionPath(root, "speed000-0000-4000-8000-000000000001")
	frame := dshFrame(t,
		`{"type":"session","version":4,"id":"speed000"}`,
		dshStepStart(1, 1, endBase-2600),
		dshTimedAssistantEvent("msg-timed", endBase,
			endBase-1100, endBase-1100, endBase-500, endBase-459,
			50, 30, 100, 150),
		dshAssistantEvent("msg-plain", now.Add(-500*time.Millisecond), 10, 5, 15, 0),
	)
	dshWriteFile(t, path, frame)

	service := dshNewService(root, now)
	service.Poll()
	snapshot := service.Snapshot()

	timed, ok := service.records["dsh:speed000-0000-4000-8000-000000000001:msg-timed"]
	if !ok {
		t.Fatal("带时间线的事件未被记录")
	}
	if timed.noMeasuredRate || !timed.hasDecodeStart || !timed.hasFirstText {
		t.Fatalf("应标记为可实测: %+v", timed)
	}
	// startMS 取同 turn/step 的 step/start（覆盖 prefill 等待段）。
	if timed.startMS != endBase-2600 || timed.endMS != endBase {
		t.Fatalf("请求窗口端点错误: start=%d end=%d", timed.startMS, timed.endMS)
	}
	if timed.flowMS != 641 {
		t.Fatalf("流窗口 = %d, want 641", timed.flowMS)
	}

	// 正文速率 = (100-30) tokens / (endBase - (endBase-500)) ms = 70 / 0.5s = 140。
	if snapshot.OutputTokensPerSecond < 139.9 || snapshot.OutputTokensPerSecond > 140.1 {
		t.Fatalf("实测输出速率 = %.3f, want 140", snapshot.OutputTokensPerSecond)
	}
	var source *SourceSnapshot
	for i := range snapshot.Sources {
		if snapshot.Sources[i].Source == "dsh" {
			source = &snapshot.Sources[i]
		}
	}
	if source == nil || source.OutputTokensPerSecond < 139.9 ||
		source.OutputTokensPerSecond > 140.1 {
		t.Fatalf("来源速率错误: %+v", source)
	}
	// 今日总量必须包含两条事件（150 + 15）。
	if snapshot.TodayTotalTokens != 165 {
		t.Fatalf("今日总量 = %d, want 165", snapshot.TodayTotalTokens)
	}
	plain, ok := service.records["dsh:speed000-0000-4000-8000-000000000001:msg-plain"]
	if !ok || !plain.noMeasuredRate {
		t.Fatalf("无时间线事件应退回只出总量: %+v ok=%v", plain, ok)
	}
}

// TestDSHSpeedFallsBackToFirstChunkWhenStepMissing 验证缺少 step/start
// （如增量从半路开始）时，请求起点退回首个 chunk 时间，不虚构 prefill 等待。
func TestDSHSpeedFallsBackToFirstChunkWhenStepMissing(t *testing.T) {
	root := t.TempDir()
	now := dshFixedNow()
	endBase := now.UnixMilli()
	path := dshSessionPath(root, "fallback-0000-4000-8000-000000000002")
	frame := dshFrame(t,
		dshTimedAssistantEvent("msg-nostep", endBase,
			endBase-1100, endBase-1100, endBase-500, endBase-459,
			50, 30, 100, 150),
	)
	dshWriteFile(t, path, frame)

	service := dshNewService(root, now)
	service.Poll()

	record, ok := service.records["dsh:fallback-0000-4000-8000-000000000002:msg-nostep"]
	if !ok {
		t.Fatal("事件未被记录")
	}
	if record.startMS != endBase-1100 {
		t.Fatalf("起点应退回首 chunk: got %d, want %d", record.startMS, endBase-1100)
	}
	if snapshot := service.Snapshot(); snapshot.OutputTokensPerSecond < 139.9 ||
		snapshot.OutputTokensPerSecond > 140.1 {
		t.Fatalf("速率 = %.3f, want 140", snapshot.OutputTokensPerSecond)
	}
}

// TestDSHSpeedIgnoresCrossStepStart 验证 step/start 与消息 turn/step 不一致时
// 不套用其起点（宁可少算 prefill，也不误配）。
func TestDSHSpeedIgnoresCrossStepStart(t *testing.T) {
	root := t.TempDir()
	now := dshFixedNow()
	endBase := now.UnixMilli()
	path := dshSessionPath(root, "crossstep0-0000-4000-8000-000000000003")
	frame := dshFrame(t,
		fmt.Sprintf(`{"type":"step/start","seq":1,"time":%d,"data":{"turn":9,"step":9}}`,
			endBase-30000),
		dshTimedAssistantEvent("msg-cross", endBase,
			endBase-1100, endBase-1100, endBase-500, endBase-459,
			50, 30, 100, 150),
	)
	dshWriteFile(t, path, frame)

	service := dshNewService(root, now)
	service.Poll()

	record, ok := service.records["dsh:crossstep0-0000-4000-8000-000000000003:msg-cross"]
	if !ok {
		t.Fatal("事件未被记录")
	}
	if record.startMS != endBase-1100 {
		t.Fatalf("跨 step 的 step/start 不应被采用: %d", record.startMS)
	}
}

// TestDSHShowsGeneratingWhileStepIsOpen 验证用户正在生成时（只写了 step/start、
// 还没有 assistant/message）DSH 会显示「正在生成」并用最近实测值给出估计速率，
// 而不是一片 0。这正是「在用 dsh 但看不到速度」的核心场景。
func TestDSHShowsGeneratingWhileStepIsOpen(t *testing.T) {
	root := t.TempDir()
	now := dshFixedNow()
	endBase := now.UnixMilli()
	session := "inflight-000-4000-8000-000000000004"
	path := dshSessionPath(root, session)
	// 先有一条已完成的响应，提供实测参考值（140 tok/s）。
	frame := dshFrame(t,
		dshStepStart(1, 1, endBase-4000),
		dshTimedAssistantEvent("msg-done", endBase-3000,
			endBase-4000, endBase-4000, endBase-3500, endBase-3459,
			50, 30, 100, 150),
		// 第二次请求已经开始，但还没产出 assistant/message。
		dshStepStart(2, 1, endBase-800),
	)
	dshWriteFile(t, path, frame)

	service := dshNewService(root, now)
	service.Poll()
	snapshot := service.Snapshot()

	if snapshot.GeneratingAgents != 1 {
		t.Fatalf("进行中的 DSH 响应应计为正在生成，实际 %d（%+v）",
			snapshot.GeneratingAgents, snapshot.Sources)
	}
	var source *SourceSnapshot
	for i := range snapshot.Sources {
		if snapshot.Sources[i].Source == "dsh" {
			source = &snapshot.Sources[i]
		}
	}
	if source == nil {
		t.Fatal("缺少 dsh 来源行")
	}
	if source.GeneratingAgents != 1 {
		t.Fatalf("dsh 来源应标记生成中: %+v", source)
	}
	// 用最近实测值做估计，必须明显大于 0。
	if source.OutputTokensPerSecond < 100 {
		t.Fatalf("生成中应给出估计速率，实际 %.1f", source.OutputTokensPerSecond)
	}
}

// TestDSHMeasureStreamBranches 直接覆盖时间线抽取的边界：
// 缺 time0 的组必须跳过、多个同类组取最早、重复时间不覆盖、tool-call 组也计时。
func TestDSHMeasureStreamBranches(t *testing.T) {
	stream := []dshStreamEntry{
		{Type: "chunk", Time: 100},
		{Type: "chunk", Time: 50},                            // 更早的 chunk 边界不应覆盖较新值
		{Type: "reasoning-chunks", Time0: 0, DT: []int64{5}}, // 缺起点，跳过
		{Type: "reasoning-chunks", Time0: 200, DT: []int64{10}},
		{Type: "reasoning-chunks", Time0: 150, DT: []int64{-3}}, // 负数间隔忽略
		{Type: "text-chunks", Time0: 260, DT: nil},
		{Type: "text-chunks", Time0: 300, DT: []int64{7}},
		{Type: "tool-call-chunks", Time0: 120, DT: []int64{40}},
		{Type: "unknown-group", Time0: 999},
	}
	got := dshMeasureStream(stream)
	if got.firstChunkMS != 120 {
		t.Fatalf("首 chunk = %d, want 120", got.firstChunkMS)
	}
	if got.firstReasonMS != 150 {
		t.Fatalf("首 reasoning = %d, want 150", got.firstReasonMS)
	}
	if got.firstTextMS != 260 {
		t.Fatalf("首正文 = %d, want 260", got.firstTextMS)
	}
	// 最大结束时间来自 tool-call 组（120+40=160）或 text 组（300+7=307）。
	if got.lastChunkMS != 307 {
		t.Fatalf("末 chunk = %d, want 307", got.lastChunkMS)
	}
	// 空时间线必须全部为零，不能凭空造时间。
	if empty := dshMeasureStream(nil); empty != (dshStreamTiming{}) {
		t.Fatalf("空时间线应全零: %+v", empty)
	}
}

// TestDSHIngestHeaderAndStepEdges 覆盖 request/header 与 step/start 的边界：
// 缺 config、空模型、无 state、无 view 时都不能 panic，也不能误写状态。
func TestDSHIngestHeaderAndStepEdges(t *testing.T) {
	now := dshFixedNow()
	service := newCoverageService(now)
	path := dshSessionPath(t.TempDir(), "edges")
	state := &dshFileState{}

	cases := []string{
		`{"type":"request/header","time":1792486955000,"data":null}`,
		`{"type":"request/header","time":1792486955000,"data":{"header":null}}`,
		`{"type":"request/header","time":1792486955000,"data":{"header":{"config":null}}}`,
		`{"type":"request/header","time":1792486955000,"data":{"header":{"config":{"model":"  "}}}}`,
		`{"type":"step/start","time":0,"data":{"turn":1,"step":1}}`,
		`{"type":"step/start","time":1792486955000,"data":null}`,
	}
	for _, line := range cases {
		service.ingestDSH(path, state, []byte(line))
	}
	if state.view != nil || state.stepMS != 0 {
		t.Fatalf("边界输入不应写入状态: view=%+v stepMS=%d", state.view, state.stepMS)
	}

	// 正常 request/header 写入模型；同名模型重复出现保持幂等。
	service.ingestDSH(path, state, []byte(
		`{"type":"request/header","time":1792486955000,"data":{"header":{"config":{"model":"deepseek-v4-pro"}}}}`))
	state.view = &fileState{source: "dsh", agentID: "dsh:edges"}
	service.ingestDSH(path, state, []byte(
		`{"type":"request/header","time":1792486955100,"data":{"header":{"config":{"model":"deepseek-v4-pro"}}}}`))
	if state.view.model != "deepseek-v4-pro" {
		t.Fatalf("模型未被记录: %+v", state.view)
	}

	// state 为 nil 时 step/start 与 request/header 必须安全返回。
	service.ingestDSH(path, nil, []byte(
		`{"type":"step/start","time":1792486956000,"data":{"turn":2,"step":3}}`))
	service.ingestDSH(path, nil, []byte(
		`{"type":"request/header","time":1792486956000,"data":{"header":{"config":{"model":"m"}}}}`))

	// 完成的 assistant 消息必须把 view 从「生成中」复位为空闲。
	state.view.active = true
	state.view.inFlightMS = 1792486956000
	service.ingestDSH(path, state, []byte(dshAssistantEvent(
		"msg-edge", now, 10, 5, 15, 0)))
	if state.view.active || state.view.inFlightMS != 0 || state.view.streaming {
		t.Fatalf("完成后应复位为空闲: %+v", state.view)
	}
	if state.view.lastLineMS != now.UnixMilli() || state.messageCount != 1 {
		t.Fatalf("完成后应推进游标: %+v count=%d", state.view, state.messageCount)
	}
}
