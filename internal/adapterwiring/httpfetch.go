package adapterwiring

import (
	"context"
	"encoding/json"

	"qka/internal/capabilities"
)

// fetchParams 是适配器 http.fetch 钩子的参数。
type fetchParams struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// fetchResult 是返回给适配器的响应。
type fetchResult struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// RegisterHTTPFetch 把 capabilities.HTTPFetch 注册为 "http.fetch" 钩子。
func RegisterHTTPFetch(mux *HookMux, fetch *capabilities.HTTPFetch) {
	mux.On("http.fetch", func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p fetchParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		resp, err := fetch.Fetch(ctx, capabilities.FetchRequest{
			Method:  p.Method,
			URL:     p.URL,
			Headers: p.Headers,
			Body:    []byte(p.Body),
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(fetchResult{
			Status:  resp.Status,
			Headers: resp.Headers,
			Body:    string(resp.Body),
		})
	})
}
