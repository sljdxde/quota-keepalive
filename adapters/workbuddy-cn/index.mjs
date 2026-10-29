// WorkBuddy CN 官方适配器：发现已登录账号、完成每日签到。
// 所有出站请求经宿主 http.fetch 钩子（白名单、UA、超时由宿主统一处理）。

let nextID = 100;
const pending = new Map();
let buffer = "";
let baseURL = "https://workbuddy.example.com";

function send(obj) {
  process.stdout.write(JSON.stringify(obj) + "\n");
}

function callHook(method, params) {
  return new Promise((resolve, reject) => {
    const id = nextID++;
    pending.set(id, { resolve, reject });
    send({ jsonrpc: "2.0", id, method, params });
  });
}

async function httpFetch(method, url, body) {
  const req = { method, url };
  if (body !== undefined) {
    req.body = JSON.stringify(body);
    req.headers = { "Content-Type": "application/json" };
  }
  const resp = await callHook("http.fetch", req);
  if (resp.status < 200 || resp.status >= 300) {
    throw new Error("目标返回非 2xx: " + resp.status);
  }
  return JSON.parse(resp.body);
}

async function handleLine(line) {
  const msg = JSON.parse(line);

  if (msg.method === "initialize") {
    if (msg.params && msg.params.baseURL) {
      baseURL = msg.params.baseURL;
    }
    send({ jsonrpc: "2.0", id: msg.id, result: { ok: true } });
    return;
  }

  if (msg.method === "action") {
    const op = msg.params.op;
    try {
      if (op === "accounts.discover") {
        const resp = await callHook("http.fetch", {
          method: "GET",
          url: baseURL + "/api/accounts",
        });
        if (resp.status === 403) {
          send({ jsonrpc: "2.0", id: msg.id, result: { status: "platform_unsupported", accounts: [] } });
          return;
        }
        if (resp.status < 200 || resp.status >= 300) {
          throw new Error("发现失败: " + resp.status);
        }
        const data = JSON.parse(resp.body);
        const accounts = data.accounts || [];
        if (accounts.length === 0) {
          send({ jsonrpc: "2.0", id: msg.id, result: { status: "not_signed_in", accounts: [] } });
          return;
        }
        const allReauth = accounts.every((a) => a.source && a.source.health === "reauth_required");
        send({
          jsonrpc: "2.0",
          id: msg.id,
          result: { status: allReauth ? "reauth_required" : "ok", accounts },
        });
      } else if (op === "checkin.run") {
        const data = await httpFetch("POST", baseURL + "/api/checkin", { accountId: msg.params.accountId });
        // 结构化日志（经宿主钩子，命名空间由宿主强制）。
        await callHook("log.write", {
          target: "workbuddy-cn",
          accountId: msg.params.accountId,
          operation: "checkin",
          level: "info",
          message: "签到完成",
        });
        send({ jsonrpc: "2.0", id: msg.id, result: data });
      } else if (op === "keepalive.run") {
        // 对齐 25k 输入 / 1 输出 token 的最低成本真实对话。
        const input = "word ".repeat(20000); // 约 100k 字符 ≈ 25k token
        const resp = await callHook("http.fetch", {
          method: "POST",
          url: baseURL + "/api/chat",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            accountId: msg.params.accountId,
            maxOutputTokens: 1,
            messages: [{ role: "user", content: input }],
          }),
        });
        if (resp.status < 200 || resp.status >= 300) {
          throw new Error("对话失败: " + resp.status);
        }
        const data = JSON.parse(resp.body);
        // 必须收到真实模型回复；伪造心跳/仅建连判失败。
        if (!data.reply || !String(data.reply).trim()) {
          throw new Error("未收到真实模型回复");
        }
        // 成本取自当前上游回执，不硬编码。
        send({
          jsonrpc: "2.0",
          id: msg.id,
          result: { status: "success", reply: data.reply, cost: data.cost },
        });
      } else {
        send({ jsonrpc: "2.0", id: msg.id, error: { code: -32601, message: "未知 op: " + op } });
      }
    } catch (e) {
      send({ jsonrpc: "2.0", id: msg.id, error: { code: -32000, message: String(e.message || e) } });
    }
    return;
  }

  // 宿主对钩子请求的响应。
  if (msg.id !== undefined && pending.has(msg.id)) {
    const p = pending.get(msg.id);
    pending.delete(msg.id);
    if (msg.error) p.reject(new Error(msg.error.message));
    else p.resolve(msg.result);
  }
}

process.stdin.on("data", (chunk) => {
  buffer += chunk.toString();
  let idx;
  while ((idx = buffer.indexOf("\n")) >= 0) {
    const line = buffer.slice(0, idx).trim();
    buffer = buffer.slice(idx + 1);
    if (line) {
      Promise.resolve(handleLine(line)).catch(() => {});
    }
  }
});
