package tokenspeed

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type usage struct {
	input     int64
	cached    int64
	cacheNew  int64
	output    int64
	reasoning int64
	total     int64
}

type responseRecord struct {
	key            string
	source         string
	agentID        string
	model          string
	startMS        int64
	hasStart       bool
	decodeStartMS  int64
	hasDecodeStart bool
	firstTextMS    int64
	hasFirstText   bool
	firstReasonMS  int64
	hasFirstReason bool
	flowMS         int64
	noMeasuredRate bool
	endMS          int64
	final          bool
	usage          usage
	updatedAt      time.Time
	project        string
}

type fileState struct {
	source        string
	agentID       string
	offset        int64
	createdMS     int64
	subagentOrd   *int64
	pendingMS     int64
	hasPending    bool
	inFlightMS    int64
	active        bool
	streaming     bool
	firstOutputMS int64
	firstTextMS   int64
	firstReasonMS int64
	lastContentMS int64
	modernUsage   bool
	lastLineMS    int64
	model         string
	project       string
	prevTotals    *usage
	claude        claudeFileState
	workbuddy     workbuddyFileState
	// backfill 非空时表示正在补读实时尾部之前的历史字节。首次打开超大会话
	// 文件时实时读取只保留尾部，历史补读用于还原当日 token 总量；它使用独立
	// 的 fileState，避免历史顺序打乱实时计时状态。
	backfill *backfillState
}

// backfillState 是一次历史补读的游标；offset/end 是文件字节区间 [offset,end)。
type backfillState struct {
	offset int64
	end    int64
	state  *fileState
}

func resetOutputTiming(state *fileState) {
	state.firstOutputMS = 0
	state.firstTextMS = 0
	state.firstReasonMS = 0
	state.lastContentMS = 0
}

func markOutputTiming(state *fileState, ts int64, text, reasoning bool) {
	if state.firstOutputMS == 0 {
		state.firstOutputMS = ts
	}
	if text && state.firstTextMS == 0 {
		state.firstTextMS = ts
	}
	if reasoning && state.firstReasonMS == 0 {
		state.firstReasonMS = ts
	}
	if ts > state.lastContentMS {
		state.lastContentMS = ts
	}
}

type dedupClaim int

const (
	dedupNew dedupClaim = iota
	dedupSameFile
	dedupOtherFile
)

type transcriptFile interface {
	io.Reader
	io.Seeker
	Stat() (os.FileInfo, error)
	Close() error
}

var (
	openTranscript  = func(path string) (transcriptFile, error) { return os.Open(path) }
	maxFilesPerRoot = 20000
	walkDir         = filepath.WalkDir
)

// Service incrementally reads transcript files and maintains a live speed snapshot.
type Service struct {
	cfg Config

	mu                 sync.RWMutex
	files              map[string]*fileState
	records            map[string]responseRecord
	deduper            map[string]string
	dedupRing          []string
	titleTheme         string
	todayDate          string
	todayTotal         int64
	todayContributions map[string]usage
	todayBySource      map[string]usage
	availableSources   map[string]bool
	opencode           map[string]*opencodeSourceState
	// projectByAgent 记住每个 agent 的项目名，供会话行展示（不使用提示词内容）。
	projectByAgent map[string]string
	dsh            map[string]*dshFileState
	dshDec         *dshDecoder
	snapshot       Snapshot

	started bool
	done    chan struct{}
}

// New creates a service. Call Start to begin polling.
func New(cfg Config) *Service {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 750 * time.Millisecond
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	now := cfg.Now()
	todayDate := beijingDay(now)
	return &Service{
		cfg:                cfg,
		files:              make(map[string]*fileState),
		records:            make(map[string]responseRecord),
		deduper:            make(map[string]string),
		titleTheme:         TitleThemeVehicles,
		todayDate:          todayDate,
		todayContributions: make(map[string]usage),
		todayBySource:      make(map[string]usage),
		availableSources:   make(map[string]bool),
		projectByAgent:     make(map[string]string),
		opencode:           make(map[string]*opencodeSourceState),
		dsh:                make(map[string]*dshFileState),
		snapshot: Snapshot{
			TitleTheme:     TitleThemeLabel(TitleThemeVehicles),
			TitleThemeID:   TitleThemeVehicles,
			TodayTokenDate: todayDate,
			Sources:        []SourceSnapshot{},
			Sessions:       []SessionSnapshot{},
			UpdatedAt:      now.Format(time.RFC3339Nano),
		},
	}
}

// Start polls immediately and then at the configured interval until ctx is cancelled.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.done = make(chan struct{})
	done := s.done
	s.mu.Unlock()

	s.Poll()
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.cfg.PollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.Poll()
			}
		}
	}()
}

// Wait blocks until the polling goroutine exits. It is safe to call after Start.
func (s *Service) Wait() {
	s.mu.RLock()
	done := s.done
	started := s.started
	s.mu.RUnlock()
	if started && done != nil {
		<-done
	}
}

