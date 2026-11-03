package tokenspeed

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// DeepSeek Harness（DSH）会话文件是 zstd 压缩的 JSONL：
// ~/.dsh/sessions/<workspace>/<session>/session.v4.jsonl.zstd。
//
// 与明文 JSONL 不同，压缩流以「每个事件追加一个独立 zstd frame」的方式增长，
// 因此优先只解压上次游标之后的新增压缩字节；文件被整体重写、被截断或新增字节
// 不是 frame 起点时，回退为整文件重解压。解析结果通过 upsertRecord 的 key 去重，
// 整文件重解压不会重复累计今日总量。
//
// DSH 的 assistant/message 事件在 data.stream 里记录了逐 chunk 的真实时间线
// （time0 为首 chunk 到达毫秒，dt 为后续每个 chunk 相对前一个的间隔），
// 因此本数据源可以给出实测解码速度；stream 缺失或没有内容 chunk 时
// （旧会话格式或纯聚合事件）退回 noMeasuredRate=true，只贡献今日总量。
const dshSuffix = ".jsonl.zstd"

// dshFileState 记录一个 DSH 压缩文件的解压游标与最近一次 stat 结果。
// lastSize/lastModMS 用于「文件未变化就跳过解压」的快路径；
// compressedOffset 是已完整消费的压缩字节数，用于只解压新追加的 frame。
type dshFileState struct {
	compressedOffset int64
	lastSize         int64
	lastModMS        int64
	// step/start 事件时间（turn/step 用于与紧随其后的 assistant/message 对齐，
	// 作为本次响应的请求起点，覆盖 prefill 到首 token 的等待段）。
	stepTurn int64
	stepNum  int64
	stepMS   int64
	// view 是注册到 s.files 的实时状态（active/streaming/model/lastLineMS），
	// 让 DSH 与其他数据源共用「正在生成」与速率估计逻辑。
	view *fileState
	// projectResolved 表示会话头 cwd 已给出权威项目名（可能为空）。
	projectResolved bool
	// 已见过的 assistant 消息数，用于判断是否仍在流式输出。
	messageCount int64
	chunkCount   int64
}

// dshDecoder 复用 zstd 解码器，避免每个 Poll 重复分配窗口内存。
// Poll 期间由 Service.mu 串行保护，无需额外锁。
type dshDecoder struct {
	dec *zstd.Decoder
}

type dshEvent struct {
	Type string `json:"type"`
	Seq  int64  `json:"seq"`
	Time int64  `json:"time"`
	// Cwd 出现在 session 头事件里，是会话真实的工作目录——比从编码目录名
	// 反推可靠（目录名用 "-" 同时表示路径分隔符和名称里的连字符，无法无损还原）。
	Cwd  string   `json:"cwd"`
	Data *dshData `json:"data"`
	// 少数版本可能把 usage 放在事件顶层，解析时作为 data.usage 的兜底。
	Usage *dshUsage `json:"usage"`
}

type dshData struct {
	ID      string           `json:"id"`
	Turn    int64            `json:"turn"`
	Step    int64            `json:"step"`
	Header  *dshHeader       `json:"header"`
	Message *dshMessage      `json:"message"`
	Source  *dshSource       `json:"source"`
	Usage   *dshUsage        `json:"usage"`
	Stream  []dshStreamEntry `json:"stream"`
}

// dshHeader 对应 request/header 事件；config.model/provider 是当前请求真正使用的
// 模型。assistant/message 本身不带 source 字段，模型只能从这里取。
type dshHeader struct {
	Config *dshHeaderConfig `json:"config"`
}

type dshHeaderConfig struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// dshStreamEntry 是 data.stream 里的一个时间线条目：
// type=="chunk" 时 time 为该块边界毫秒；
// type=="*-chunks"（reasoning-chunks/text-chunks/tool-call-chunks）时
// time0 为首个内容 chunk 到达毫秒、dt 为后续 chunk 相对前一个的间隔序列。
type dshStreamEntry struct {
	Type  string  `json:"type"`
	Time  int64   `json:"time"`
	Time0 int64   `json:"time0"`
	DT    []int64 `json:"dt"`
}

