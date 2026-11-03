package tokenspeed

import (
	"math"
	"sort"
	"time"
)

type timedResponse struct {
	responseRecord
	startMS    int64
	durationMS int64
}

type speedEstimate struct {
	output float64
	total  float64
}

type sessionRow struct {
	view     SessionSnapshot
	lastMS   int64
	hasSpeed bool
}

func lessSessionRow(a, b sessionRow) bool {
	if a.view.Generating != b.view.Generating {
		return a.view.Generating
	}
	if a.hasSpeed != b.hasSpeed {
		return a.hasSpeed
	}
	if a.view.OutputTokensPerSecond != b.view.OutputTokensPerSecond {
		return a.view.OutputTokensPerSecond > b.view.OutputTokensPerSecond
	}
	if a.lastMS != b.lastMS {
		return a.lastMS > b.lastMS
	}
	return a.view.ID < b.view.ID
}

func (s *Service) ingest(source, fileID string, state *fileState, line []byte) {
	switch source {
	case "codex":
		s.ingestCodex(fileID, state, line)
	case "claude":
		s.ingestClaudeSource(source, fileID, state, line)
	case "qoder", "qoder-cn":
		s.ingestClaudeSource(source, fileID, state, line)
	case "workbuddy", "codebuddy":
		s.ingestWorkBuddy(source, fileID, state, line)
	}
}

const (
	maxDecodeRate   = 10000.0
	minDecodeMS     = int64(100)
	heldShare       = int64(4)
	thinkPace       = int64(20)
	codexWindowMS   = int64(1000)
	codexBodyTokens = int64(100)
	floorBodyTokens = int64(1000)
	floorWindowMS   = int64(1000)
	maxResponseAge  = 15 * time.Minute
)

type liveRate struct {
	source string
	model  string
	output float64
	total  float64
	endMS  int64
}

func responseRate(response timedResponse) (liveRate, bool) {
	tokens, windowMS, overallMS := responseMeasure(response)
	if tokens <= 0 || windowMS <= 0 || overallMS <= 0 {
		return liveRate{}, false
	}
	seconds := float64(windowMS) / 1000
	output := float64(tokens) / seconds
	input := float64(maxInt64(0, response.usage.input)) /
		(float64(overallMS) / 1000)
	return liveRate{
		source: response.source,
		model:  response.model,
		output: output,
		total:  output + input,
		endMS:  response.endMS,
	}, true
}

func responseMeasure(response timedResponse) (tokens, windowMS, overallMS int64) {
	if response.noMeasuredRate || response.endMS <= 0 || response.startMS <= 0 {
		return 0, 0, 0
	}
	overallMS = response.endMS - response.startMS
	if overallMS <= 0 || overallMS > maxResponseAge.Milliseconds() {
		return 0, 0, 0
	}
	tokens, windowMS = decodedResponse(response.responseRecord)
	if windowMS == 0 {
		tokens, windowMS = floorResponse(response.responseRecord, overallMS)
	}
	if tokens <= 0 || windowMS <= 0 {
		return 0, 0, overallMS
	}
	return tokens, windowMS, overallMS
}

func decodedResponse(record responseRecord) (tokens, windowMS int64) {
	if record.usage.output <= 0 || !record.hasDecodeStart ||
		record.decodeStartMS <= 0 || record.endMS <= record.decodeStartMS {
		return 0, 0
	}
	tokens = record.usage.output
	startMS := record.decodeStartMS
	if record.usage.reasoning > 0 {
		switch {
		case record.hasFirstText && record.firstTextMS >= startMS:
			tokens -= record.usage.reasoning
			startMS = record.firstTextMS
		case record.hasFirstReason && record.firstReasonMS >= startMS &&
			record.flowMS > 0 && record.usage.reasoning <= tokens:
			startMS = record.firstReasonMS
		default:
			return 0, 0
		}
	}
	windowMS = decodeWindow(tokens, record.endMS, startMS)
	if windowMS == 0 {
		return 0, 0
	}
	if record.flowMS > 0 && record.flowMS*heldShare < windowMS {
		return 0, 0
	}
	if record.usage.reasoning > 0 && record.hasFirstText &&
		tokens*(record.firstTextMS-record.decodeStartMS) >
			thinkPace*record.usage.reasoning*windowMS {
		return 0, 0
	}
	if record.source == "codex" &&
		(windowMS < codexWindowMS || tokens < codexBodyTokens) {
		return 0, 0
	}
	return tokens, windowMS
}

