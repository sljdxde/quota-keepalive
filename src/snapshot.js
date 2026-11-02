import { randomBytes } from "node:crypto"
import { chmod, mkdir, open, readFile, rename, unlink } from "node:fs/promises"
import { dirname, isAbsolute, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { beijingDay, errorText, sanitize } from "./common.js"
import { readInventory } from "./inventory.js"
import { readDailyLogs } from "./log.js"
import { readCommand } from "./commands.js"

const MAX_READ_RECORDS = 2_500
const MAX_SNAPSHOT_RECORDS = 120
const MAX_RUN_GROUPS = 16
const MAX_TODAY_RUNS = 400
const ACCOUNT = /^acct-[a-f0-9]{12}$/
const SNAPSHOT_VARIABLE = "DailyKeepaliveSnapshot"
const SUMMARY_PROVIDERS = new Set(["daily-keepalive", "quota-keepalive"])
const RUN_EVENTS = new Set(["detect", "checkin", "keepalive", "all", "schedule", "refresh"])
const PAGE_LOG_EVENTS = new Set([
  "started",
  "checkin",
  "keepalive",
  "keepalive-model",
  "finished",
  "all",
  "schedule",
])
const FILE_OPS = { chmod, mkdir, open, rename, unlink }
const FIELD_LIMITS = {
  ts: 40,
  day: 10,
  event: 20,
  status: 12,
  outcome: 24,
  model: 160,
  probeMode: 16,
  account: 24,
  run: 48,
}

export function defaultSnapshotPath() {
  return fileURLToPath(new URL("../ui/data.js", import.meta.url))
}

function safeField(record, name) {
  const value = record?.[name]
  if (typeof value !== "string" || !value) return ""
  return sanitize(value, FIELD_LIMITS[name] || 80)
}

export function publicRecord(record) {
  const out = {}
  for (const name of ["ts", "day", "provider", "event", "status", "outcome", "model", "probeMode", "account", "run"]) {
    const value = safeField(record, name)
    if (value) out[name] = value
  }
  if (out.account && !ACCOUNT.test(out.account)) delete out.account
  for (const name of ["attempts", "inputTokens", "credit", "streak", "durationMs", "retry", "persisted", "keepalive", "activityVerified"]) {
    const value = record?.[name]
    if (value !== undefined && value !== null && (typeof value === "number" || typeof value === "boolean")) {
      out[name] = value
    }
  }
  for (const name of ["message", "error"]) {
    if (typeof record?.[name] === "string" && record[name]) {
      out[name] = sanitize(record[name], 480)
    }
  }
  return out
}

export function publicCommand(command) {
  if (!command || typeof command !== "object" || Array.isArray(command)) return null
  const out = {
    id: sanitize(command.id, 80),
    action: sanitize(command.action, 24),
    provider: sanitize(command.provider, 80),
    status: sanitize(command.status, 24),
    createdAt: sanitize(command.createdAt, 40),
    startedAt: sanitize(command.startedAt, 40),
    finishedAt: sanitize(command.finishedAt, 40),
    summary: sanitize(command.summary, 480),
    error: sanitize(command.error, 480),
    accounts: (Array.isArray(command.accounts) ? command.accounts : []).slice(0, 80).map((account) => ({
      provider: sanitize(account?.provider, 80),
      account: sanitize(account?.account, 80),
      action: sanitize(account?.action, 24),
      status: sanitize(account?.status, 24),
      outcome: sanitize(account?.outcome, 32),
      message: sanitize(account?.message, 300),
    })),
  }
  if (!out.id || !out.action || !out.status) return null
  return out
}

function childEventsFor(event) {
  if (event === "checkin") return new Set(["checkin"])
  if (event === "keepalive") return new Set(["keepalive", "keepalive-model"])
  if (event === "refresh") return new Set(["refresh"])
  if (event === "all" || event === "schedule") {
    return new Set(["checkin", "keepalive", "keepalive-model"])
  }
  return new Set()
}

function orphanKeepaliveGroups(source, consumed) {
  const groups = []
  for (const start of source) {
    if (
      consumed.has(start) ||
      start?.event !== "started" ||
      start?.keepalive !== true ||
      typeof start?.run !== "string" ||
      !start.run
    ) continue
    const children = source.filter((record) =>
      !consumed.has(record) &&
      record?.run === start.run &&
      ["started", "keepalive", "keepalive-model"].includes(record?.event)
    )
    const finals = children.filter((record) => record?.event === "keepalive")
    let final = null
    for (const record of finals) {
      if (!final || String(record.ts || "") >= String(final.ts || "")) final = record
    }
    if (!final) continue
    for (const child of children) consumed.add(child)
    const startedAt = Date.parse(start.ts)
    const finishedAt = Date.parse(final.ts)
    const providers = [...new Set(children.map((record) => record.provider).filter(Boolean))]
    const accounts = new Set(children.map((record) => record.account).filter(Boolean))
    groups.push({
      id: start.run,
      at: final.ts,
      day: final.day || start.day,
      event: "keepalive",
      status: final.status,
      outcome: final.outcome,
      message: final.message || final.error ||
        (final.status === "success" ? "保活成功。" : "保活失败。"),
      run: start.run,
      durationMs: Number.isFinite(startedAt) && Number.isFinite(finishedAt)
        ? Math.max(0, finishedAt - startedAt)
        : 0,
      providers,
      accounts: accounts.size,
      successes: finals.filter((record) => record.status === "success").length,
      failures: finals.filter((record) => record.status === "failed").length,
      children,
    })
  }
  return groups
}

function groupRunHistory(records) {
  const source = Array.isArray(records) ? records : []
  const anchors = source
    .filter((record) =>
      SUMMARY_PROVIDERS.has(record?.provider) &&
      RUN_EVENTS.has(record?.event) &&
      typeof record?.ts === "string" &&
      Number.isFinite(Date.parse(record.ts))
    )
    .sort((left, right) => String(left.ts).localeCompare(String(right.ts)))
  const consumed = new Set()
  const groups = []
  let previousAt = 0

  for (const anchor of anchors) {
    const at = Date.parse(anchor.ts)
    const windowStart = Math.max(previousAt, at - 15 * 60_000)
    const allowed = childEventsFor(anchor.event)
    const children = allowed.size
      ? source.filter((record) => {
          if (record === anchor || consumed.has(record) || !allowed.has(record?.event)) return false
          if (SUMMARY_PROVIDERS.has(record?.provider)) return false
          if (anchor.day && record.day !== anchor.day) return false
          const childAt = Date.parse(record.ts)
          if (!Number.isFinite(childAt)) return false
          if (childAt < windowStart || childAt > at + 5_000) return false
          consumed.add(record)
          return true
        })
      : []
    const providers = new Set(children.map((record) => record.provider).filter(Boolean))
    const accounts = new Set(children.map((record) => record.account).filter(Boolean))
    const successes = children.filter((record) => record.status === "success").length
    const failures = children.filter((record) => record.status === "failed").length
    groups.push({
      id: anchor.run || `${anchor.ts}|${anchor.event}`,
      at: anchor.ts,
      day: anchor.day,
      event: anchor.event,
      status: anchor.status,
      outcome: anchor.outcome,
      message: anchor.message || anchor.error || "",
      run: anchor.run || "",
      durationMs: anchor.durationMs || 0,
      providers: [...providers],
      accounts: accounts.size,
      successes,
      failures,
      children,
    })
    if (Number.isFinite(at)) previousAt = at
  }
  groups.push(...orphanKeepaliveGroups(source, consumed))
  return groups
    .sort((left, right) => (Date.parse(right.at) || 0) - (Date.parse(left.at) || 0))
}

function publicRunGroup(group) {
  const { children, ...summary } = group
  return summary
}

export function buildRunHistory(records) {
  return groupRunHistory(records).slice(0, MAX_RUN_GROUPS)
}

export function buildTodayRunHistory(records, day = beijingDay()) {
  return groupRunHistory(records)
    .filter((group) => group.day === day)
    .slice(0, MAX_TODAY_RUNS)
    .map(publicRunGroup)
}

export async function readPreviousSnapshot(snapshotPath) {
  const path = resolve(snapshotPath)
  const jsonPath = path.replace(/\.js$/, ".json")
  try {
    return JSON.parse(await readFile(jsonPath, "utf8"))
  } catch {}
  try {
    const text = await readFile(path, "utf8")
    const prefix = `window.${SNAPSHOT_VARIABLE} = `
    if (!text.startsWith(prefix)) return null
    return JSON.parse(text.slice(prefix.length).replace(/;\s*$/, ""))
  } catch {
    return null
  }
}

export function carryForwardQuotas(inventory, previous) {
  if (!inventory || !previous || !Array.isArray(previous.providers)) return inventory
  const old = new Map()
  for (const provider of previous.providers) {
    if (!provider || !Array.isArray(provider.accounts)) continue
    for (const account of provider.accounts) {
      if (!account?.label || !account?.quota) continue
      old.set(`${provider.id || provider.name}\0${String(account.label).toLowerCase()}`, account.quota)
    }
  }
  for (const provider of inventory.providers || []) {
    if (Array.isArray(provider.accounts)) {
      for (const account of provider.accounts) {
        if (account?.quota) continue
        account.quota = old.get(`${provider.id || provider.name}\0${String(account?.label || "").toLowerCase()}`) || null
      }
    }
  }
  inventory.accounts = (inventory.providers || []).flatMap((provider) =>
    (Array.isArray(provider.accounts) ? provider.accounts : []).map((account) => ({
      provider: provider.id,
      providerName: provider.name,
      ...account,
    })),
  )
  return inventory
}

export async function writeAtomic(path, content, ops = FILE_OPS) {
  await ops.mkdir(dirname(path), { recursive: true, mode: 0o700 })
  const tmp = `${path}.tmp-${process.pid}-${randomBytes(6).toString("hex")}`
  let handle
  try {
    handle = await ops.open(tmp, "wx", 0o600)
    await handle.writeFile(content, "utf8")
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

export async function writeDailySnapshot(options = {}) {
  const {
    logPath,
    snapshotPath = defaultSnapshotPath(),
    configHome = undefined,
    accountsData = null,
    quotaCards = [],
    commandPath = undefined,
  } = options
  const writeCompanion = options.writeCompanion ??
    (resolve(snapshotPath) === defaultSnapshotPath())
  if (typeof logPath !== "string" || !isAbsolute(logPath)) {
    throw new Error("logPath must be absolute")
  }
  if (typeof snapshotPath !== "string" || !isAbsolute(snapshotPath)) {
    throw new Error("snapshotPath must be absolute")
  }
  const source = resolve(logPath)
  const result = await readDailyLogs({
    logPath: source,
    includePrevious: true,
    limit: MAX_READ_RECORDS,
  })
  const records = result.records
    .filter((record) => PAGE_LOG_EVENTS.has(record?.event))
    .map(publicRecord)
  let inventory = await readInventory({
    ...(configHome ? { configHome } : {}),
    logPath,
    records,
    accountsData,
    quotaCards,
  })
  if (!Array.isArray(accountsData)) {
    inventory = carryForwardQuotas(inventory, await readPreviousSnapshot(snapshotPath))
  }
  const command = commandPath ? publicCommand(await readCommand(commandPath)) : null
  const today = beijingDay()
  const payload = {
    version: 5,
    generatedAt: new Date().toISOString(),
    timezone: "Asia/Shanghai",
    today,
    logEnabled: true,
    logFile: "daily-keepalive.log.jsonl",
    rotated: result.rotated,
    malformed: result.malformed,
    records: records.slice(-MAX_SNAPSHOT_RECORDS),
    summary: summarize(records, inventory.accounts),
    providers: inventory.providers,
    accounts: inventory.accounts,
    command,
    runHistory: buildRunHistory(records),
    todayRuns: buildTodayRunHistory(records, today),
  }
  await writeAtomic(
    resolve(snapshotPath),
    `window.${SNAPSHOT_VARIABLE} = ${JSON.stringify(payload, null, 2)};\n`,
  )
  if (writeCompanion) {
    await writeAtomic(
      resolve(snapshotPath).replace(/\.js$/, ".json"),
      `${JSON.stringify(payload, null, 2)}\n`,
    )
  }
  return payload
}

export function snapshotWarning(error) {
  return `cannot update the daily keepalive page snapshot: ${sanitize(errorText(error), 260)}`
}

export function summarize(records, accounts = []) {
  const today = beijingDay()
  const source = Array.isArray(records) ? records : []
  const accountList = Array.isArray(accounts) ? accounts : []
  const out = {
    today: {
      checkin: { success: 0, skipped: 0, failed: 0 },
      keepalive: { success: 0, skipped: 0, failed: 0 },
      accounts: accountList.length,
      checkinDone: 0,
      keepaliveDone: 0,
      failed: 0,
    },
    latestAt: "",
    failures: 0,
  }
  const keepaliveAccounts = new Set()
  for (const record of source) {
    if (!record || typeof record !== "object") continue
    if (record.status === "failed") out.failures++
    if (!record.ts || !Number.isFinite(Date.parse(record.ts))) continue
    if (record.ts > out.latestAt) out.latestAt = record.ts
    if (record.day !== today) continue
    const kind = record.event === "keepalive" ? "keepalive" : record.event === "checkin" ? "checkin" : ""
    if (!kind) continue
    if (kind === "keepalive" && record.status === "success" && record.account) {
      keepaliveAccounts.add(record.account)
    }
  }
  const failedAccounts = new Set()
  for (const account of accountList) {
    if (!account || typeof account !== "object") continue
    const active = account?.activity?.today
    const key = `${account?.provider || ""}|${account?.label || account?.index || ""}`
    if (active?.checkin?.success) out.today.checkinDone++
    else if (active?.checkin?.status === "failed") failedAccounts.add(`checkin:${key}`)
    if (active?.keepalive?.success) out.today.keepaliveDone++
    else if (active?.keepalive?.status === "failed") failedAccounts.add(`keepalive:${key}`)
    if (active?.checkin?.success) {
      out.today.checkin.success++
    } else if (active?.checkin?.status === "skipped") {
      out.today.checkin.skipped++
    } else if (active?.checkin?.status === "failed") {
      out.today.checkin.failed++
    }
    if (active?.keepalive?.success) {
      out.today.keepalive.success++
    } else if (active?.keepalive?.status === "skipped") {
      out.today.keepalive.skipped++
    } else if (active?.keepalive?.status === "failed") {
      out.today.keepalive.failed++
    }
  }
  out.today.failed = failedAccounts.size
  out.today.keepaliveDone = Math.max(out.today.keepaliveDone, keepaliveAccounts.size)
  return out
}
