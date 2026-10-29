package adapterwiring

import (
	"context"
	"database/sql"
	"encoding/json"

	"qka/internal/capabilities"
)

// logWriteParams 是适配器 log.write 钩子参数。
type logWriteParams struct {
	Target    string `json:"target"`
	AccountID string `json:"accountId"`
	Operation string `json:"operation"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

// RegisterLogWrite 把适配器 log.write 钩子接线到 logs 表（命名空间由宿主强制）。
func RegisterLogWrite(mux *HookMux, db *sql.DB) {
	mux.On("log.write", func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p logWriteParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		svc := capabilities.NewLogService(db,
			capabilities.GrantFromNeeds([]string{"log.write"}), nil)
		ns := capabilities.Namespace{
			Target: p.Target, AccountID: p.AccountID, Operation: p.Operation,
		}
		if err := svc.Write(ctx, ns, p.Level, p.Message); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]bool{"ok": true})
	})
}
