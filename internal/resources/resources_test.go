package resources

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeProvider struct {
	logsCursor int64
}

func (f *fakeProvider) StatusToday() (json.RawMessage, error) {
	return json.RawMessage(`{"today":"ok"}`), nil
}
func (f *fakeProvider) Agents() (json.RawMessage, error) {
	return json.RawMessage(`[{"id":"a"}]`), nil
}
func (f *fakeProvider) Accounts() (json.RawMessage, error) {
	return json.RawMessage(`[{"id":"u1"}]`), nil
}
func (f *fakeProvider) Logs(cursor int64) (json.RawMessage, error) {
	f.logsCursor = cursor
	return json.RawMessage(`{"entries":[]}`), nil
}

func TestListFiveResourcesNoSchedule(t *testing.T) {
	got := List()
	if len(got) != 5 {
		t.Fatalf("应恰好有 5 个只读资源，实际 %d", len(got))
	}
	for _, r := range got {
		if strings.Contains(r.URI, "schedule") {
			t.Errorf("不应存在 schedule resource，实际 %s", r.URI)
		}
	}
}

func TestReadRoutes(t *testing.T) {
	p := &fakeProvider{}
	ctx := context.Background()

	cases := map[string]string{
		"qka://status/today":     `{"today":"ok"}`,
		"qka://agents":           `[{"id":"a"}]`,
		"qka://accounts":         `[{"id":"u1"}]`,
		"qka://host-integration": "",
	}
	for uri, want := range cases {
		data, err := Read(ctx, p, uri)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", uri, err)
		}
		if want != "" && string(data) != want {
			t.Errorf("资源 %s 内容不符，实际 %s", uri, data)
		}
	}
}

func TestLogsCursor(t *testing.T) {
	p := &fakeProvider{}
	if _, err := Read(context.Background(), p, "qka://logs?cursor=42"); err != nil {
		t.Fatal(err)
	}
	if p.logsCursor != 42 {
		t.Errorf("logs 应解析 cursor=42，实际 %d", p.logsCursor)
	}
}

func TestScheduleResourceRejected(t *testing.T) {
	p := &fakeProvider{}
	if _, err := Read(context.Background(), p, "qka://schedule"); err != ErrUnknownResource {
		t.Fatalf("schedule resource 应被拒绝，实际 %v", err)
	}
}
