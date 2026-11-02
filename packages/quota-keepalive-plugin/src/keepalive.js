import { sanitize, text } from "./common.js"

const ORIGIN = "https://copilot.tencent.com"
const CHAT_PATH = "/v2/chat/completions"
const FILLER = "The quick brown fox jumps over the lazy dog. "
const CHARS_PER_TOKEN = 4.5

export function buildKeepaliveBody({ model, inputTokens, day }) {
  const targetChars = Math.ceil(inputTokens * CHARS_PER_TOKEN)
  const filler = FILLER.repeat(Math.ceil(targetChars / FILLER.length))
  return {
    model,
    messages: [
      {
        role: "system",
        content: "You are a connectivity check. Reply with a single word.",
      },
      {
        role: "user",
        content: `${filler}Today is ${day}. Reply with a single word.`,
      },
    ],
    max_tokens: 1,
    stream: true,
  }
}

function responseMessage(raw) {
  const body = (() => {
    try {
      return JSON.parse(raw)
    } catch {
      return null
    }
  })()
  if (!body || typeof body !== "object") return ""
  return sanitize(body.error?.message || body.msg || body.message || "", 260)
}

function assistantReply(raw) {
  let sawReply = false
  let content = ""
  for (const line of raw.split(/\r?\n/)) {
    const trimmed = line.trim()
    if (!trimmed.startsWith("data:")) continue
    const data = trimmed.slice(5).trim()
    if (data === "[DONE]") continue
    let event
    try {
      event = JSON.parse(data)
    } catch {
      continue
    }
    const choices = Array.isArray(event?.choices) ? event.choices : []
    for (const choice of choices) {
      if (!choice || typeof choice !== "object") continue
      const value =
        choice.delta?.content ??
        choice.message?.content ??
        choice.text
      if (typeof value === "string" && value) {
        sawReply = true
        content += value
      }
    }
  }
  return {
    // A choice with no content is a frame, not a reply. A [DONE] frame is
    // only the end of a stream and must never prove that the model answered.
    ok: sawReply,
    content: sanitize(content, 120),
  }
}

export async function sendKeepalive({
  fetchImpl,
  origin = ORIGIN,
  model,
  inputTokens,
  timeoutMs,
  day,
  key,
}) {
  if (typeof fetchImpl !== "function") {
    throw new Error("WorkBuddy's signed fetch is unavailable")
  }
  const response = await fetchImpl(`${origin}${CHAT_PATH}`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      accept: "text/event-stream",
      "x-magpie-workbuddy-session": `workbuddy-keepalive:${key}:${day}`.slice(0, 200),
    },
    body: JSON.stringify(buildKeepaliveBody({ model, inputTokens, day })),
    signal: AbortSignal.timeout(timeoutMs),
  })
  const raw = await response.text()
  if (!response.ok) {
    const said = responseMessage(raw)
    throw new Error(
      `WorkBuddy's keepalive chat answered HTTP ${response.status}${said ? `: ${said}` : ""}`,
    )
  }
  const reply = assistantReply(raw)
  if (!reply.ok) {
    throw new Error("WorkBuddy's keepalive chat returned 200 but no streamed model reply")
  }
  return {
    model,
    reply: reply.content,
  }
}
