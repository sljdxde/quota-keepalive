package pairing

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// 三层校验失败原因（内部使用；对外统一返回 403，不暴露细节）。
var (
	ErrNotLoopback   = errors.New("来源不是 loopback")
	ErrBadHost       = errors.New("Host 头不匹配")
	ErrBadOrigin     = errors.New("Origin 不在 loopback")
	ErrBadCredential = errors.New("配对凭据缺失或无效")
)

// Guard 是端点的三层安全校验中间件。
type Guard struct {
	pairing      *Pairing
	expectedHost string
}

// NewGuard 创建绑定到指定预期 Host（如 127.0.0.1:43189）的校验器。
func NewGuard(p *Pairing, expectedHost string) *Guard {
	return &Guard{pairing: p, expectedHost: expectedHost}
}

// Wrap 返回被保护的处理器。
func (g *Guard) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := g.check(r); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Guard) check(r *http.Request) error {
	// ① 实际远端地址必须是 loopback。
	if !isLoopbackAddr(r.RemoteAddr) {
		return ErrNotLoopback
	}
	// ② Host 头必须匹配预期。
	if !hostMatches(r.Host, g.expectedHost) {
		return ErrBadHost
	}
	// ③ Origin（浏览器会带；非浏览器客户端缺失时按 Host 规则处理）。
	if origin := r.Header.Get("Origin"); origin != "" && !originLoopback(origin) {
		return ErrBadOrigin
	}
	// ④ per-宿主配对凭据。
	hostID := r.Header.Get("X-QKA-Host")
	token := bearerToken(r)
	if token == "" || !g.pairing.Verify(hostID, token) {
		return ErrBadCredential
	}
	return nil
}

func isLoopbackAddr(remoteAddr string) bool {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hostMatches(got, expected string) bool {
	if got == expected {
		return true
	}
	gotHost, gotPort, gotErr := net.SplitHostPort(got)
	expHost, expPort, expErr := net.SplitHostPort(expected)
	if gotErr != nil || expErr != nil || gotPort != expPort {
		return false
	}
	loopbackName := func(h string) bool { return h == "127.0.0.1" || h == "localhost" }
	return loopbackName(gotHost) && loopbackName(expHost)
}

func originLoopback(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost"
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, prefix) {
		return strings.TrimPrefix(h, prefix)
	}
	return ""
}
