package adapterkit

const skeletonManifest = `{
  "id": "my-adapter",
  "name": "My Adapter",
  "version": "0.1.0",
  "entry": "index.mjs",
  "capabilities": ["http.fetch", "log.write"],
  "needs": ["http.fetch", "log.write"],
  "actions": [
    {
      "op": "accounts.discover",
      "effects": {
        "accessesNetwork": true,
        "mutatesTarget": false,
        "spendsCredits": false,
        "idempotent": true,
        "mayRequireReauth": false,
        "mutatesLocalState": false
      }
    }
  ],
  "compatibility": { "qka": ">=1.0" }
}
`

const skeletonIndex = `// 最小适配器骨架：实现 initialize 与 accounts.discover。
const handlers = {
  initialize: async () => ({ status: "ok", protocolVersion: "2025-06-18" }),
  "accounts.discover": async () => ({
    status: "ok",
    accounts: [
      {
        id: "example-1",
        displayName: "Example User",
        maskedIdentity: "****1234",
        source: { id: "src-1", kind: "managed", health: "healthy" }
      }
    ]
  })
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
`

const skeletonPackage = `{
  "name": "my-adapter",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "main": "index.mjs",
  "scripts": {
    "test": "echo \\"no tests\\" && exit 0"
  }
}
`

const skeletonReadme = `# My Adapter

由 qka adapter init 生成。

- 修改 manifest.json 声明动作与效应。
- 在 index.mjs 实现各动作。
- 用 qka adapter validate . 离线校验（不联网、不读凭据）。
`