type dshStreamTiming struct {
	firstChunkMS  int64
	firstReasonMS int64
	firstTextMS   int64
	lastChunkMS   int64
}

// dshMeasureStream 从 stream 时间线取首 token、首 reasoning/正文与末 chunk 时间。
// 只信任 time0/dt 这类真实记录，不反推；缺 time0 的组用 dt 之和也无起点，直接跳过。
func dshMeasureStream(stream []dshStreamEntry) dshStreamTiming {
	var t dshStreamTiming
	endOf := func(entry dshStreamEntry) int64 {
		end := entry.Time0
		for _, d := range entry.DT {
			if d > 0 {
				end += d
			}
		}
		return end
	}
	for _, entry := range stream {
		switch entry.Type {
		case "chunk":
			if entry.Time > t.lastChunkMS {
				t.lastChunkMS = entry.Time
			}
		case "reasoning-chunks", "text-chunks", "tool-call-chunks":
			if entry.Time0 <= 0 {
				continue
			}
			if t.firstChunkMS == 0 || entry.Time0 < t.firstChunkMS {
				t.firstChunkMS = entry.Time0
			}
			switch entry.Type {
			case "reasoning-chunks":
				if t.firstReasonMS == 0 || entry.Time0 < t.firstReasonMS {
					t.firstReasonMS = entry.Time0
				}
			case "text-chunks":
				if t.firstTextMS == 0 || entry.Time0 < t.firstTextMS {
					t.firstTextMS = entry.Time0
				}
			}
			if end := endOf(entry); end > t.lastChunkMS {
				t.lastChunkMS = end
			}
		}
	}
	return t
}

type dshMessage struct {
	Role string `json:"role"`
}

