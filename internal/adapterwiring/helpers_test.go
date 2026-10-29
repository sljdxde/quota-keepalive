package adapterwiring

import (
	"bytes"
	"net/url"
	"testing"

	"qka/internal/store"
)

func parseURL(raw string) (*url.URL, error) {
	return url.Parse(raw)
}

func storeOpenTemp(t *testing.T) (*store.Store, error) {
	return store.Open(t.TempDir())
}

// containsToken 报告结果 JSON 中是否出现 token 键（发现不得返回 token）。
func containsToken(raw []byte) bool {
	return bytes.Contains(bytes.ToLower(raw), []byte(`"token"`))
}

// containsReadOnly 报告结果中是否标记了 read_only（导入来源）。
func containsReadOnly(raw []byte) bool {
	return bytes.Contains(raw, []byte(`"readOnly":true`))
}
