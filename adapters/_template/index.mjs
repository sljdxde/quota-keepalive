// 最小适配器骨架：实现 initialize 握手与 accounts.discover。
// 用 qka 注入的 http.fetch / log.write 能力，不直接持有凭据。

const handlers = {
  initialize: async () => ({ status: "ok", protocolVersion: "2025-06-18" }),

  "accounts.discover": async () => {
    // TODO: 替换为真实目标的发现逻辑。
    return {
      status: "ok",
      accounts: [
        {
          id: "example-1",
          displayName: "Example User",
          maskedIdentity: "****1234",
          source: { id: "src-1", kind: "managed", health: "healthy" },
        },
      ],
    };
  },
};

function send(message) {
  process.stdout.write(JSON.stringify(message) + "\n");
}

let buffer = "";
process.stdin.on("data", (chunk) => {
  buffer += chunk;
  let idx;
  while ((idx = buffer.indexOf("\n")) >= 0) {
    const line = buffer.slice(0, idx).trim();
    buffer = buffer.slice(idx + 1);
    if (!line) continue;
    const req = JSON.parse(line);
    const handler = handlers[req.method];
    if (!handler) {
      send({ id: req.id, error: { code: -32601, message: "method not found" } });
      continue;
    }
    Promise.resolve(handler(req.params || {}))
      .then((result) => send({ id: req.id, result }))
      .catch((err) => send({ id: req.id, error: { code: -32000, message: String(err) } }));
  }
});
