// 测试用假适配器：NDJSON JSON-RPC over stdio，供 adapterhost 端到端测试。
let buf = "";

function send(obj) {
  process.stdout.write(JSON.stringify(obj) + "\n");
}

function handle(msg) {
  if (msg.method === "initialize") {
    send({ jsonrpc: "2.0", id: msg.id, result: { protocol: "qka-adapter", version: "1.0.0" } });
    return;
  }

  if (msg.method !== "action") {
    send({ jsonrpc: "2.0", id: msg.id, error: { code: -32601, message: "unknown method" } });
    return;
  }

  const op = msg.params && msg.params.op;
  if (op === "checkin") {
    send({ jsonrpc: "2.0", id: msg.id, result: { state: "signed", message: "ok" } });
    return;
  }
  if (op === "crash") {
    process.exit(1);
  }
  if (op === "oversize") {
    send({ jsonrpc: "2.0", id: msg.id, result: { blob: "x".repeat(2 * 1024 * 1024) } });
    return;
  }
  send({ jsonrpc: "2.0", id: msg.id, error: { code: -32601, message: "unknown op: " + op } });
}

process.stdin.on("data", (chunk) => {
  buf += chunk.toString();
  let nl;
  while ((nl = buf.indexOf("\n")) >= 0) {
    const line = buf.slice(0, nl);
    buf = buf.slice(nl + 1);
    if (!line.trim()) continue;
    try {
      handle(JSON.parse(line));
    } catch {
      // 忽略无法解析的行
    }
  }
});
