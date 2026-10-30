import { randomBytes } from "node:crypto"
import { chmod, mkdir, open, readFile, rename, unlink } from "node:fs/promises"
import { dirname, join } from "node:path"
import { errorText, magpieConfigHome, sanitize } from "./common.js"

const VERSION = 1
const FILE_OPS = { chmod, mkdir, open, readFile, rename, unlink }

export function defaultSchedulePath() {
  return join(magpieConfigHome(), "quota-keepalive-schedule.json")
}

export function emptySchedule() {
  return {
    version: VERSION,
    signature: "",
    lastRunAt: "",
    nextRunAt: "",
    lastStatus: "",
    lastSummary: "",
  }
}

function validDate(value) {
  return typeof value === "string" && Number.isFinite(Date.parse(value))
}

export async function readSchedule(path, ops = FILE_OPS) {
  let raw
  try {
    raw = await ops.readFile(path, "utf8")
  } catch (error) {
    if (error?.code === "ENOENT") return emptySchedule()
    throw new Error(`cannot read ${path}: ${errorText(error)}`)
  }
  let value
  try {
    value = JSON.parse(raw)
  } catch {
    throw new Error(`${path} is not valid JSON; it was left unchanged`)
  }
  if (!value || typeof value !== "object" || Array.isArray(value) || value.version !== VERSION) {
    throw new Error(`${path} has an unsupported shape; it was left unchanged`)
  }
  return {
    version: VERSION,
    signature: sanitize(value.signature, 2_000),
    lastRunAt: validDate(value.lastRunAt) ? value.lastRunAt : "",
    nextRunAt: validDate(value.nextRunAt) ? value.nextRunAt : "",
    lastStatus: sanitize(value.lastStatus, 24),
    lastSummary: sanitize(value.lastSummary, 480),
  }
}

export async function writeSchedule(path, value, ops = FILE_OPS) {
  await ops.mkdir(dirname(path), { recursive: true, mode: 0o700 })
  const tmp = `${path}.tmp-${process.pid}-${randomBytes(6).toString("hex")}`
  let handle
  try {
    handle = await ops.open(tmp, "wx", 0o600)
    await handle.writeFile(`${JSON.stringify(value, null, 2)}\n`, "utf8")
    await handle.sync()
    await handle.close()
    handle = null
    await ops.rename(tmp, path)
    await ops.chmod(path, 0o600)
  } catch (error) {
    if (handle) await handle.close().catch(() => {})
    await ops.unlink(tmp).catch(() => {})
    throw error
  }
}

export function nextRunAfter({
  now = Date.now(),
  lastRunAt = "",
  nextRunAt = "",
  intervalMs,
  storedSignature = "",
  signature = "",
}) {
  const interval = Math.max(1_000, Number(intervalMs) || 60_000)
  if (storedSignature !== signature) return now + interval
  const saved = Date.parse(nextRunAt)
  const last = Date.parse(lastRunAt)
  if (Number.isFinite(saved)) return Math.max(saved, now)
  if (Number.isFinite(last)) return Math.max(last + interval, now)
  return now + interval
}

export function scheduleSignature(config = {}) {
  return JSON.stringify(config)
}