// Snapshot returns a copy of the latest credential-free view.
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.snapshot
	out.Sources = append([]SourceSnapshot(nil), s.snapshot.Sources...)
	out.Sessions = append([]SessionSnapshot(nil), s.snapshot.Sessions...)
	return out
}

// SetTitleTheme switches the built-in title theme and refreshes the derived snapshot.
func (s *Service) SetTitleTheme(theme string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.titleTheme = NormalizeTitleTheme(theme)
	s.snapshot = s.computeSnapshot(s.cfg.Now())
}

// Poll performs one discovery, incremental read, and recomputation pass.
func (s *Service) Poll() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.cfg.Now()
	s.ensureToday(now)
	s.discoverAndRead(now)
	s.readBackfills()
	s.readOpenCodeSources(now)
	s.readDSHSources(now)
	s.prune(now)
	s.snapshot = s.computeSnapshot(now)
}

func (s *Service) discoverAndRead(now time.Time) {
	seen := make(map[string]struct{})
	s.availableSources = make(map[string]bool)
	dayStart := beijingDayStart(now)
	for _, root := range s.cfg.Roots {
		if root.Source == "opencode" || root.Source == "dsh" {
			continue
		}
		info, err := os.Stat(root.Path)
		if err != nil || !info.IsDir() {
			continue
		}
		s.availableSources[root.Source] = true
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
			if entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
				return nil
			}
			count++
			stat, err := entry.Info()
			if err != nil || stat.ModTime().Before(dayStart) {
				return nil
			}
			seen[path] = struct{}{}
			s.readFile(path, root.Source, now)
			return nil
		})
	}
	for path := range s.files {
		if _, ok := seen[path]; !ok {
			delete(s.files, path)
		}
	}
}

func (s *Service) readFile(path, source string, now time.Time) {
	file, err := openTranscript(path)
	if err != nil {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	state := s.files[path]
	fresh := state == nil
	if fresh || info.Size() < state.offset {
		state = &fileState{source: source, agentID: path,
			project: projectName(source, path)}
		s.files[path] = state
		fresh = true
	}
	if state.project != "" {
		s.projectByAgent[state.agentID] = state.project
	}

	offset := state.offset
	if offset == 0 && info.Size() > maxInitialTail {
		offset = alignToNextLine(file, info.Size()-maxInitialTail)
		state.offset = offset
	}
	if fresh && offset > 0 {
		// 首次打开超大会话文件时只读尾部，历史区间通过 readBackfills 分片
		// 补读，避免启动时解析数百 MB 会话阻塞实时速率；当日总量在补读
		// 完成后收敛。
		state.backfill = &backfillState{
			end:   offset,
			state: &fileState{source: source, agentID: path},
		}
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return
	}
	remaining := info.Size() - offset
	if remaining <= 0 {
		return
	}
	if remaining > maxReadChunk {
		remaining = maxReadChunk
	}
	data := make([]byte, remaining)
	n, err := io.ReadFull(file, data)
	if err != nil && err != io.ErrUnexpectedEOF {
		return
	}
	data = data[:n]
	consumed, skippedOverlong := processLines(data, func(line []byte) {
		s.ingest(source, path, state, line)
	})
	state.offset += int64(consumed)
	if skippedOverlong > 0 {
		state.offset += int64(skippedOverlong)
	}
	_ = now
}

func alignToNextLine(file io.ReadSeeker, offset int64) int64 {
	if offset <= 0 {
		return 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return offset
	}
	buf := make([]byte, 64<<10)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			if index := strings.IndexByte(string(buf[:n]), '\n'); index >= 0 {
				return offset + int64(index+1)
			}
			offset += int64(n)
		}
		if err != nil {
			return offset
		}
	}
}

// processLines returns complete bytes consumed and the bytes skipped for one
// overlong line that exceeded the read chunk without a newline.
func processLines(data []byte, consume func([]byte)) (int, int) {
	start := 0
	for i, b := range data {
		if b == '\n' {
			line := data[start:i]
			if len(line) > 0 {
				consume(line)
			}
			start = i + 1
		}
	}
	if start == 0 && len(data) == maxReadChunk {
		return 0, len(data)
	}
	return start, 0
}

// readBackfills advances bounded historical reads for files whose live offset
// starts past the beginning of the file. Polling keeps the total work per pass
// bounded so the UI snapshot never waits on a whole-file parse.
func (s *Service) readBackfills() {
	budget := backfillBudget
	for path, state := range s.files {
		if state.backfill == nil {
			continue
		}
		if budget <= 0 {
			return
		}
		budget -= s.readBackfill(path, state, budget)
	}
}

