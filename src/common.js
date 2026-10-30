import { homedir } from "node:os"
import { isAbsolute, join } from "node:path"

const BEIJING_OFFSET_MS = 8 * 60 * 60 * 1000

export function beijingDay(now = new Date()) {
  return new Date(now.getTime() + BEIJING_OFFSET_MS).toISOString().slice(0, 10)
}

export function finiteNumber(value, fallback = 0) {
  const n = typeof value === "number" ? value : Number(value)
  return Number.isFinite(n) ? n : fallback
}

export function text(value) {
  return typeof value === "string" ? value : ""
}

export function truthy(value) {
  return value === true || value === 1 || value === "1" || value === "true"
}

export function magpieConfigHome() {
  const xdg = process.env.XDG_CONFIG_HOME
  if (typeof xdg === "string" && isAbsolute(xdg)) return join(xdg, "magpie")
  return join(homedir(), ".config", "magpie")
}

export function defaultStatePath() {
  return join(magpieConfigHome(), "workbuddy-daily-keepalive.json")
}

export function defaultCommandPath() {
  return join(magpieConfigHome(), "daily-keepalive-command.json")
}

export function accountKey(auth) {
  const uid = text(auth?.uid).trim()
  if (!uid) return ""
  return `${uid}|${text(auth?.enterpriseId).trim()}`
}

export function appendMessage(base, extra) {
  const a = text(base).trim()
  const b = text(extra).trim()
  if (!a) return b
  if (!b) return a
  return `${a} ${b}`
}

export function sanitize(value, limit = 480) {
  let out = text(value)
  out = out.replace(/\beyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\b/g, "[redacted token]")
  out = out.replace(/(\bBearer\s+)[^\s"']+/gi, "$1[redacted]")
  out = out.replace(
    /(\b(?:access_token|refresh_token|api_key|apikey|authorization|token)\b\s*[=:]\s*["']?)[^"'\s,}&]+/gi,
    "$1[redacted]",
  )
  return out.slice(0, limit)
}

export function errorText(error) {
  return sanitize(error instanceof Error ? error.message : String(error))
}

export function terminalQuotaFailure(status, message) {
  if (status !== 402 && status !== 429) return false
  return /credits?\s+(?:are\s+)?exhausted|insufficient\s+(?:credits?|quota|balance)|credit\s+(?:package|balance)|free\s+usage\s+limit|exceeded\s+(?:the\s+)?quota|quota\s+(?:exceeded|exhausted)|add-on\s+packs|purchase|subscribe|payment required|billing/i
    .test(text(message))
}

export async function withTimeout(work, timeoutMs, message) {
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) return work()
  let timer
  try {
    return await Promise.race([
      Promise.resolve().then(work),
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error(message)), timeoutMs)
        timer.unref?.()
      }),
    ])
  } finally {
    if (timer) clearTimeout(timer)
  }
}
