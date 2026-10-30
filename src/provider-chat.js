import { sanitize, terminalQuotaFailure, text, withTimeout } from "./common.js"
import {
  DEFAULT_PROBE_MESSAGE,
  buildKeepaliveBody,
  sendKeepalive,
} from "./keepalive.js"

const FILLER = "The quick brown fox jumps over the lazy dog. "
const CHARS_PER_TOKEN = 4.5

export class ProviderChatError extends Error {
  constructor(message, {
    provider = "",
    model = "",
    status = 0,
    code = "",
    retryNext = true,
    retryToday = true,
  } = {}) {
    super(message)
    this.name = "ProviderChatError"
    this.provider = provider
    this.model = model
    this.status = status
    this.code = code
    this.retryNext = retryNext
    this.retryToday = retryToday
  }
}

function object(value) {
  return !!value && typeof value === "object" && !Array.isArray(value)
}

function parsedJson(raw) {
  try {
    return JSON.parse(raw)
  } catch {
    return null
  }
}

export function endpointFor(baseURL, protocol) {
  const base = text(baseURL).trim().replace(/\/+$/, "")
  if (!base) throw new Error("the provider login plugin returned no request address")
  return `${base}${protocol === "anthropic" ? "/messages" : "/chat/completions"}`
}

export function anthropicBody({
  model,
  inputTokens,
  day,
  probeMode = "short",
  probeMessage = "",
}) {
  const message = sanitize(probeMessage, 500).trim() || DEFAULT_PROBE_MESSAGE
  const prompt = `${message} Today is ${day}.`
  let content = prompt
  if (probeMode === "context") {
    const targetChars = Math.ceil(inputTokens * CHARS_PER_TOKEN)
    const filler = FILLER.repeat(Math.ceil(targetChars / FILLER.length))
    content = `${filler}${prompt}`
  }
  return {
    model,
    system: "You are a concise connectivity check. Reply with a short word.",
    messages: [{
      role: "user",
      content,
    }],
    max_tokens: 8,
    stream: true,
  }
}

export function messageFromBody(body) {
  if (!object(body)) return ""
  const choices = Array.isArray(body.choices) ? body.choices : []
  for (const choice of choices) {
    const value = choice?.message?.content ?? choice?.delta?.content ?? choice?.text
    if (typeof value === "string" && value.trim()) return value
    if (Array.isArray(value)) {
      const joined = value.map((part) => text(part?.text)).join("")
      if (joined.trim()) return joined
    }
  }
  const blocks = Array.isArray(body.content) ? body.content : []
  const joined = blocks.map((block) => text(block?.text)).join("")
  if (joined.trim()) return joined
  return text(body.output_text)
}

export function messageFromStream(raw, protocol) {
  let content = ""
  let sawEvent = false
  for (const line of raw.split(/\r?\n/)) {
    const trimmed = line.trim()
    if (!trimmed.startsWith("data:")) continue
    const data = trimmed.slice(5).trim()
    if (!data || data === "[DONE]") continue
    const event = parsedJson(data)
    if (!object(event)) continue
    sawEvent = true
    if (protocol === "anthropic") {
      const delta = text(event.delta?.text)
      if (delta) content += delta
      const direct = text(event.completion)
      if (direct) content += direct
      const blocks = Array.isArray(event.content_block?.content) ? event.content_block.content : []
      content += blocks.map((block) => text(block?.text)).join("")
      continue
    }
    const choices = Array.isArray(event.choices) ? event.choices : []
    for (const choice of choices) {
      const delta = choice?.delta?.content ?? choice?.message?.content ?? choice?.text
      if (typeof delta === "string") content += delta
      if (Array.isArray(delta)) content += delta.map((part) => text(part?.text)).join("")
    }
  }
  return { ok: sawEvent && content.trim().length > 0, content }
}

export function errorMessage(raw, status) {
  const body = parsedJson(raw)
  if (object(body)) {
    return sanitize(
      body.error?.message ||
      body.error?.code ||
      body.message ||
      body.msg ||
      body.ResponseMetadata?.Error?.Message ||
      "",
      260,
    )
  }
  return sanitize(raw, 260) || `HTTP ${status}`
}

export async function sendProviderChat({
  provider,
  model,
  protocol,
  loader,
  inputTokens,
  probeMode,
  probeMessage,
  timeoutMs,
  day,
  key,
}) {
  if (typeof loader?.fetch !== "function") {
    throw new ProviderChatError("the provider login plugin returned no signed request", {
      provider,
      model,
      retryNext: true,
    })
  }

  if (provider === "workbuddy" || provider === "workbuddy-ai") {
    try {
      const origin = provider === "workbuddy"
        ? "https://copilot.tencent.com"
        : "https://www.workbuddy.ai"
      return await sendKeepalive({
        fetchImpl: loader.fetch,
        origin,
        model,
        inputTokens,
        probeMode,
        probeMessage,
        timeoutMs,
        day,
        key,
      })
    } catch (error) {
      if (error instanceof ProviderChatError) throw error
      throw new ProviderChatError(
        sanitize(error instanceof Error ? error.message : String(error), 260),
        {
          provider,
          model,
          status: Number(error?.status) || 0,
          retryNext: error?.retryNext !== false,
          retryToday: error?.retryToday !== false,
        },
      )
    }
  }

  const url = endpointFor(loader.baseURL, protocol)
  const body = protocol === "anthropic"
    ? anthropicBody({ model, inputTokens, day, probeMode, probeMessage })
    : buildKeepaliveBody({ model, inputTokens, day, probeMode, probeMessage })

  let response
  let raw
  try {
    ({ response, raw } = await withTimeout(async () => {
      const response = await loader.fetch(url, {
        method: "POST",
        headers: {
          "content-type": "application/json",
          accept: "text/event-stream, application/json",
        },
        body: JSON.stringify(body),
        signal: AbortSignal.timeout(timeoutMs),
      })
      return { response, raw: await response.text() }
    }, timeoutMs, `${provider} chat timed out after ${timeoutMs}ms`))
  } catch (error) {
    throw new ProviderChatError(
      sanitize(error instanceof Error ? error.message : String(error), 260),
      { provider, model, retryNext: true },
    )
  }

  if (!response.ok) {
    const said = errorMessage(raw, response.status)
    const terminal = terminalQuotaFailure(response.status, raw)
    throw new ProviderChatError(
      `${provider} chat answered HTTP ${response.status}: ${said}`,
      {
        provider,
        model,
        status: response.status,
        code: parsedJson(raw)?.error?.code || parsedJson(raw)?.code || "",
        retryNext: !terminal,
        retryToday: !terminal,
      },
    )
  }

  const parsed = parsedJson(raw)
  const jsonReply = messageFromBody(parsed)
  const streamed = jsonReply ? { ok: true, content: jsonReply } : messageFromStream(raw, protocol)
  if (!streamed.ok) {
    throw new ProviderChatError(
      `${provider} chat returned 200 but no model reply`,
      { provider, model, status: response.status, retryNext: true },
    )
  }
  return { model, reply: sanitize(streamed.content, 120) }
}
