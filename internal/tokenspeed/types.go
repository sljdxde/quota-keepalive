// Package tokenspeed measures local coding-agent output throughput from append-only
// transcript files. It never exposes prompts, responses, credentials, or raw headers.
package tokenspeed

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	liveWindow       = 3 * time.Second
	recentReference  = 10 * time.Minute
	recentCount      = 3
	fallback         = 30 * time.Minute
	fallbackCount    = 10
	staleAfter       = 5 * time.Minute
	maxRecordAge     = 35 * time.Minute
	maxReadChunk     = 16 << 20
	maxTranscriptAge = 45 * time.Minute
	maxTokenValue    = int64(1_000_000_000_000)
	deduperSize      = 65536
	sessionVisible   = 10 * time.Minute
	maxSessions      = 12
)

// 初始读取只保留会话文件尾部，避免启动时解析超大历史；尾部之前的历史字节
// 由后台分片补读，用于还原当日 token 总量。变量形式便于测试注入小阈值。
var (
	// maxInitialTail 是首次打开会话文件时实时读取的最大尾部字节数。
	maxInitialTail = int64(8 << 20)
	// backfillChunk 是每次 Poll 为单个文件补读的最大历史字节数。
	backfillChunk = int64(4 << 20)
	// backfillBudget 是每次 Poll 全部文件补读的字节预算。
	backfillBudget = int64(16 << 20)
)

// Root is one transcript root monitored by the service.
type Root struct {
	Source string
	Path   string
}

// Config controls local transcript discovery and deterministic test clocks.
type Config struct {
	Roots        []Root
	PollInterval time.Duration
	Now          func() time.Time
}

// Built-in speed-title themes used by the QKA title icon library.
const (
	TitleThemeVehicles  = "vehicles"
	TitleThemeAnimals   = "animals"
	TitleThemeAthletics = "athletics"
	TitleThemeMusic     = "music"
	TitleThemeNetwork   = "network"
	TitleThemeSound     = "sound"
	TitleThemeStudent   = "student"
	TitleThemeCareer    = "career"
	TitleThemeSpace     = "space"
)

// TitleThemeOption is a selectable built-in title theme.
type TitleThemeOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// TitleThemeDefinition is the complete, credential-free catalog consumed by
// the title picker. It keeps display rules out of the frontend.
type TitleThemeDefinition struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Idle       string    `json:"idle"`
	Thresholds []float64 `json:"thresholds"`
	Titles     []string  `json:"titles"`
}

var titleThemes = map[string]TitleThemeOption{
	TitleThemeVehicles:  {ID: TitleThemeVehicles, Label: "交通工具"},
	TitleThemeAnimals:   {ID: TitleThemeAnimals, Label: "动物速度"},
	TitleThemeAthletics: {ID: TitleThemeAthletics, Label: "田径赛场"},
	TitleThemeMusic:     {ID: TitleThemeMusic, Label: "音乐速度"},
	TitleThemeNetwork:   {ID: TitleThemeNetwork, Label: "网络世代"},
	TitleThemeSound:     {ID: TitleThemeSound, Label: "音速时代"},
	TitleThemeStudent:   {ID: TitleThemeStudent, Label: "学生生涯"},
	TitleThemeCareer:    {ID: TitleThemeCareer, Label: "职场晋升"},
	TitleThemeSpace:     {ID: TitleThemeSpace, Label: "宇宙速度"},
}

// TitleThemeOptions returns the picker order used by the UI.
func TitleThemeOptions() []TitleThemeOption {
	return []TitleThemeOption{
		titleThemes[TitleThemeVehicles],
		titleThemes[TitleThemeAnimals],
		titleThemes[TitleThemeAthletics],
		titleThemes[TitleThemeMusic],
		titleThemes[TitleThemeNetwork],
		titleThemes[TitleThemeSound],
		titleThemes[TitleThemeStudent],
		titleThemes[TitleThemeCareer],
		titleThemes[TitleThemeSpace],
	}
}