func decodeWindow(tokens, endMS, startMS int64) int64 {
	windowMS := endMS - startMS
	if tokens <= 0 || startMS <= 0 || endMS <= startMS || windowMS < minDecodeMS {
		return 0
	}
	if tokens*1000 > int64(maxDecodeRate)*windowMS {
		return 0
	}
	return windowMS
}

func floorResponse(record responseRecord, overallMS int64) (tokens, windowMS int64) {
	if record.noMeasuredRate || overallMS <= 0 || record.usage.output <= 0 {
		return 0, 0
	}
	if record.source == "codex" && record.usage.reasoning > 0 &&
		record.hasDecodeStart && record.decodeStartMS > 0 {
		answer := record.usage.output - record.usage.reasoning
		if answer >= codexBodyTokens {
			if w := decodeWindow(answer, record.endMS, record.decodeStartMS); w >= codexWindowMS {
				return answer, w
			}
		}
	}
	if overallMS < floorWindowMS || record.usage.output < floorBodyTokens {
		return 0, 0
	}
	if w := decodeWindow(record.usage.output, record.endMS, record.startMS); w > 0 {
		return record.usage.output, w
	}
	return 0, 0
}

func (s *Service) computeSnapshot(now time.Time) Snapshot {
	nowMS := now.UnixMilli()
	records := make([]responseRecord, 0, len(s.records))
	for _, record := range s.records {
		records = append(records, record)
	}
	sortedRecent(records)

	measured := make([]timedResponse, 0, len(records))
	unmeasured := make([]timedResponse, 0, len(records))
	for _, record := range records {
		timed, ok := asTimed(record)
		if !ok {
			continue
		}
		if record.final {
			measured = append(measured, timed)
		} else {
			unmeasured = append(unmeasured, timed)
		}
	}

	recentByAgent, fallbackBySource := buildReferences(measured, nowMS)
	ratesByAgent := make(map[string]liveRate)
	generatingBySource := make(map[string]int)
	generatingByAgent := make(map[string]bool)
	lastActiveByAgent := make(map[string]int64)

	installRate := func(agentID string, rate liveRate, force bool) {
		current, ok := ratesByAgent[agentID]
		if force || !ok || rate.endMS >= current.endMS {
			ratesByAgent[agentID] = rate
		}
	}

	// A completed response keeps its measured decode speed through the short
	// live window. The newest response wins when a session has several turns.
	for _, response := range measured {
		if response.endMS > nowMS || nowMS-response.endMS > liveWindow.Milliseconds() {
			continue
		}
		rate, ok := responseRate(response)
		if !ok {
			continue
		}
		installRate(response.agentID, rate, false)
		if response.endMS > lastActiveByAgent[response.agentID] {
			lastActiveByAgent[response.agentID] = response.endMS
		}
	}

	generatingAgents := 0
	for agentID, state := range s.files {
		if !state.active || state.lastLineMS == 0 ||
			nowMS-state.lastLineMS > staleAfter.Milliseconds() {
			if state.lastLineMS > lastActiveByAgent[agentID] {
				lastActiveByAgent[agentID] = state.lastLineMS
			}
			continue
		}
		start := state.inFlightMS
		if start == 0 {
			start = state.pendingMS
		}
		if start > nowMS {
			if state.lastLineMS > lastActiveByAgent[agentID] {
				lastActiveByAgent[agentID] = state.lastLineMS
			}
			continue
		}
		generatingAgents++
		generatingBySource[state.source]++
		generatingByAgent[agentID] = true
		delete(ratesByAgent, agentID)
		if estimate, ok := referenceFor(state.agentID, state.source, recentByAgent, fallbackBySource); ok {
			installRate(agentID, liveRate{
				source: state.source,
				model:  state.model,
				output: estimate.output,
				total:  estimate.total,
				endMS:  nowMS,
			}, true)
		}
		if state.lastLineMS > lastActiveByAgent[agentID] {
			lastActiveByAgent[agentID] = state.lastLineMS
		}
	}

	for _, response := range unmeasured {
		if nowMS-response.endMS > staleAfter.Milliseconds() || generatingByAgent[response.agentID] {
			continue
		}
		generatingAgents++
		generatingBySource[response.source]++
		generatingByAgent[response.agentID] = true
		if estimate, ok := referenceFor(response.agentID, response.source, recentByAgent, fallbackBySource); ok {
			installRate(response.agentID, liveRate{
				source: response.source,
				model:  response.model,
				output: estimate.output,
				total:  estimate.total,
				endMS:  response.endMS,
			}, true)
		}
		if response.endMS > lastActiveByAgent[response.agentID] {
			lastActiveByAgent[response.agentID] = response.endMS
		}
	}

	outputTokens := 0.0
	totalTokens := 0.0
	ratesBySource := make(map[string]*SourceSnapshot)
	sessionRows := make([]sessionRow, 0, len(ratesByAgent))
	ensureSource := func(source string) *SourceSnapshot {
		rate := ratesBySource[source]
		if rate == nil {
			rate = &SourceSnapshot{Source: source, Label: sourceLabel(source)}
			ratesBySource[source] = rate
		}
		return rate
	}
	for agentID, rate := range ratesByAgent {
		outputTokens += rate.output
		totalTokens += rate.total
		sourceRate := ensureSource(rate.source)
		sourceRate.OutputTokensPerSecond += rate.output
		sourceRate.TotalTokensPerSecond += rate.total
		project := s.projectByAgent[agentID]
		session := SessionSnapshot{
			ID:      sessionID(agentID),
			Project: project,
			// 会话名优先用项目/工作区名；拿不到时回退到模型名，
			// 最后才是短 ID（避免面板里出现一排含义不明的哈希）。
			Label:                 sessionDisplayLabel(project, rate.model, agentID),
			Source:                rate.source,
			SourceLabel:           sourceLabel(rate.source),
			Model:                 rate.model,
			OutputTokensPerSecond: roundRate(rate.output),
			TotalTokensPerSecond:  roundRate(rate.total),
		}
		session.InputTokensPerSecond = roundRate(math.Max(0, session.TotalTokensPerSecond-session.OutputTokensPerSecond))
		session.Generating = generatingByAgent[agentID]
		sessionRows = append(sessionRows, sessionRow{
			view:     session,
			lastMS:   lastActiveByAgent[agentID],
			hasSpeed: session.OutputTokensPerSecond > 0 || session.Generating,
		})
	}

	out := Snapshot{
		OutputTokensPerSecond: roundRate(outputTokens),
		TotalTokensPerSecond:  roundRate(totalTokens),
		TodayTotalTokens:      s.todayTotal,
		TodayTokenDate:        s.todayDate,
		GeneratingAgents:      generatingAgents,
		UpdatedAt:             now.Format(time.RFC3339Nano),
		Sources:               make([]SourceSnapshot, 0, len(ratesBySource)),
		Sessions:              make([]SessionSnapshot, 0, len(sessionRows)),
	}
	out.InputTokensPerSecond = roundRate(math.Max(0, out.TotalTokensPerSecond-out.OutputTokensPerSecond))
	out.Active = generatingAgents > 0 || out.OutputTokensPerSecond >= 0.05
	out.TitleThemeID = NormalizeTitleTheme(s.titleTheme)
	out.Title = ThemeTitle(out.TitleThemeID, out.OutputTokensPerSecond, out.Active)
	out.TitleTheme = TitleThemeLabel(out.TitleThemeID)
	out.TitleCatalog = TitleThemeCatalog()

	// 今天有消耗、但此刻没有速率的来源也要有一行（否则今日总量对的、列表却是空）。
	for source, today := range s.todayBySource {
		if today.total > 0 {
			ensureSource(source)
		}
	}
	// 只保留「真正用过」的来源：今天有消耗、正在生成或还有速率。配置里存在但
	// 今天没用过的来源不再占一行，避免一上来铺满整屏的空 Agent。
	used := make(map[string]struct{}, len(ratesBySource))
	for source, rate := range ratesBySource {
		rate.OutputTokensPerSecond = roundRate(rate.OutputTokensPerSecond)
		rate.TotalTokensPerSecond = roundRate(rate.TotalTokensPerSecond)
		rate.InputTokensPerSecond = roundRate(math.Max(0, rate.TotalTokensPerSecond-rate.OutputTokensPerSecond))
		rate.GeneratingAgents = generatingBySource[source]
		today := s.todayBySource[source]
		rate.TodayInputTokens = today.input
		rate.TodayOutputTokens = today.output
		rate.TodayTotalTokens = today.total
		if rate.GeneratingAgents > 0 || rate.OutputTokensPerSecond > 0 ||
			rate.TotalTokensPerSecond > 0 || today.total > 0 {
			used[source] = struct{}{}
			out.Sources = append(out.Sources, *rate)
		}
	}
	for source := range s.availableSources {
		if _, ok := used[source]; !ok {
			out.IdleSourceCount++
		}
	}
	sort.SliceStable(out.Sources, func(i, j int) bool {
		if (out.Sources[i].GeneratingAgents > 0) != (out.Sources[j].GeneratingAgents > 0) {
			return out.Sources[i].GeneratingAgents > 0
		}
		if out.Sources[i].OutputTokensPerSecond == out.Sources[j].OutputTokensPerSecond {
			if out.Sources[i].TodayTotalTokens == out.Sources[j].TodayTotalTokens {
				return out.Sources[i].Source < out.Sources[j].Source
			}
			return out.Sources[i].TodayTotalTokens > out.Sources[j].TodayTotalTokens
		}
		return out.Sources[i].OutputTokensPerSecond > out.Sources[j].OutputTokensPerSecond
	})
	if len(out.Sources) > 0 {
		out.Agent = out.Sources[0].Label
	}

	seenSession := make(map[string]bool, len(ratesByAgent))
	for agentID := range ratesByAgent {
		seenSession[agentID] = true
	}
	for _, response := range records {
		agentID := response.agentID
		if seenSession[agentID] || nowMS-response.endMS > sessionVisible.Milliseconds() {
			continue
		}
		seenSession[agentID] = true
		project := response.project
		if project == "" {
			project = s.projectByAgent[agentID]
		}
		sessionRows = append(sessionRows, sessionRow{
			view: SessionSnapshot{
				ID:          sessionID(agentID),
				Project:     project,
				Label:       sessionDisplayLabel(project, response.model, agentID),
				Source:      response.source,
				SourceLabel: sourceLabel(response.source),
				Model:       response.model,
			},
			lastMS: response.endMS,
		})
	}
	// Recent idle sessions remain visible briefly, matching the requested
	// session-level view without retaining prompts or transcript paths.
	// s.files 的键是文件路径，会话身份必须用 state.agentID——否则同一个会话会
	// 以「路径哈希」和「agentID 哈希」出现两次，且名字永远退回短 ID。
	for _, state := range s.files {
		agentID := state.agentID
		if agentID == "" || seenSession[agentID] || state.lastLineMS == 0 ||
			nowMS-state.lastLineMS > sessionVisible.Milliseconds() {
			continue
		}
		seenSession[agentID] = true
		project := state.project
		if project == "" {
			project = s.projectByAgent[agentID]
		}
		sessionRows = append(sessionRows, sessionRow{
			view: SessionSnapshot{
				ID:          sessionID(agentID),
				Project:     project,
				Label:       sessionDisplayLabel(project, state.model, agentID),
				Source:      state.source,
				SourceLabel: sourceLabel(state.source),
				Model:       state.model,
			},
			lastMS: state.lastLineMS,
		})
	}
	// 同一项目下可能同时有多个会话，名字会完全一样（都叫「临时文件」），
	// 用短 ID 后缀区分，否则用户无法知道哪一行是哪一个。
	labelCount := make(map[string]int, len(sessionRows))
	for _, row := range sessionRows {
		labelCount[row.view.Label]++
	}
	for i := range sessionRows {
		view := &sessionRows[i].view
		if labelCount[view.Label] > 1 {
			view.Label = view.Label + " #" + view.ID[:4]
		}
	}
	sort.SliceStable(sessionRows, func(i, j int) bool {
		return lessSessionRow(sessionRows[i], sessionRows[j])
	})
	if len(sessionRows) > maxSessions {
		sessionRows = sessionRows[:maxSessions]
	}
	for _, row := range sessionRows {
		view := row.view
		if row.lastMS > 0 {
			view.LastActiveAt = time.UnixMilli(row.lastMS).Format(time.RFC3339Nano)
		}
		out.Sessions = append(out.Sessions, view)
	}
	return out
}

