import { mkdtemp, rm } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"

export function json(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  })
}

export function sse(body) {
  return new Response(body, {
    status: 200,
    headers: { "content-type": "text/event-stream" },
  })
}

export async function tempDir(prefix = "quota-keepalive-") {
  const dir = await mkdtemp(join(tmpdir(), prefix))
  return {
    dir,
    cleanup: () => rm(dir, { recursive: true, force: true }),
    path: (...parts) => join(dir, ...parts),
  }
}

export function openAiStream(model = "test-model", content = "OK") {
  return sse(
    `data: ${JSON.stringify({ choices: [{ delta: { content } }], model })}\n\n` +
    "data: [DONE]\n\n",
  )
}
