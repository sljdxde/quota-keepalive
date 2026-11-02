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
// DSH 事件没有显式的首 token 时间戳（assistant/message 只有结束时间 time 与 usage），
// 所以本数据源与 opencode 一样只贡献今日总量，不给出实测 tok/s：
// record.noMeasuredRate=true，避免用结束时间反推速率这种猜测口径。
const dshSuffix = ".jsonl.zstd"

// dshFileState 记录一个 DSH 压缩文件的解压游标与最近一次 stat 结果。
// lastSize/lastModMS 用于「文件未变化就跳过解压」的快路径；
// compressedOffset 是已完整消费的压缩字节数，用于只解压新追加的 frame。
type dshFileState struct {
	compressedOffset int64
	lastSize         int64
	lastModMS        int64
}

// dshDecoder 复用 zstd 解码器，避免每个 Poll 重复分配窗口内存。
// Poll 期间由 Service.mu 串行保护，无需额外锁。
type dshDecoder struct {
	dec *zstd.Decoder
}

type dshEvent struct {
	Type string   `json:"type"`
	Seq  int64    `json:"seq"`
	Time int64    `json:"time"`
	Data *dshData `json:"data"`
	// 少数版本可能把 usage 放在事件顶层，解析时作为 data.usage 的兜底。
	Usage *dshUsage `json:"usage"`
}

type dshData struct {
	ID      string      `json:"id"`
	Message *dshMessage `json:"message"`
	Source  *dshSource  `json:"source"`
	Usage   *dshUsage   `json:"usage"`
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
	for path := range s.dsh {
		if _, ok := seen[path]; !ok {
			delete(s.dsh, path)
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
		if err := s.decodeDSHRange(file, path, state.compressedOffset, size); err == nil {
			state.compressedOffset = size
			return
		}
		// 新增字节不是独立 frame（文件被重写或上次留下半个 frame），回退整文件。
		state.compressedOffset = 0
	}
	if err := s.decodeDSHRange(file, path, 0, size); err != nil {
		// 文件可能正在写入（末尾 frame 不完整），下一轮 Poll 再试。
		return
	}
	state.compressedOffset = size
	_ = now
}

// decodeDSHRange 解压 [offset,end) 压缩区间并把完整行交给 ingestDSH。
// zstd 解码器原生支持连续 frame，因此一次 Reset 可消费多个追加 frame。
func (s *Service) decodeDSHRange(file transcriptFile, path string, offset, end int64) error {
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
				s.ingestDSH(path, trimmed)
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
func (s *Service) ingestDSH(path string, line []byte) {
	var event dshEvent
	if json.Unmarshal(line, &event) != nil {
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
	// startMS 与 endMS 相同：没有真实首 token 时间，asTimed 会因窗口为 0 跳过速率，
	// 只保留会话/来源的今日总量与最近活跃时间。
	s.upsertRecord(responseRecord{
		key:            "dsh:" + session + ":" + id,
		source:         "dsh",
		agentID:        "dsh:" + session,
		model:          model,
		startMS:        endMS,
		hasStart:       true,
		endMS:          endMS,
		final:          true,
		noMeasuredRate: true,
		usage:          normalized,
		updatedAt:      time.UnixMilli(endMS),
	})
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
