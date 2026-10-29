package adapterkit

import "testing"

func TestShippedTemplateValidates(t *testing.T) {
	// 工作目录为本包目录，随仓库发布的模板在 ../../adapters/_template。
	if err := Validate("../../adapters/_template"); err != nil {
		t.Fatalf("随仓库发布的模板应通过校验，实际: %v", err)
	}
}
