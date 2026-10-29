package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
)

// ServerDeps 是服务依赖（后续接线 registry、store、各工具实现）。
type ServerDeps struct{}

// Server 是仅绑定 loopback 的 streamable HTTP MCP 服务。
type Server struct {
	tools []toolDef
}

// NewServer 创建 MCP 服务。
func NewServer(_ ServerDeps) *Server {
	return &Server{tools: toolDefinitions()}
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
		result := initializeResult{
			ProtocolVersion: protocolVersion,
			Capabilities:    map[string]any{"tools": map[string]any{}},
			ServerInfo:      serverInfo{Name: "qka", Version: "1.0.0"},
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
		// 05 阶段工具尚未接线：返回稳定显式错误，不崩溃。
		resp.Error = &rpcError{
			Code:    errToolNotImplemented,
			Message: "工具尚未实现: " + params.Name,
		}
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
