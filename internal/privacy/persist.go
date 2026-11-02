package privacy

import (
	"encoding/json"
	"os"
	"sort"

	"qka/internal/atomicfile"
)

type stateFile struct {
	Disabled []string `json:"disabled"`
}

// Save 把开关状态原子写入 path（0600）。
func Save(gate *Gate, path string) error {
	gate.mu.Lock()
	var disabled []string
	for t := range gate.disabled {
		if gate.disabled[t] {
			disabled = append(disabled, t)
		}
	}
	gate.mu.Unlock()
	sort.Strings(disabled)

	data, _ := json.Marshal(stateFile{Disabled: disabled})
	return atomicfile.WriteFile(path, data, 0600)
}

// Load 从 path 读取开关状态；文件不存在返回全启用的门。
func Load(path string) (*Gate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewGate(), nil
		}
		return nil, err
	}
	var st stateFile
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	gate := NewGate()
	for _, t := range st.Disabled {
		gate.disabled[t] = true
	}
	return gate, nil
}
