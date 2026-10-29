// Qoder CN 官方适配器：发现已登录账号、完成每日签到。
// 活动 id 现读现用：每次签到先读取当前活动，再带活动 id 领取，不硬编码。
// 所有出站请求经宿主 http.fetch 钩子（白名单、UA、超时由宿主统一处理）。

let nextID = 100;
const pending = new Map();
let buffer = "";
let baseURL = "https://qoder.example.com";

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

async function currentActivityId() {
  const resp = await callHook("http.fetch", {
    method: "GET",
    url: baseURL + "/api/activity",
  });
  if (resp.status < 200 || resp.status >= 300) {
    throw new Error("活动读取失败: " + resp.status);
  }
  const data = JSON.parse(resp.body);
  return data.activityId;
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
        const data = await httpFetch("GET", baseURL + "/api/accounts");
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
        // 活动 id 现读现用。
        const activityId = await currentActivityId();
        const data = await httpFetch("POST", baseURL + "/api/checkin", {
          accountId: msg.params.accountId,
          activityId,
        });
        await callHook("log.write", {
          target: "qoder-cn",
          accountId: msg.params.accountId,
          operation: "checkin",
          level: "info",
          message: "签到完成",
        });
        send({ jsonrpc: "2.0", id: msg.id, result: data });
      } else {
        send({ jsonrpc: "2.0", id: msg.id, error: { code: -32601, message: "未知 op: " + op } });
      }
    } catch (e) {
      send({ jsonrpc: "2.0", id: msg.id, error: { code: -32000, message: String(e.message || e) } });
    }
    return;
  }

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