// TitleThemeCatalog returns every built-in theme with its full title ladder.
func TitleThemeCatalog() []TitleThemeDefinition {
	options := TitleThemeOptions()
	out := make([]TitleThemeDefinition, 0, len(options))
	for _, option := range options {
		spec := builtInTitles[option.ID]
		out = append(out, TitleThemeDefinition{
			ID:         option.ID,
			Label:      option.Label,
			Idle:       spec.idle,
			Thresholds: append([]float64(nil), titleThresholds[:]...),
			Titles:     append([]string(nil), spec.titles[:]...),
		})
	}
	return out
}

// Snapshot is the credential-free live speed view exposed to the UI.
type Snapshot struct {
	OutputTokensPerSecond float64                `json:"output_tokens_per_second"`
	InputTokensPerSecond  float64                `json:"input_tokens_per_second"`
	TotalTokensPerSecond  float64                `json:"total_tokens_per_second"`
	TodayTotalTokens      int64                  `json:"today_total_tokens"`
	TodayTokenDate        string                 `json:"today_token_date"`
	Active                bool                   `json:"active"`
	GeneratingAgents      int                    `json:"generating_agents"`
	Agent                 string                 `json:"agent,omitempty"`
	Title                 string                 `json:"title"`
	TitleTheme            string                 `json:"title_theme"`
	TitleThemeID          string                 `json:"title_theme_id"`
	TitleCatalog          []TitleThemeDefinition `json:"title_catalog"`
	Sources               []SourceSnapshot       `json:"sources"`
	Sessions              []SessionSnapshot      `json:"sessions"`
	UpdatedAt             string                 `json:"updated_at"`
}

// SourceSnapshot is one agent family's contribution to the live window.
type SourceSnapshot struct {
	Source                string  `json:"source"`
	Label                 string  `json:"label"`
	OutputTokensPerSecond float64 `json:"output_tokens_per_second"`
	InputTokensPerSecond  float64 `json:"input_tokens_per_second"`
	TotalTokensPerSecond  float64 `json:"total_tokens_per_second"`
	GeneratingAgents      int     `json:"generating_agents"`
	TodayInputTokens      int64   `json:"today_input_tokens"`
	TodayOutputTokens     int64   `json:"today_output_tokens"`
	TodayTotalTokens      int64   `json:"today_total_tokens"`
}

// SessionSnapshot is one local agent transcript's safe, credential-free view.
// The opaque ID is derived from the local file identity and never contains a path.
type SessionSnapshot struct {
	ID                    string  `json:"id"`
	Label                 string  `json:"label"`
	Source                string  `json:"source"`
	SourceLabel           string  `json:"source_label"`
	Model                 string  `json:"model,omitempty"`
	OutputTokensPerSecond float64 `json:"output_tokens_per_second"`
	InputTokensPerSecond  float64 `json:"input_tokens_per_second"`
	TotalTokensPerSecond  float64 `json:"total_tokens_per_second"`
	Generating            bool    `json:"generating"`
	LastActiveAt          string  `json:"last_active_at,omitempty"`
}

