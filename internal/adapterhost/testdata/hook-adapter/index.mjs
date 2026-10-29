let nextID = 100;
const pending = new Map();
let buffer = "";

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

async function handleLine(line) {
  const msg = JSON.parse(line);

  if (msg.method === "initialize") {
    send({ jsonrpc: "2.0", id: msg.id, result: { ok: true } });
    return;
  }

  if (msg.method === "action" && msg.params.op === "useHook") {
    try {
      const result = await callHook("echo", { text: "hello" });
      send({ jsonrpc: "2.0", id: msg.id, result });
    } catch (e) {
      send({ jsonrpc: "2.0", id: msg.id, error: { code: -32000, message: String(e) } });
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