func asTimed(record responseRecord) (timedResponse, bool) {
	startMS := int64(0)
	if record.hasStart && record.startMS > 0 {
		startMS = record.startMS
	} else if record.hasDecodeStart && record.decodeStartMS > 0 {
		startMS = record.decodeStartMS
	}
	duration := record.endMS - startMS
	if startMS <= 0 || duration <= 0 || duration > maxResponseAge.Milliseconds() {
		return timedResponse{}, false
	}
	return timedResponse{
		responseRecord: record,
		startMS:        startMS,
		durationMS:     duration,
	}, true
}

func buildReferences(measured []timedResponse, nowMS int64) (map[string]speedEstimate, map[string]speedEstimate) {
	recentByAgent := make(map[string]speedEstimate)
	type sums struct {
		count           int
		outputTokens    int64
		outputDuration  int64
		inputTokens     int64
		overallDuration int64
	}
	agentSums := make(map[string]sums)
	for _, response := range measured {
		if response.endMS < nowMS-recentReference.Milliseconds() {
			continue
		}
		current := agentSums[response.agentID]
		if current.count >= recentCount {
			continue
		}
		tokens, windowMS, overallMS := responseMeasure(response)
		if windowMS == 0 {
			continue
		}
		current.count++
		current.outputTokens += tokens
		current.outputDuration += windowMS
		current.inputTokens += maxInt64(0, response.usage.input)
		current.overallDuration += overallMS
		agentSums[response.agentID] = current
	}
	for agentID, current := range agentSums {
		output := float64(current.outputTokens) /
			(float64(current.outputDuration) / 1000)
		input := float64(current.inputTokens) /
			(float64(current.overallDuration) / 1000)
		recentByAgent[agentID] = speedEstimate{output: output, total: output + input}
	}

	fallbackBySource := make(map[string]speedEstimate)
	sourceSums := make(map[string]sums)
	for _, response := range measured {
		if response.endMS < nowMS-fallback.Milliseconds() {
			continue
		}
		current := sourceSums[response.source]
		if current.count >= fallbackCount {
			continue
		}
		tokens, windowMS, overallMS := responseMeasure(response)
		if windowMS == 0 {
			continue
		}
		current.count++
		current.outputTokens += tokens
		current.outputDuration += windowMS
		current.inputTokens += maxInt64(0, response.usage.input)
		current.overallDuration += overallMS
		sourceSums[response.source] = current
	}
	for source, current := range sourceSums {
		output := float64(current.outputTokens) /
			(float64(current.outputDuration) / 1000)
		input := float64(current.inputTokens) /
			(float64(current.overallDuration) / 1000)
		fallbackBySource[source] = speedEstimate{output: output, total: output + input}
	}
	return recentByAgent, fallbackBySource
}

func referenceFor(agentID, source string, recentByAgent, fallbackBySource map[string]speedEstimate) (speedEstimate, bool) {
	if estimate, ok := recentByAgent[agentID]; ok {
		return estimate, true
	}
	estimate, ok := fallbackBySource[source]
	return estimate, ok
}

func roundRate(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0
	}
	return math.Round(value*1000) / 1000
}