type dshSource struct {
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// dshUsage 对应 DSH 事件 data.usage 的字段；totalTokens 已包含缓存读写，
// 与 inputTokens+outputTokens+cacheReadTokens 一致（实测样例 3695+654+13312=17661）。
type dshUsage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	TotalTokens      int64 `json:"totalTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
	ReasoningTokens  int64 `json:"reasoningTokens"`
}

func (u dshUsage) normalized() usage {
	input := maxInt64(0, u.InputTokens)
	cached := maxInt64(0, u.CacheReadTokens)
	cacheNew := maxInt64(0, u.CacheWriteTokens)
	output := maxInt64(0, u.OutputTokens)
	reasoning := maxInt64(0, u.ReasoningTokens)
	total := maxInt64(0, u.TotalTokens)
	// 缺失 totalTokens 时用各项求和兜底（DSH total 含缓存读写）。
	if total <= 0 && input+output+cached+cacheNew > 0 {
		total = input + output + cached + cacheNew
	}
	return usage{
		input:     input,
		cached:    cached,
		cacheNew:  cacheNew,
		output:    output,
		reasoning: reasoning,
		total:     total,
	}
}

// readDSHSources 遍历所有 dsh 根目录，按 mtime 只处理今天修改过的会话文件。
func (s *Service) readDSHSources(now time.Time) {
	dayStart := beijingDayStart(now)
	seen := make(map[string]struct{})
	for _, root := range s.cfg.Roots {
		if root.Source != "dsh" {
			continue
		}
		info, err := os.Stat(root.Path)
		if err != nil || !info.IsDir() {
			continue
		}
		s.availableSources["dsh"] = true
		count := 0
		_ = walkDir(root.Path, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if count >= maxFilesPerRoot {
				return filepath.SkipAll
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 ||
				!strings.HasSuffix(strings.ToLower(entry.Name()), dshSuffix) {
				return nil
			}
			count++
			stat, err := entry.Info()
			if err != nil || stat.ModTime().Before(dayStart) {
				return nil
			}
			seen[path] = struct{}{}
			s.readDSHFile(path, now)
			return nil
		})
	}
	for path, state := range s.dsh {
		if _, ok := seen[path]; !ok {
			delete(s.dsh, path)
			if state != nil && state.view != nil {
				delete(s.files, path)
			}
		}
	}
}

// readDSHFile 增量读取一个压缩会话文件：未变化直接跳过，增长时只解压新增 frame，
// 其余情况回退整文件解压。
func (s *Service) readDSHFile(path string, now time.Time) {
	file, err := openTranscript(path)
	if err != nil {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	size := info.Size()
	modMS := info.ModTime().UnixMilli()

	state := s.dsh[path]
	if state == nil {
		state = &dshFileState{}
		s.dsh[path] = state
	}
	if state.view == nil {
		// agentID 与响应记录的 agentID 保持一致，快照才能把实时状态与
		// 实测速率按同一个 agent 聚合。
		project := projectName("dsh", path)
		state.view = &fileState{
			source:  "dsh",
			agentID: "dsh:" + dshSessionID(path),
			project: project,
		}
		s.files[path] = state.view
	}
	if state.view.project != "" {
		s.projectByAgent[state.view.agentID] = state.view.project
	}
	// 快路径：同一文件大小与 mtime 都未变化，说明没有新事件，不再解压。
	if size == state.lastSize && modMS == state.lastModMS {
		return
	}
	state.lastSize = size
	state.lastModMS = modMS

	if size < state.compressedOffset {
		// 文件被截断或重写，游标失效。
		state.compressedOffset = 0
	}
	if state.compressedOffset > 0 && size-state.compressedOffset <= maxReadChunk {
		if err := s.decodeDSHRange(file, path, state, state.compressedOffset, size); err == nil {
			state.compressedOffset = size
			return
		}
		// 新增字节不是独立 frame（文件被重写或上次留下半个 frame），回退整文件。
		state.compressedOffset = 0
	}
	if err := s.decodeDSHRange(file, path, state, 0, size); err != nil {
		// 文件可能正在写入（末尾 frame 不完整），下一轮 Poll 再试。
		return
	}
	state.compressedOffset = size
	_ = now
}

// decodeDSHRange 解压 [offset,end) 压缩区间并把完整行交给 ingestDSH。
// zstd 解码器原生支持连续 frame，因此一次 Reset 可消费多个追加 frame。
func (s *Service) decodeDSHRange(file transcriptFile, path string, state *dshFileState, offset, end int64) error {
	if end <= offset {
		return nil
	}
	decoder := s.dshDecoder()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	// Reset 只重置流状态，不解析数据；坏 frame 的真实错误会在下面 Read 时返回，
	// 因此这里没有可覆盖的错误分支（已删除），读取失败由循环统一回传。
	_ = decoder.dec.Reset(io.LimitReader(file, end-offset))
	reader := bufio.NewReaderSize(decoder.dec, 256<<10)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
				s.ingestDSH(path, state, trimmed)
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// dshDecoder 返回复用的 zstd 解码器。NewReader 只会在选项非法时返回错误，
// 这里的选项是常量组合，不会失败；返回 error 的分支属于永远走不到的防御代码，
// 已删除，保持调用点直白。
func (s *Service) dshDecoder() *dshDecoder {
	if s.dshDec != nil {
		return s.dshDec
	}
	dec, _ := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderLowmem(true))
	s.dshDec = &dshDecoder{dec: dec}
	return s.dshDec
}

// ingestDSH 解析一条解压后的 JSONL 事件；只有带 usage 的 assistant 消息计入总量。
func (s *Service) ingestDSH(path string, state *dshFileState, line []byte) {
	var event dshEvent
	if json.Unmarshal(line, &event) != nil {
		return
	}
	if strings.TrimSpace(event.Cwd) != "" && state != nil && state.view != nil {
		// 会话头里的 cwd 是权威来源：拿到它就覆盖路径猜测的结果，
		// 即便它给不出名字（例如根目录）也不再退回目录名反推。
		name := projectBaseName(event.Cwd)
		state.view.project = name
		state.projectResolved = true
		if name != "" {
			s.projectByAgent[state.view.agentID] = name
		} else {
			delete(s.projectByAgent, state.view.agentID)
		}
		return
	}
	if event.Type == "step/start" && state != nil && event.Time > 0 && event.Data != nil {
		state.stepTurn = event.Data.Turn
		state.stepNum = event.Data.Step
		state.stepMS = event.Time
		// 用户按下发送即视为一次新响应开始：登记请求起点并标记为活跃，
		// 这样 prefill 等待期间也能显示「正在生成」。
		if view := state.view; view != nil {
			view.pendingMS = event.Time
			view.hasPending = true
			view.inFlightMS = event.Time
			view.active = true
			view.streaming = false
			view.lastLineMS = event.Time
		}
		return
	}
	if event.Type == "request/header" && state != nil && event.Data != nil {
		if h := event.Data.Header; h != nil && h.Config != nil {
			if model := strings.TrimSpace(h.Config.Model); model != "" && state.view != nil {
				state.view.model = model
			}
		}
		return
	}
	if event.Type != "assistant/message" || event.Data == nil {
		return
	}
	if event.Data.Message == nil || event.Data.Message.Role != "assistant" {
		return
	}
	usageView := event.Data.Usage
	if usageView == nil {
		usageView = event.Usage
	}
	if usageView == nil {
		return
	}
	normalized := usageView.normalized()
	if normalized.total <= 0 {
		return
	}
	endMS := event.Time
	if endMS <= 0 {
		return
	}
	session := dshSessionID(path)
	id := strings.TrimSpace(event.Data.ID)
	if id == "" {
		if event.Seq > 0 {
			id = strconv.FormatInt(event.Seq, 10)
		} else {
			id = strconv.FormatInt(endMS, 10)
		}
	}
	model := ""
	if event.Data.Source != nil {
		model = strings.TrimSpace(event.Data.Source.Model)
	}
	if state != nil && state.view != nil && state.view.model != "" {
		// request/header 记录的模型比事件自带的更可靠（新版事件不带 source）。
		model = state.view.model
	}
	timing := dshMeasureStream(event.Data.Stream)
	// 请求起点优先用同 turn/step 的 step/start；跨 step 误配时宁可缺省。
	startMS := endMS
	if state != nil && state.stepMS > 0 && state.stepMS < endMS &&
		state.stepTurn == event.Data.Turn && state.stepNum == event.Data.Step {
		startMS = state.stepMS
	} else if timing.firstChunkMS > 0 && timing.firstChunkMS < endMS {
		startMS = timing.firstChunkMS
	}
	measurable := timing.firstChunkMS > 0 && timing.firstChunkMS < endMS &&
		normalized.output > 0
	record := responseRecord{
		key:            "dsh:" + session + ":" + id,
		source:         "dsh",
		agentID:        "dsh:" + session,
		model:          model,
		startMS:        startMS,
		hasStart:       true,
		endMS:          endMS,
		final:          true,
		noMeasuredRate: !measurable,
		usage:          normalized,
		updatedAt:      time.UnixMilli(endMS),
	}
	if measurable {
		record.decodeStartMS = timing.firstChunkMS
		record.hasDecodeStart = true
		record.firstTextMS = timing.firstTextMS
		record.hasFirstText = timing.firstTextMS > 0
		record.firstReasonMS = timing.firstReasonMS
		record.hasFirstReason = timing.firstReasonMS > 0
		if timing.lastChunkMS > timing.firstChunkMS {
			record.flowMS = timing.lastChunkMS - timing.firstChunkMS
		}
	}
	s.upsertRecord(record)
	if view := state.view; state != nil && view != nil {
		// 一条 assistant 消息写完即本次响应结束，回到空闲并推进游标。
		view.active = false
		view.streaming = false
		view.inFlightMS = 0
		view.pendingMS = endMS
		view.hasPending = true
		view.lastLineMS = endMS
		state.messageCount++
	}
}

// dshSessionID 从 .../<sessionDir>/session.v4.jsonl.zstd 提取会话标识；
// 只保留目录名，不把本机路径带进快照。
func dshSessionID(path string) string {
	dir := filepath.Base(filepath.Dir(path))
	dir = strings.TrimPrefix(dir, "session-")
	if dir == "" || dir == "." || dir == string(filepath.Separator) {
		return "unknown"
	}
	return dir
}
