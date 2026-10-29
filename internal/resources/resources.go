// Package resources 提供 qka 的只读 MCP resources：今日状态、agents、
// accounts、logs（游标）、宿主集成；不提供 schedule resource。
package resources

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

var (
	// ErrUnknownResource 表示请求了未定义的资源。
	ErrUnknownResource = errors.New("resources: 未知资源（无 schedule resource）")
)

// Provider 由各服务实现，提供只读数据。
type Provider interface {
	StatusToday() (json.RawMessage, error)
	Agents() (json.RawMessage, error)
	Accounts() (json.RawMessage, error)
	Logs(cursor int64) (json.RawMessage, error)
}

// Resource 是一个只读资源描述。
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

// catalog 是五个固定只读资源（无 schedule）。
var catalog = []Resource{
	{URI: "qka://status/today", Name: "今日状态", Description: "今日签到与保活状态", MimeType: "application/json"},
	{URI: "qka://agents", Name: "Agents", Description: "已注册适配器", MimeType: "application/json"},
	{URI: "qka://accounts", Name: "账号", Description: "账号与来源", MimeType: "application/json"},
	{URI: "qka://logs", Name: "日志", Description: "日志（支持 cursor 查询参数）", MimeType: "application/json"},
	{URI: "qka://host-integration", Name: "宿主集成", Description: "宿主侧集成指引", MimeType: "text/plain"},
}

// List 返回五个固定只读资源。
func List() []Resource {
	return catalog
}

// Read 读取一个只读资源；logs 支持 cursor 查询参数。
func Read(ctx context.Context, p Provider, uri string) ([]byte, error) {
	base, query, _ := strings.Cut(uri, "?")

	switch base {
	case "qka://status/today":
		return p.StatusToday()
	case "qka://agents":
		return p.Agents()
	case "qka://accounts":
		return p.Accounts()
	case "qka://logs":
		var cursor int64
		if query != "" {
			params := parseQuery(query)
			if c, ok := params["cursor"]; ok {
				cursor, _ = strconv.ParseInt(c, 10, 64)
			}
		}
		return p.Logs(cursor)
	case "qka://host-integration":
		return []byte(hostIntegrationText), nil
	default:
		return nil, ErrUnknownResource
	}
}

func parseQuery(q string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(q, "&") {
		k, v, ok := strings.Cut(pair, "=")
		if ok {
			out[k] = v
		}
	}
	return out
}

const hostIntegrationText = `宿主侧集成指引：
- qka 不创建调度：请在宿主侧安排每日调用 checkin_run；
- WorkBuddy CN 另需每日 keepalive 调用；
- 宿主内部计划对 qka 不可观察，请在宿主侧核验。`
