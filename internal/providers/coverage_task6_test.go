package providers

import (
	"context"
	"strings"
	"testing"
)

// TestModelTestAccountLookupErrors 覆盖诊断测试的账号查询错误分支：
// accounts 表不可用时应回传数据库错误，而不是继续外呼。
func TestModelTestAccountLookupErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("指定账号查询失败", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.store.DB().Exec(`DROP TABLE accounts`); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.TestModel(ctx, "fixture", "a1", "model-a"); err == nil {
			t.Fatal("accounts 表缺失时应回传错误")
		}
	})

	t.Run("自动选号后账号查询失败", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.store.DB().Exec(`DROP TABLE accounts`); err != nil {
			t.Fatal(err)
		}
		_, err := f.service.TestModel(ctx, "fixture", "", "model-a")
		if err == nil || strings.Contains(err.Error(), "尚未添加账号") {
			t.Fatalf("自动选号后的查询错误应回传，实际 %v", err)
		}
	})
}