// DefaultConfig discovers local coding-agent transcript roots.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	roots := make([]Root, 0, 8)
	seen := make(map[string]struct{})
	// 调用方只会传入非空 Join 结果；唯一需要防御的是同一路径被两个来源
	// 注册（例如 QODER_CONFIG_DIR 与 QODERCN_CONFIG_DIR 指向同一目录）。
	add := func(source, path string) {
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		roots = append(roots, Root{Source: source, Path: path})
	}
	if root := os.Getenv("CODEX_HOME"); root != "" {
		add("codex", filepath.Join(root, "sessions"))
	} else if home != "" {
		add("codex", filepath.Join(home, ".codex", "sessions"))
	}
	if root := os.Getenv("CLAUDE_CONFIG_DIR"); root != "" {
		add("claude", filepath.Join(root, "projects"))
	} else if home != "" {
		add("claude", filepath.Join(home, ".claude", "projects"))
	}
	if root := os.Getenv("QODER_CONFIG_DIR"); root != "" {
		add("qoder", filepath.Join(root, "projects"))
	} else if home != "" {
		add("qoder", filepath.Join(home, ".qoder", "projects"))
	}
	if root := os.Getenv("QODERCN_CONFIG_DIR"); root != "" {
		add("qoder-cn", filepath.Join(root, "projects"))
	} else if home != "" {
		add("qoder-cn", filepath.Join(home, ".qoder-cn", "projects"))
	}
	if root := os.Getenv("WORKBUDDY_CONFIG_DIR"); root != "" {
		add("workbuddy", filepath.Join(root, "projects"))
	} else if home != "" {
		add("workbuddy", filepath.Join(home, ".workbuddy", "projects"))
	}
	if root := os.Getenv("CODEBUDDY_CONFIG_DIR"); root != "" {
		add("codebuddy", filepath.Join(root, "projects"))
	} else if home != "" {
		add("codebuddy", filepath.Join(home, ".codebuddy", "projects"))
	}
	if root := os.Getenv("DSH_HOME"); root != "" {
		add("dsh", filepath.Join(root, "sessions"))
	} else if home != "" {
		add("dsh", filepath.Join(home, ".dsh", "sessions"))
	}
	if home != "" {
		add("zcode", filepath.Join(home, ".zcode", "cli", "db", "db.sqlite"))
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" && home != "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	if dataHome != "" {
		add("opencode", filepath.Join(dataHome, "opencode", "opencode.db"))
	}
	return Config{
		Roots:        roots,
		PollInterval: 750 * time.Millisecond,
		Now:          time.Now,
	}
}

// FormatSpeed mirrors Agent Speed's compact menu-bar number formatting.
func FormatSpeed(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return "0"
	}
	if value < 10 {
		rounded := math.Round(value*10) / 10
		if rounded >= 10 {
			return "10"
		}
		return strconv.FormatFloat(rounded, 'f', 1, 64)
	}
	if value < 1000 {
		rounded := math.Round(value)
		if rounded >= 1000 {
			return "1.00k"
		}
		return strconv.FormatInt(int64(rounded), 10)
	}
	return compact(value)
}

type themeTitles struct {
	idle string
	// The 13 active rungs share Agent Speed's redline-relative thresholds.
	titles [13]string
}

var builtInTitles = map[string]themeTitles{
	TitleThemeVehicles: {
		idle: "待机",
		titles: [13]string{
			"步行", "自行车", "电动车", "摩托车", "汽车", "跑车", "高铁",
			"赛车", "磁悬浮列车", "喷气客机", "战斗机", "高超音速飞机", "火箭",
		},
	},
	TitleThemeAnimals: {
		idle: "打盹",
		titles: [13]string{
			"蜗牛", "乌龟", "刺猬", "老鼠", "松鼠", "猫", "兔子",
			"袋鼠", "马", "猎豹", "蝙蝠", "金雕", "游隼",
		},
	},
	TitleThemeAthletics: {
		idle: "热身",
		titles: [13]string{
			"慢走", "快走", "慢跑", "配速跑", "冲刺", "接力", "校纪录",
			"市纪录", "省纪录", "全国纪录", "亚洲纪录", "世界纪录", "人类极限",
		},
	},
	TitleThemeMusic: {
		idle: "静音",
		titles: [13]string{
			"极缓板", "庄板", "广板", "小广板", "柔板", "行板", "中板",
			"小快板", "快板", "活泼板", "急板", "最急板", "超频爆速",
		},
	},
	TitleThemeNetwork: {
		idle: "离线",
		titles: [13]string{
			"拨号", "ISDN", "ADSL", "3G", "HSPA+", "4G", "4G+",
			"千兆光纤", "5G", "5G-A", "万兆光纤", "6G", "数据中心互联",
		},
	},
	TitleThemeSound: {
		idle: "静止",
		titles: [13]string{
			"自行车", "汽车", "高铁", "客机", "亚音速", "跨音速", "音速",
			"超音速", "2 马赫", "3 马赫", "5 马赫", "高超音速", "再入大气层",
		},
	},
	TitleThemeStudent: {
		idle: "放假",
		titles: [13]string{
			"幼儿园", "小学", "初中", "高中", "本科", "硕士", "博士",
			"博士后", "讲师", "副教授", "教授", "院士", "诺奖级",
		},
	},
	TitleThemeCareer: {
		idle: "下班",
		titles: [13]string{
			"实习生", "助理", "专员", "资深专员", "主管", "经理", "高级经理",
			"总监", "副总裁", "总裁", "董事长", "商业传奇", "行业缔造者",
		},
	},
	TitleThemeSpace: {
		idle: "休眠",
		titles: [13]string{
			"星尘", "流星", "彗星", "月球", "行星", "恒星", "红巨星",
			"超新星", "中子星", "黑洞", "星系", "类星体", "大爆炸",
		},
	},
}

