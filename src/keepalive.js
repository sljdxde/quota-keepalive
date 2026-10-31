import { sanitize, terminalQuotaFailure, text, withTimeout } from "./common.js"

const ORIGIN = "https://copilot.tencent.com"
const CHAT_PATH = "/v2/chat/completions"
const FILLER = "The quick brown fox jumps over the lazy dog. "
const CHARS_PER_TOKEN = 4.5
const DEFAULT_ACTIVITY_INPUT_TOKENS = 25_000
const ACTIVITY_PROMPT = "Reply with a single word."
const ACTIVITY_SYSTEM_PROMPT = "You are a connectivity check. Reply with a single word."
export const DEFAULT_PROBE_MESSAGE = "Magpie daily connectivity check. Reply with OK."

function probeMessage(value) {
  return sanitize(value, 500).trim() || DEFAULT_PROBE_MESSAGE
}

export function buildKeepaliveBody({
  model,
  inputTokens,
  day,
  probeMode = "short",
  probeMessage: message = "",
}) {
  const prompt = `${probeMessage(message)} Today is ${day}.`
  const activity = probeMode === "activity"
  const context = activity || probeMode === "context"
  let content = prompt
  let system = "You are a concise connectivity check. Reply with a single word."
  if (context) {
    const tokens = Number(inputTokens) || (activity ? DEFAULT_ACTIVITY_INPUT_TOKENS : 0)
    const targetChars = Math.ceil(tokens * CHARS_PER_TOKEN)
    const filler = FILLER.repeat(Math.ceil(targetChars / FILLER.length))
    // WorkBuddy's CN gateway measures a real request by its input size. The
    // activity shape mirrors dsh's probe body so the request is recorded as
    // a conversation instead of a short connectivity check.
    content = activity ? `${filler}${ACTIVITY_PROMPT}` : `${filler}${prompt}`
    if (activity) system = ACTIVITY_SYSTEM_PROMPT
  }
  return {
    model,
    messages: [
      {
        role: "system",
        content: system,
      },
      {
        role: "user",
        content,
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

function numberValue(value) {
  if (typeof value === "number") return Number.isFinite(value) ? value : undefined
  if (typeof value !== "string" || !value.trim()) return undefined
  const n = Number(value.trim())
  return Number.isFinite(n) ? n : undefined
}

function hasKey(value, name) {
  return object(value) && Object.prototype.hasOwnProperty.call(value, name)
}

function numberField(value, names) {
  for (const name of names) {
    if (!hasKey(value, name)) continue
    const n = numberValue(value[name])
    if (n !== undefined) return n
  }
  return undefined
}

function usageEvidence(payloads) {
  const result = {
    sawUsage: false,
    credit: undefined,
    promptTokens: undefined,
    completionTokens: undefined,
    totalTokens: undefined,
  }
  for (const payload of Array.isArray(payloads) ? payloads : []) {
    const candidates = [
      payload,
      payload?.usage,
      payload?.data?.usage,
      payload?.data,
    ].filter(object)
    for (const candidate of candidates) {
      const credit = numberField(candidate, ["credit", "credits"])
      const promptTokens = numberField(candidate, ["prompt_tokens", "promptTokens", "input_tokens", "inputTokens"])
      const completionTokens = numberField(candidate, [
        "completion_tokens",
        "completionTokens",
        "output_tokens",
        "outputTokens",
      ])
      const totalTokens = numberField(candidate, ["total_tokens", "totalTokens"])
      if ([credit, promptTokens, completionTokens, totalTokens].every((value) => value === undefined)) {
        continue
      }
      result.sawUsage = true
      result.credit ??= credit
      result.promptTokens ??= promptTokens
      result.completionTokens ??= completionTokens
      result.totalTokens ??= totalTokens
    }
  }
  return result
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

function contentValue(value) {
  if (typeof value === "string") return value
  if (Array.isArray(value)) return value.map((part) => contentValue(part?.text)).join("")
  return ""
}

function payloadReply(payload) {
  const choices = Array.isArray(payload?.choices) ? payload.choices : []
  for (const choice of choices) {
    if (!object(choice)) continue
    const value = choice.delta?.content ?? choice.message?.content ?? choice.text
    const content = contentValue(value)
    if (content) return content
  }
  const blocks = Array.isArray(payload?.content) ? payload.content : []
  const blockText = blocks.map((block) => contentValue(block?.text)).join("")
  if (blockText) return blockText
  return contentValue(payload?.output_text)
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
      const piece = contentValue(value)
      if (piece) {
        sawReply = true
        content += piece
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

export function workbuddyProbeResult(response, raw, { probeMode = "short" } = {}) {
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

  const reply = assistantReply(source)
  let content = reply.content
  let hasReply = reply.ok
  for (const payload of payloads) {
    if (content) break
    const direct = payloadReply(payload)
    if (direct) {
      content = direct
      hasReply = true
      break
    }
  }
  const evidence = usageEvidence(payloads)
  const activity = probeMode === "activity" || probeMode === "context"
  const activityVerified = activity && (hasReply || evidence.sawUsage)
  if (activity && !activityVerified) {
    return {
      ok: false,
      content,
      activityVerified: false,
      error: "WorkBuddy's keepalive chat returned 200 without a model reply or usage evidence; no active conversation was confirmed",
    }
  }
  return {
    ok: true,
    content: sanitize(content, 120),
    activityVerified,
    ...(evidence.credit === undefined ? {} : { credit: evidence.credit }),
    ...(evidence.promptTokens === undefined ? {} : { promptTokens: evidence.promptTokens }),
    ...(evidence.completionTokens === undefined ? {} : { completionTokens: evidence.completionTokens }),
    ...(evidence.totalTokens === undefined ? {} : { totalTokens: evidence.totalTokens }),
  }
}

export async function sendKeepalive({
  fetchImpl,
  origin = ORIGIN,
  model,
  inputTokens,
  probeMode = "activity",
  probeMessage,
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
      body: JSON.stringify(buildKeepaliveBody({
        model,
        inputTokens,
        day,
        probeMode,
        probeMessage,
      })),
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
  const probe = workbuddyProbeResult(response, raw, { probeMode })
  if (!probe.ok) {
    throw new Error(probe.error)
  }
  return {
    model,
    reply: probe.content,
    activityVerified: probe.activityVerified,
    ...(probe.credit === undefined ? {} : { credit: probe.credit }),
    ...(probe.promptTokens === undefined ? {} : { promptTokens: probe.promptTokens }),
    ...(probe.completionTokens === undefined ? {} : { completionTokens: probe.completionTokens }),
    ...(probe.totalTokens === undefined ? {} : { totalTokens: probe.totalTokens }),
  }
}
