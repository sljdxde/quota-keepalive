package adapterhost

import (
	"strings"
	"testing"
)

func validManifest() Manifest {
	return Manifest{
		ID:      "qoder-cn",
		Name:    "Qoder CN",
		Version: "1.2.0",
		Entry:   "./index.mjs",
		Capabilities: []string{"accounts.discover", "checkin"},
		Needs:        []string{"http.outbound"},
		Actions: []Action{
			{Op: "checkin"},
		},
	}
}

func TestValidManifestPasses(t *testing.T) {
	m := validManifest()
	if err := m.Validate(); err != nil {
		t.Fatalf("完整 manifest 应通过，实际 %v", err)
	}
}

func TestManifestRequiresCoreFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Manifest)
		want   string
	}{
		{"缺 id", func(m *Manifest) { m.ID = "" }, "id"},
		{"缺 name", func(m *Manifest) { m.Name = "" }, "name"},
		{"缺 version", func(m *Manifest) { m.Version = "" }, "version"},
		{"缺 entry", func(m *Manifest) { m.Entry = "" }, "entry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validManifest()
			tc.mutate(&m)
			err := m.Validate()
			if err == nil {
				t.Fatalf("期望报错，实际通过")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误应指向字段 %q，实际 %v", tc.want, err)
			}
		})
	}
}

func TestManifestRejectsActionWithoutOp(t *testing.T) {
	m := validManifest()
	m.Actions = []Action{{Op: ""}}
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "op") {
		t.Fatalf("缺 op 的 action 应报错，实际 %v", err)
	}
}

func TestManifestRejectsDuplicateActionOp(t *testing.T) {
	m := validManifest()
	m.Actions = []Action{{Op: "checkin"}, {Op: "checkin"}}
	if err := m.Validate(); err == nil {
		t.Fatal("重复 action op 应报错")
	}
}

func TestManifestHasAction(t *testing.T) {
	m := validManifest()
	if !m.HasAction("checkin") {
		t.Error("应声明 checkin")
	}
	if m.HasAction("keepalive") {
		t.Error("不应声明 keepalive")
	}
}