var titleThresholds = [13]float64{
	0.5, 8, 12, 16, 24, 32, 48, 68, 96, 136, 200, 280, 400,
}

// NormalizeTitleTheme maps unknown or empty IDs to the default theme.
func NormalizeTitleTheme(theme string) string {
	if _, ok := builtInTitles[theme]; ok {
		return theme
	}
	switch theme {
	case "wind", "cultivation", "wuxia", "mage", "gameRank", "engineer":
		return TitleThemeVehicles
	}
	return TitleThemeVehicles
}

// TitleThemeLabel returns the user-facing name of a built-in theme.
func TitleThemeLabel(theme string) string {
	return titleThemes[NormalizeTitleTheme(theme)].Label
}

// ThemeTitle returns the title for the selected built-in Agent Speed theme.
func ThemeTitle(theme string, value float64, active bool) string {
	spec := builtInTitles[NormalizeTitleTheme(theme)]
	if !active || value < 0.5 {
		return spec.idle
	}
	for index := len(titleThresholds) - 1; index >= 0; index-- {
		if value >= titleThresholds[index] {
			return spec.titles[index]
		}
	}
	return spec.titles[0]
}

// TransportTitle returns the default traffic-vehicle title.
func TransportTitle(value float64, active bool) string {
	return ThemeTitle(TitleThemeVehicles, value, active)
}

func sessionID(agentID string) string {
	sum := sha256.Sum256([]byte(agentID))
	return hex.EncodeToString(sum[:])[:8]
}

func sessionLabel(agentID string) string {
	return "会话 " + sessionID(agentID)
}

func compact(value float64) string {
	thousands := value / 1000
	if round(thousands, 2) < 10 {
		return fixed(thousands, 2) + "k"
	}
	if round(thousands, 1) < 100 {
		return fixed(thousands, 1) + "k"
	}
	if math.Round(thousands) < 1000 {
		return strconv.FormatInt(int64(math.Round(thousands)), 10) + "k"
	}
	millions := value / 1_000_000
	if round(millions, 2) < 10 {
		return fixed(millions, 2) + "M"
	}
	if round(millions, 1) < 100 {
		return fixed(millions, 1) + "M"
	}
	if math.Round(millions) < 1000 {
		return strconv.FormatInt(int64(math.Round(millions)), 10) + "M"
	}
	billions := value / 1_000_000_000
	if round(billions, 2) < 10 {
		return fixed(billions, 2) + "B"
	}
	if round(billions, 1) < 100 {
		return fixed(billions, 1) + "B"
	}
	return strconv.FormatInt(int64(math.Round(billions)), 10) + "B"
}

func round(value float64, digits int) float64 {
	scale := math.Pow(10, float64(digits))
	return math.Round(value*scale) / scale
}

func fixed(value float64, digits int) string {
	return strconv.FormatFloat(value, 'f', digits, 64)
}

func sourceLabel(source string) string {
	switch source {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	case "qoder":
		return "Qoder"
	case "qoder-cn":
		return "Qoder CN"
	case "workbuddy":
		return "WorkBuddy"
	case "codebuddy":
		return "CodeBuddy"
	case "opencode":
		return "OpenCode"
	case "zcode":
		return "ZCode"
	case "dsh":
		return "DeepSeek Harness"
	default:
		return source
	}
}

func clampTokens(value float64) int64 {
	if math.IsNaN(value) || value <= 0 {
		return 0
	}
	if math.IsInf(value, 1) || value >= float64(maxTokenValue) {
		return maxTokenValue
	}
	return int64(value)
}
