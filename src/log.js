import { createHash, randomBytes } from "node:crypto"
import { chmod, mkdir, open, readFile, rename, stat, unlink } from "node:fs/promises"
import { dirname, isAbsolute, join, resolve } from "node:path"
import { magpieConfigHome, sanitize } from "./common.js"

export const LOG_VERSION = 1
export const DEFAULT_LOG_MAX_BYTES = 2 * 1024 * 1024
export const MIN_LOG_MAX_BYTES = 64 * 1024
export const MAX_LOG_MAX_BYTES = 50 * 1024 * 1024

const MAX_READ_RECORDS = 5_000
const CONTROL = /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/g
const queues = new Map()
const warned = new Map()
const beijingTime = new Intl.DateTimeFormat("sv-SE", {
  timeZone: "Asia/Shanghai",
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hourCycle: "h23",
})

export function defaultLogPath() {
  return join(magpieConfigHome(), "workbuddy-daily.log.jsonl")
}

export function newRunId(now = new Date()) {
  const stamp = now.toISOString().replace(/[-:]/g, "").replace(/\.\d{3}Z$/, "Z")
  return `${stamp}-${randomBytes(4).toString("hex")}`
}

export function accountTag(account) {
  const value = typeof account === "string" ? account.trim() : ""
  if (!value) return ""
  return `acct-${createHash("sha256").update(value).digest("hex").slice(0, 12)}`
}

export function safeLogText(value, account = "", limit = 480) {
  let out = typeof value === "string" ? value : String(value ?? "")
  const secrets = (typeof account === "string" ? account.split("|") : [])
    .map((part) => part.trim())
    .filter((part) => part.length >= 4)
    .sort((a, b) => b.length - a.length)
  for (const secret of secrets) out = out.split(secret).join("[redacted account]")
  out = sanitize(out, Math.max(1, Number(limit) || 480))
  out = out.replace(CONTROL, " ").replace(/\s+/g, " ").trim()
  return out.slice(0, Math.max(1, Number(limit) || 480))
}

function serialized(path, work) {
  const previous = queues.get(path) || Promise.resolve()
  const next = previous.catch(() => {}).then(work)
  queues.set(path, next)
  return next.finally(() => {
    if (queues.get(path) === next) queues.delete(path)
  })
}

const FILE_OPS = { stat, rename, unlink, chmod, open, mkdir }

export async function rotateIfNeeded(path, maxBytes, nextBytes, ops = FILE_OPS) {
  let info
  try {
    info = await ops.stat(path)
  } catch (error) {
    if (error?.code === "ENOENT") return
    throw error
  }
  if (info.size === 0 || info.size + nextBytes <= maxBytes) return

  const previous = `${path}.1`
  try {
    await ops.rename(path, previous)
  } catch (error) {
    if (error?.code === "ENOENT") return
    if (!["EEXIST", "EPERM", "EACCES"].includes(error?.code)) throw error
    await ops.unlink(previous).catch((unlinkError) => {
      if (unlinkError?.code !== "ENOENT") throw unlinkError
    })
    await ops.rename(path, previous)
  }
  await ops.chmod(previous, 0o600).catch(() => {})
}

export async function appendUnlocked(path, maxBytes, line, ops = FILE_OPS) {
  await ops.mkdir(dirname(path), { recursive: true, mode: 0o700 })
  await rotateIfNeeded(path, maxBytes, Buffer.byteLength(line), ops)
  const handle = await ops.open(path, "a", 0o600)
  try {
    // mode on open only applies to a new file.
    await handle.chmod(0o600)
    await handle.writeFile(line, "utf8")
  } finally {
    await handle.close().catch(() => {})
  }
  warned.delete(path)
}

function warnWrite(path, error) {
  const now = Date.now()
  const last = warned.get(path) || 0
  warned.set(path, now)
  if (now - last < 5 * 60 * 1000) return
  const why = sanitize(error instanceof Error ? error.message : String(error), 260)
  console.warn(`[workbuddy-daily] cannot write log ${path}: ${why}`)
}

