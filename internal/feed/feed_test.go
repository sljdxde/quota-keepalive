package feed

import (
	"encoding/json"
	"testing"
)

func TestListEmptyUnimplemented(t *testing.T) {
	r := List()
	if r.Status != "unimplemented" {
		t.Errorf("feed_list 状态应恒为 unimplemented，实际 %s", r.Status)
	}
	if len(r.Entries) != 0 {
		t.Errorf("feed_list 应固定返回空集，实际 %d 条", len(r.Entries))
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"status":"unimplemented","entries":[]}` {
		t.Errorf("feed_list 序列化应为稳定空集，实际 %s", data)
	}
}
