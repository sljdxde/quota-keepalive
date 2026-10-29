package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
)

// ToolCaller 由组合根注入：执行一个具名工具并返回可 JSON 序列化的结果。
type ToolCaller func(ctx context.Context, name string, args json.RawMessage) (any, error)

// ServerDeps 是服务依赖（组合根注入工具调用与版本）。
type ServerDeps struct {
	// CallTool 执行 tools/call；为 nil 时保持显式 not-implemented 错误。
	CallTool ToolCaller
	// Version 是 initialize/serverInfo 上报的本体版本。
	Version string
}

// Server 是仅绑定 loopback 的 streamable HTTP MCP 服务。
type Server struct {
	tools []toolDef
	deps  ServerDeps
}

// NewServer 创建 MCP 服务。
func NewServer(deps ServerDeps) *Server {
	return &Server{tools: toolDefinitions(), deps: deps}
}

// Handler 返回路由处理器（MCP 端点路径 /mcp）。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handle)
	return mux
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPC(w, rpcResponse{Error: &rpcError{Code: errParseError, Message: "Parse error"}})
		return
	}
	writeRPC(w, s.dispatch(r.Context(), req))
}

func (s *Server) dispatch(ctx context.Context, req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		version := s.deps.Version
		if version == "" {
			version = "1.0.0"
		}
		result := initializeResult{
			ProtocolVersion: protocolVersion,
			Capabilities:    map[string]any{"tools": map[string]any{}},
			ServerInfo:      serverInfo{Name: "QKA", Version: version},
		}
		data, _ := json.Marshal(result)
		resp.Result = data
		return resp

	case "tools/list":
		data, _ := json.Marshal(toolsListResult{Tools: s.tools})
		resp.Result = data
		return resp

	case "tools/call":
		var params toolsCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{Code: errInvalidParams, Message: "Invalid params"}
			return resp
		}
		if s.deps.CallTool == nil {
			// 未注入工具实现：返回稳定显式错误，不崩溃。
			resp.Error = &rpcError{
				Code:    errToolNotImplemented,
				Message: "工具尚未实现: " + params.Name,
			}
			return resp
		}
		out, err := s.deps.CallTool(ctx, params.Name, params.Arguments)
		if err != nil {
			resp.Error = &rpcError{Code: errInternalError, Message: err.Error()}
			return resp
		}
		payload, err := json.Marshal(out)
		if err != nil {
			resp.Error = &rpcError{Code: errInternalError, Message: err.Error()}
			return resp
		}
		data, _ := json.Marshal(toolCallResult{
			Content: []toolTextContent{{Type: "text", Text: string(payload)}},
		})
		resp.Result = data
		return resp

	case "notifications/initialized":
		// 客户端通知，无需结果。
		return resp

	default:
		resp.Error = &rpcError{Code: errMethodNotFound, Message: "Method not found: " + req.Method}
		return resp
	}
}

func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ListenAndServe 在给定地址（应为 127.0.0.1:port）启动服务，返回实际监听的 HTTP 服务。
func (s *Server) ListenAndServe(addr string) (*http.Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	httpSrv := &http.Server{Handler: s.Handler()}
	httpSrv.Addr = ln.Addr().String()
	go func() { _ = httpSrv.Serve(ln) }()
	return httpSrv, nil
}