// readBackfill reads at most one bounded chunk of the not-yet-read history and
// feeds complete lines through the normal per-source parser with a dedicated
// fileState. It returns the bytes consumed from the backfill budget.
func (s *Service) readBackfill(path string, state *fileState, budget int64) int64 {
	backfill := state.backfill
	if backfill == nil {
		return 0
	}
	remaining := backfill.end - backfill.offset
	if remaining <= 0 {
		state.backfill = nil
		return 0
	}
	limit := backfillChunk
	if limit > remaining {
		limit = remaining
	}
	if limit > budget {
		limit = budget
	}
	if limit <= 0 {
		return 0
	}

	file, err := openTranscript(path)
	if err != nil {
		state.backfill = nil
		return 0
	}
	defer file.Close()
	if _, err := file.Seek(backfill.offset, io.SeekStart); err != nil {
		state.backfill = nil
		return 0
	}
	data := make([]byte, limit)
	n, err := io.ReadFull(file, data)
	if err != nil && err != io.ErrUnexpectedEOF {
		state.backfill = nil
		return 0
	}
	data = data[:n]

	consumed := int64(0)
	if index := bytes.LastIndexByte(data, '\n'); index >= 0 {
		consumed = int64(index + 1)
		processLines(data[:consumed], func(line []byte) {
			s.ingest(state.source, path, backfill.state, line)
		})
	} else {
		// 整块都没有换行：按超长行跳过，保证补读一定前进。
		consumed = int64(n)
	}
	backfill.offset += consumed
	if backfill.offset >= backfill.end {
		state.backfill = nil
	}
	return consumed
}

func (s *Service) claim(key, owner string) dedupClaim {
	existing, ok := s.deduper[key]
	if ok {
		if existing == owner {
			return dedupSameFile
		}
		return dedupOtherFile
	}
	s.deduper[key] = owner
	s.dedupRing = append(s.dedupRing, key)
	if len(s.dedupRing) > deduperSize {
		evict := len(s.dedupRing) - deduperSize
		for _, old := range s.dedupRing[:evict] {
			delete(s.deduper, old)
		}
		s.dedupRing = append([]string(nil), s.dedupRing[evict:]...)
	}
	return dedupNew
}

func (s *Service) upsertRecord(record responseRecord) {
	if record.endMS <= 0 {
		return
	}
	if record.project == "" {
		record.project = s.projectByAgent[record.agentID]
	}
	s.records[record.key] = record
	s.trackTodayUsage(record)
}

func (s *Service) ensureToday(now time.Time) {
	date := beijingDay(now)
	if date == s.todayDate {
		return
	}
	s.todayDate = date
	s.todayTotal = 0
	s.todayContributions = make(map[string]usage)
	s.todayBySource = make(map[string]usage)
}

func (s *Service) trackTodayUsage(record responseRecord) {
	if beijingDay(time.UnixMilli(record.endMS)) != s.todayDate {
		return
	}
	contribution := nonNegativeUsage(record.usage)
	previous := s.todayContributions[record.key]
	if contribution == previous {
		return
	}
	s.todayContributions[record.key] = contribution
	delta := subtractNonNegative(contribution, previous)
	s.todayTotal += delta.total
	source := record.source
	if source == "" {
		source = "unknown"
	}
	total := s.todayBySource[source]
	total.input += delta.input
	total.output += delta.output
	total.total += delta.total
	s.todayBySource[source] = total
}

func nonNegativeUsage(value usage) usage {
	return usage{
		input:     maxInt64(0, value.input),
		cached:    maxInt64(0, value.cached),
		cacheNew:  maxInt64(0, value.cacheNew),
		output:    maxInt64(0, value.output),
		reasoning: maxInt64(0, value.reasoning),
		total:     maxInt64(0, value.total),
	}
}

func subtractNonNegative(current, previous usage) usage {
	return usage{
		input:     maxInt64(0, current.input-previous.input),
		cached:    maxInt64(0, current.cached-previous.cached),
		cacheNew:  maxInt64(0, current.cacheNew-previous.cacheNew),
		output:    maxInt64(0, current.output-previous.output),
		reasoning: maxInt64(0, current.reasoning-previous.reasoning),
		total:     maxInt64(0, current.total-previous.total),
	}
}

var beijingZone = time.FixedZone("Asia/Shanghai", 8*60*60)

func beijingDay(now time.Time) string {
	return now.In(beijingZone).Format("2006-01-02")
}

func beijingDayStart(now time.Time) time.Time {
	local := now.In(beijingZone)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, beijingZone)
}

func (s *Service) prune(now time.Time) {
	cutoff := now.Add(-maxRecordAge).UnixMilli()
	for key, record := range s.records {
		if record.endMS < cutoff {
			delete(s.records, key)
		}
	}
}

func sortedRecent(records []responseRecord) []responseRecord {
	sort.Slice(records, func(i, j int) bool {
		return records[i].endMS > records[j].endMS
	})
	return records
}
