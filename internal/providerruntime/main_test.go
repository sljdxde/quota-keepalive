package providerruntime

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("QKA_PROVIDERRUNTIME_HELPER") == "1" {
		os.Exit(runFakeHost())
	}
	os.Exit(m.Run())
}

func runFakeHost() int {
	if os.Getenv("HELPER_EXIT_INIT") == "1" {
		return 3
	}
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	for scanner.Scan() {
		var request struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			continue
		}
		writeFakeResponse(writer, request.ID, request.Method, request.Params)
		_ = writer.Flush()
	}
	return 0
}

func writeFakeResponse(w *bufio.Writer, id int64, method string, params map[string]any) {
	if os.Getenv("HELPER_HANG_ALL") == "1" && method != "abort" && method != "init" {
		return
	}
	respond := func(value any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "result": value})
	}
	switch method {
	case "init":
		if capture := os.Getenv("HELPER_CAPTURE"); capture != "" {
			data, _ := json.Marshal(params)
			_ = os.WriteFile(capture, data, 0o600)
		}
		plugins := []any{}
		if os.Getenv("HELPER_PLUGIN_ERROR") == "1" {
			plugins = append(plugins, map[string]any{
				"spec": "broken-auth", "error": "manifest failed",
			})
		}
		respond(map[string]any{"plugins": plugins})
	case "providers":
		if os.Getenv("HELPER_EVENT_THEN_RESULT") == "1" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": id, "event": "progress", "data": "loading",
			})
		}
		if os.Getenv("HELPER_BAD_PROVIDERS") == "1" {
			_, _ = w.WriteString("{not-json}\n")
			return
		}
		respond([]any{map[string]any{
			"id": "fixture", "spec": "@magpie-community/opencode-fixture-auth",
			"name": "Fixture", "icon": "data:image/png;base64,AA==",
			"usage": true, "checkin": true, "signedIn": true, "authType": "oauth",
			"methods": []any{
				map[string]any{"type": "oauth", "label": "浏览器"},
				map[string]any{"type": "api", "label": "API Key"},
			},
			"models": []any{map[string]any{
				"id": "fixture-model", "name": "Fixture Model",
				"npm": "@fixture/sdk", "context": 4096,
			}},
			"accounts": []any{map[string]any{
				"key": "a1", "type": "oauth", "accountId": "user@example.com",
				"hint": "hint", "models": []string{"fixture-model"},
			}},
		}})
	case "prompt":
		if os.Getenv("HELPER_PROMPT") == "1" {
			respond(map[string]any{"prompt": map[string]any{
				"type": "text", "key": "phone", "message": "手机号",
				"placeholder": "请输入手机号",
			}})
			return
		}
		respond(map[string]any{"prompt": nil})
	case "authorize":
		respond(map[string]any{
			"session": "session-1", "url": "https://login.example",
			"instructions": "在浏览器完成授权", "method": "browser", "opened": true,
		})
	case "callback":
		if os.Getenv("HELPER_FAIL_LOGIN") == "1" {
			respond(map[string]any{"ok": false, "error": "bad code"})
			return
		}
		respond(map[string]any{
			"ok": true, "provider": "fixture", "account": "a1",
		})
	case "apiKey":
		if os.Getenv("HELPER_FAIL_LOGIN") == "1" {
			respond(map[string]any{"ok": false, "error": "bad key"})
			return
		}
		respond(map[string]any{
			"ok": true, "provider": "fixture", "account": "a1",
		})
	case "usage":
		balance := 12.5
		respond(map[string]any{
			"plan": "Pro", "until": "2026-12-01", "balance": "12.5",
			"windows": []any{map[string]any{
				"name": "daily", "used": 3.5, "resetsAt": "2026-11-01",
				"display": "3.5 / 16", "amount": 16, "limit": 16,
				"unit": "requests", "balance": balance,
			}},
		})
	case "checkin":
		respond(map[string]any{
			"outcome": "claimed", "credit": 100, "streak": 2,
			"message": "签到成功", "until": "2026-11-01",
		})
	case "load":
		respond(map[string]any{
			"baseURL": "https://provider.example/v1", "apiKey": "secret",
			"headers": map[string]string{"x-test": "yes"}, "fetch": true,
		})
	case "signOut":
		respond(nil)
	case "fetch":
		if capture := os.Getenv("HELPER_CAPTURE_FETCH"); capture != "" {
			data, _ := json.Marshal(params)
			_ = os.WriteFile(capture, data, 0o600)
		}
		if os.Getenv("HELPER_FETCH_HANG") == "1" {
			return
		}
		if os.Getenv("HELPER_FETCH_ERROR") == "1" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": id, "error": map[string]string{"message": "fetch denied"},
			})
			return
		}
		if os.Getenv("HELPER_FETCH_OVERSIZED") == "1" {
			payload := make([]byte, (4<<20)+1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": id, "event": "chunk",
				"data": base64.StdEncoding.EncodeToString(payload),
			})
			return
		}
		payload := []byte(`{"choices":[{"message":{"content":"OK"}}]}`)
		if os.Getenv("HELPER_FETCH_BODY") == "empty" {
			payload = []byte(`{}`)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "event": "head", "status": 200,
			"headers": map[string]string{"content-type": "application/json"},
		})
		chunk := base64.StdEncoding.EncodeToString(payload)
		if os.Getenv("HELPER_INVALID_CHUNK") == "1" {
			chunk = "%%%"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "event": "chunk", "data": chunk,
		})
		respond(nil)
	case "abort":
		respond(nil)
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "error": map[string]string{"message": "unknown " + method},
		})
	}
}

func helperEnv(values ...string) []string {
	return append([]string{"QKA_PROVIDERRUNTIME_HELPER=1"}, values...)
}

func helperExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func helperConfig(t *testing.T, extraEnv ...string) LaunchConfig {
	t.Helper()
	dir := t.TempDir()
	return LaunchConfig{
		Bun:        helperExecutable(t),
		ConfigDir:  dir,
		AuthPath:   dir + "/auth.json",
		Directory:  dir,
		Env:        helperEnv(extraEnv...),
		LoadMillis: 200,
		ListMillis: 200,
	}
}

func jsonResult(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%s", data)
}
