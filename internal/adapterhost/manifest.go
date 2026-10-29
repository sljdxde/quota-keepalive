// Package adapterhost 用随应用打包的 Bun 以子进程运行普通 npm 适配器，经 stdio JSON-RPC 通信。
package adapterhost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Effects 是适配器声明的六个效应位；07 阶段只做结构承载，缺失的保守投影在 08/21 处理。
type Effects struct {
	AccessesNetwork   *bool `json:"accessesNetwork,omitempty"`
	MutatesTarget     *bool `json:"mutatesTarget,omitempty"`
	SpendsCredits     *bool `json:"spendsCredits,omitempty"`
	Idempotent        *bool `json:"idempotent,omitempty"`
	MayRequireReauth  *bool `json:"mayRequireReauth,omitempty"`
	MutatesLocalState *bool `json:"mutatesLocalState,omitempty"`
}

// CostHint 是动作的成本预估（供 plan_validate 与面板展示）。
type CostHint struct {
	Unit   string  `json:"unit,omitempty"`
	Approx float64 `json:"approx,omitempty"`
	Source string  `json:"source,omitempty"`
}

// Action 声明一个可派发操作及其效应。
type Action struct {
	Op       string   `json:"op"`
	Effects  Effects  `json:"effects,omitempty"`
	CostHint CostHint `json:"costHint,omitempty"`
}

// DayRule 定义每日幂等的业务日界。
type DayRule struct {
	TZ string `json:"tz,omitempty"`
}

// Compatibility 声明适配器兼容的 qka 版本范围。
type Compatibility struct {
	QKA string `json:"qka,omitempty"`
}

// Manifest 是适配器的签名清单。
type Manifest struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Version       string        `json:"version"`
	Entry         string        `json:"entry"`
	Capabilities  []string      `json:"capabilities,omitempty"`
	Needs         []string      `json:"needs,omitempty"`
	Actions       []Action      `json:"actions"`
	DayRule       DayRule       `json:"dayRule,omitempty"`
	Compatibility Compatibility `json:"compatibility,omitempty"`
}

// Validate 做基础 schema 校验：核心字段必填、每个 action 必须有 op、op 不重复。
func (m *Manifest) Validate() error {
	if m.ID == "" {
		return fmt.Errorf("manifest 缺少必填字段 id")
	}
	if m.Name == "" {
		return fmt.Errorf("manifest %s 缺少必填字段 name", m.ID)
	}
	if m.Version == "" {
		return fmt.Errorf("manifest %s 缺少必填字段 version", m.ID)
	}
	if m.Entry == "" {
		return fmt.Errorf("manifest %s 缺少必填字段 entry", m.ID)
	}

	seen := map[string]bool{}
	for i, a := range m.Actions {
		if a.Op == "" {
			return fmt.Errorf("manifest %s 的 actions[%d] 缺少 op", m.ID, i)
		}
		if seen[a.Op] {
			return fmt.Errorf("manifest %s 的 action op 重复: %s", m.ID, a.Op)
		}
		seen[a.Op] = true
	}
	return nil
}

// HasAction 报告是否声明了某操作；未声明的操作本体不下发。
func (m *Manifest) HasAction(op string) bool {
	for _, a := range m.Actions {
		if a.Op == op {
			return true
		}
	}
	return false
}

// LoadManifest 读取并解析适配器目录下的 manifest.json 并校验。
func LoadManifest(dir string) (Manifest, error) {
	path := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, err
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}