export async function appendDailyLog({
  logPath,
  logMaxBytes = DEFAULT_LOG_MAX_BYTES,
  entry,
}) {
  if (typeof logPath !== "string" || !isAbsolute(logPath)) {
    warnWrite(String(logPath || "log"), new Error("logPath must be absolute"))
    return false
  }
  if (!entry || typeof entry !== "object" || Array.isArray(entry)) {
    warnWrite(logPath, new Error("log entry must be an object"))
    return false
  }
  const limit = Number.isInteger(logMaxBytes) && logMaxBytes > 0
    ? logMaxBytes
    : DEFAULT_LOG_MAX_BYTES
  let line
  try {
    line = `${JSON.stringify({ ...entry, version: LOG_VERSION })}\n`
  } catch (error) {
    warnWrite(logPath, error)
    return false
  }
  return serialized(logPath, () => appendUnlocked(logPath, limit, line))
    .then(() => true)
    .catch((error) => {
      warnWrite(logPath, error)
      return false
    })
}

function validRecord(value) {
  return !!value &&
    typeof value === "object" &&
    !Array.isArray(value) &&
    value.version === LOG_VERSION &&
    typeof value.ts === "string" &&
    typeof value.event === "string" &&
    typeof value.status === "string"
}

function timestamp(record) {
  const at = Date.parse(record.ts)
  return Number.isFinite(at) ? at : 0
}

export async function readDailyLogs({
  logPath,
  includePrevious = true,
  day = "",
  status = "",
  limit = 50,
}) {
  if (typeof logPath !== "string" || !isAbsolute(logPath)) {
    throw new Error("logPath must be absolute")
  }
  const base = resolve(logPath)
  const files = includePrevious ? [`${base}.1`, base] : [base]
  const records = []
  let malformed = 0
  let order = 0
  let rotated = false

  for (const file of files) {
    let raw
    try {
      raw = await readFile(file, "utf8")
      if (file === `${base}.1`) rotated = true
    } catch (error) {
      if (error?.code === "ENOENT") continue
      throw new Error(`cannot read ${file}: ${sanitize(error?.message || error, 260)}`)
    }
    for (const line of raw.split(/\r?\n/)) {
      if (!line.trim()) continue
      let record
      try {
        record = JSON.parse(line)
      } catch {
        malformed++
        continue
      }
      if (!validRecord(record)) {
        malformed++
        continue
      }
      if (day && record.day !== day) continue
      if (status && record.status !== status) continue
      records.push({ record, order: order++ })
    }
  }

  records.sort((a, b) => timestamp(a.record) - timestamp(b.record) || a.order - b.order)
  const bounded = Math.max(1, Math.min(MAX_READ_RECORDS, Number(limit) || 50))
  return {
    records: records.slice(-bounded).map((item) => item.record),
    files,
    malformed,
    rotated,
  }
}

function displayText(value) {
  return safeLogText(value, "", 480)
}

function quoted(value) {
  return JSON.stringify(displayText(value))
}

export function formatLogRecord(record) {
  const at = new Date(record.ts)
  const when = Number.isFinite(at.getTime()) ? `${beijingTime.format(at)} +08` : record.ts
  const fields = []
  const scalar = [
    ["run", record.run],
    ["acct", record.account],
    ["outcome", record.outcome],
    ["attempts", record.attempts],
    ["model", record.model],
    ["probeMode", record.probeMode],
    ["inputTokens", record.inputTokens],
    ["credit", record.credit],
    ["activityVerified", record.activityVerified],
    ["streak", record.streak],
    ["durationMs", record.durationMs],
    ["retry", record.retry],
    ["persisted", record.persisted],
    ["keepalive", record.keepalive],
  ]
  for (const [key, value] of scalar) {
    if (value === undefined || value === null || value === "") continue
    fields.push(`${key}=${typeof value === "string" ? displayText(value) : value}`)
  }
  if (record.provider) fields.unshift(`provider=${displayText(record.provider)}`)
  if (record.message) fields.push(`message=${quoted(record.message)}`)
  if (record.error) fields.push(`error=${quoted(record.error)}`)
  return `${when} [${displayText(record.status)}] ${displayText(record.event)}${
    fields.length ? ` ${fields.join(" ")}` : ""
  }`
}

export function formatLogRecords(records) {
  return (Array.isArray(records) ? records : []).map(formatLogRecord).join("\n")
}
