import { sanitize, terminalQuotaFailure, text, withTimeout } from "./common.js"

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
        content: `${filler}Reply with a single word.`,
      },
    ],
    max_tokens: 1,
    stream: true,
  }
}

function object(value) {
  return !!value && typeof value === "object" && !Array.isArray(value)
}

function parsedJson(value) {
  try {
    return JSON.parse(value)
  } catch {
    return null
  }
}

function businessFailure(body) {
  if (!object(body)) return ""
  if (body.success === false) {
    return sanitize(body.message || body.msg || "the upstream rejected the request", 260)
  }
  const error = body.error
  if (error !== undefined && error !== null && error !== "") {
    if (object(error)) {
      return sanitize(error.message || error.msg || error.code || "the upstream rejected the request", 260)
    }
    return sanitize(error, 260)
  }
  if (body.code !== undefined && body.code !== null && String(body.code) !== "0") {
    return sanitize(body.msg || body.message || `business code ${body.code}`, 260)
  }
  return ""
}

export function responseMessage(raw) {
  const body = parsedJson(raw)
  if (object(body)) {
    return sanitize(body.error?.message || body.msg || body.message || "", 260)
  }
  for (const line of raw.split(/\r?\n/)) {
    const trimmed = line.trim()
    if (!trimmed.startsWith("data:")) continue
    const event = parsedJson(trimmed.slice(5).trim())
    const message = businessFailure(event)
    if (message) return message
  }
  return ""
}

export function assistantReply(raw) {
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

export function workbuddyProbeResult(response, raw) {
  const source = text(raw)
  const body = source.trim()
  const contentType = text(response?.headers?.get?.("content-type")).toLowerCase()
  if (contentType.includes("text/html") || /^<(?:!doctype\s+html|html)(?:[\s>]|$)/i.test(body)) {
    return {
      ok: false,
      content: "",
      error: "WorkBuddy's keepalive chat returned an HTML page; sign in again",
    }
  }

  const payloads = []
  const direct = parsedJson(body)
  if (direct !== null) payloads.push(direct)
  for (const line of source.split(/\r?\n/)) {
    const trimmed = line.trim()
    if (!trimmed.startsWith("data:")) continue
    const event = parsedJson(trimmed.slice(5).trim())
    if (event !== null) payloads.push(event)
  }
  for (const payload of payloads) {
    const failure = businessFailure(payload)
    if (failure) {
      return {
        ok: false,
        content: "",
        error: `WorkBuddy's keepalive chat returned 200 but rejected the request: ${failure}`,
      }
    }
  }

  // WorkBuddy can accept a real conversation without emitting assistant text:
  // max_tokens is one and the signed gateway may finish with only [DONE].
  // dsh treats HTTP 2xx as a successful probe for that reason.
  const reply = assistantReply(source)
  return {
    ok: true,
    content: reply.content,
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
  const { response, raw } = await withTimeout(async () => {
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
    return { response, raw: await response.text() }
  }, timeoutMs, `WorkBuddy's keepalive chat timed out after ${timeoutMs}ms`)
  if (!response.ok) {
    const said = responseMessage(raw)
    const error = new Error(
      `WorkBuddy's keepalive chat answered HTTP ${response.status}${said ? `: ${said}` : ""}`,
    )
    const terminal = terminalQuotaFailure(response.status, raw)
    error.status = response.status
    error.retryNext = !terminal
    error.retryToday = !terminal
    throw error
  }
  const probe = workbuddyProbeResult(response, raw)
  if (!probe.ok) {
    throw new Error(probe.error)
  }
  return {
    model,
    reply: probe.content,
  }
}
