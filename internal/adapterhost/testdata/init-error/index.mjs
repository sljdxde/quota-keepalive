let buffer = "";

process.stdin.on("data", (chunk) => {
  buffer += chunk.toString();
  const newline = buffer.indexOf("\n");
  if (newline < 0) return;
  const line = buffer.slice(0, newline);
  const request = JSON.parse(line);
  process.stdout.write(JSON.stringify({
    jsonrpc: "2.0",
    id: request.id,
    error: { code: -32000, message: "initialize denied" },
  }) + "\n");
});
