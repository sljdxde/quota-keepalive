package registry

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAgentsListJSON(t *testing.T) {
	reg, st := newRegistry(t)
	seedAccount(t, st, "workbuddy-cn", "u_1", true)
	seedSource(t, st, "workbuddy-cn", "u_1", "s_1", "managed", false, "healthy")
	reg.SetAdapters([]LoadedAdapter{{Manifest: sampleManifest()}})

	data, err := reg.AgentsList(context.Background())
	if err != nil {
		t.Fatalf("AgentsList 失败: %v", err)
	}

	var got []AgentDescriptor
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("JSON 无法解析: %v", err)
	}
	if len(got) != 1 || got[0].ID != "workbuddy-cn" {
		t.Errorf("JSON 内容错误: %+v", got)
	}
}

func TestAgentsListEmptyIsArray(t *testing.T) {
	reg, _ := newRegistry(t)
	data, err := reg.AgentsList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "[]" {
		t.Errorf("空列表应序列化为 []，实际 %s", data)
	}
}
