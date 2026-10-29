package cli

import (
	"encoding/json"
	"io"
)

// Response 是成功路径的稳定输出结构。
type Response struct {
	Status string `json:"status"`
	Data   any    `json:"data,omitempty"`
}

// ErrorInfo 是失败路径的稳定错误结构。
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeSuccess(w io.Writer, resp *Response) error {
	return writeJSON(w, map[string]any{"ok": true, "data": resp})
}

func writeError(w io.Writer, e *ErrorInfo) error {
	return writeJSON(w, map[string]any{"ok": false, "error": e})
}
