// 豆包工作官方适配器（独立产品，非豆包电脑版）：发现已登录账号、完成每日签到。
// 签到非幂等：同日重复返回通用业务码 710010702/系统错误，而非明确已签到；
// 由本地北京日界成功门保证同日成功一次，成功后不再发领取。

let nextID = 100;
const pending = new Map();
let buffer = "";
let baseURL = "https://work.doubao.example.com";

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
        const data = await httpFetch("POST", baseURL + "/api/checkin", {
          accountId: msg.params.accountId,
        });
        // 同日重复：通用系统错误，非明确已签到。
        if (data.code === "710010702") {
          send({
            jsonrpc: "2.0",
            id: msg.id,
            result: { status: "error", code: "710010702", message: "system error" },
          });
          return;
        }
        await callHook("log.write", {
          target: "doubao-work",
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
