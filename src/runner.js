import { execFile } from "node:child_process"
import { readFile } from "node:fs/promises"
import { join } from "node:path"
import { beijingDay, errorText, magpieConfigHome, sanitize } from "./common.js"
import { claimCommand, enqueueCommand, recordCommandResult } from "./commands.js"
import { readInventory } from "./inventory.js"
import {
  accountTag,
  appendDailyLog,
  newRunId,
  safeLogText,
} from "./log.js"
import { providerEnabled } from "./preferences.js"
import { snapshotWarning, writeDailySnapshot } from "./snapshot.js"

const ACTIONS = new Set(["detect", "checkin", "keepalive", "all", "refresh"])
const ACTION_LABELS = Object.freeze({
  detect: "检测账号",
  checkin: "全部签到",
  keepalive: "全部保活",
  all: "签到并保活",
  refresh: "刷新额度",
})
const BUILTIN_PROVIDERS = Object.freeze([
  "codex",
  "claude",
  "copilot",
  "gemini",
  "antigravity",
  "zed",
  "factory",
  "mimo",
])

let refreshRunning = false

function nowIso() {
  return new Date().toISOString()
}

function parseJsonOutput(raw) {
  const value = typeof raw === "string" ? raw.trim() : ""
  if (!value) return null
  try {
    return JSON.parse(value)
  } catch {}
  const starts = [value.indexOf("["), value.indexOf("{")].filter((index) => index >= 0)
  if (!starts.length) return null
  const first = Math.min(...starts)
  for (let end = value.length; end > first; end--) {
    const suffix = value.slice(first, end).trim()
    if (!suffix || !["}", "]"].includes(suffix.at(-1))) continue
    try {
      return JSON.parse(suffix)
    } catch {}
  }
  return null
}

function defaultRunMagpieJson(args, { timeoutMs = 120_000, command = "magpie" } = {}) {
  return new Promise((resolve) => {
    execFile(
      command,
      args,
      { timeout: timeoutMs, maxBuffer: 8 * 1024 * 1024, env: process.env },
      (error, stdout, stderr) => {
        resolve({
          ok: !error,
          exitCode: typeof error?.code === "number" ? error.code : error ? 1 : 0,
          stdout: String(stdout || ""),
          stderr: String(stderr || ""),
          error: error ? errorText(error) : "",
        })
      },
    )
  })
}

async function readJson(path, fallback) {
  try {
    return JSON.parse(await readFile(path, "utf8"))
  } catch {
    return fallback
  }
}

function checkinOutcome(row) {
  const outcome = sanitize(row?.outcome, 24)
  if (["claimed", "done"].includes(outcome)) return "success"
  if (["inactive", "ineligible", "own-app", "captcha"].includes(outcome)) return "skipped"
  return "failed"
}

const BY_PROVIDER = Object.freeze({
  trae: "trae-cn",
  minimax: "minimax-code",
  qoder: "qoder-cn",
})

function providerForRow(row, providers) {
  const by = sanitize(row?.by, 80).trim()
  if (by && BY_PROVIDER[by]) return BY_PROVIDER[by]
  const agent = sanitize(row?.agent, 80).trim()
  if (agent) return BY_PROVIDER[agent] || agent
  const vendor = sanitize(row?.vendor, 120).trim()
  if (vendor) {
    const match = providers.find((provider) =>
      provider?.id === vendor ||
      String(provider?.name || "").toLowerCase() === vendor.toLowerCase()
    )
    return match?.id || vendor
  }
  if (by.startsWith("plugin:")) return by.slice("plugin:".length)
  return "workbuddy"
}

function commandAccounts(rows, providers = []) {
  return rows.map((row) => ({
    provider: providerForRow(row, providers),
    account: sanitize(row?.user || "", 80),
    action: "checkin",
    status: checkinOutcome(row),
    outcome: sanitize(row?.outcome, 32),
    message: safeLogText(row?.msg || "", row?.user || "", 300),
    credit: Number.isFinite(row?.credit) ? row.credit : 0,
    streak: Number.isInteger(row?.streak) ? row.streak : 0,
  }))
}

function failureSummary(action, result) {
  const said = result.error || result.stderr || result.stdout || "magpie exited unsuccessfully"
  return `${ACTION_LABELS[action]}失败：${safeLogText(said, "", 300)}`
}

async function runJsonCommand({ args, config, runMagpieJson, timeoutMs = 0 }) {
  const result = await runMagpieJson(args, {
    timeoutMs: timeoutMs > 0
      ? timeoutMs
      : Math.max(120_000, Number(config.timeoutMs) * 4),
  })
  return { result, body: parseJsonOutput(result.stdout) }
}

async function snapshotProviders(config) {
  const snapshotPath = String(config.snapshotPath || "").replace(/\.js$/, ".json")
  if (!snapshotPath) return []
  const payload = await readJson(snapshotPath, null)
  return Array.isArray(payload?.providers) ? payload.providers : []
}

// Only providers with a real card are refreshed. This keeps a disabled
// provider from being asked behind the user's back and avoids asking a
// provider that has never been signed in.
export async function enabledProviderIds(config) {
  const ids = new Set()
  const configHome = config.configHome || magpieConfigHome()
  const add = (value) => {
    const id = sanitize(value, 80).trim()
    if (id && providerEnabled(config, id)) ids.add(id)
  }
  const [providers, quotas, checkins, snapshot] = await Promise.all([
    readJson(join(configHome, "plugin-providers.json"), []),
    readJson(join(configHome, "quotas.json"), {}),
    readJson(join(configHome, "plugin-checkin.json"), {}),
    snapshotProviders(config),
  ])
  for (const provider of Array.isArray(snapshot) ? snapshot : []) add(provider?.id)
  for (const provider of Array.isArray(providers) ? providers : []) {
    if (Array.isArray(provider?.accounts) && provider.accounts.length) add(provider?.id)
  }
  for (const key of Object.keys(quotas && typeof quotas === "object" ? quotas : {})) {
    add(String(key).split("/")[0])
  }
  for (const key of Object.keys(checkins && typeof checkins === "object" ? checkins : {})) {
    add(String(key).split("|")[0])
  }
  if (!ids.size && config.configHome) {
    for (const id of BUILTIN_PROVIDERS) add(id)
  }
  return [...ids]
}

function refreshSummary(success, failed, skipped) {
  const parts = []
  if (success) parts.push(`${success} 个供应商成功`)
  if (failed) parts.push(`${failed} 个失败`)
  if (skipped) parts.push(`${skipped} 个跳过`)
  if (!parts.length) parts.push("没有需要刷新的供应商")
  return `额度刷新完成：${parts.join("，")}。`
}

const NO_REFRESH_CARD = /no (?:subscription, plan or key balance|card) of/i
const REFRESH_CACHE = /not asked again before .*? have passed, shown as it was read/i

export function classifyRefreshResult(result, body) {
  const cards = Array.isArray(body) ? body : []
  const failedCards = cards.filter((card) => sanitize(card?.error, 300)).length
  const output = [result?.error, result?.stderr, result?.stdout]
    .filter(Boolean)
    .join("\n")

  if (NO_REFRESH_CARD.test(output)) {
    return {
      status: "skipped",
      outcome: "no-card",
      message: "Magpie 没有可读取的额度卡，已跳过。",
      cards,
    }
  }
  if (REFRESH_CACHE.test(output)) {
    return {
      status: "skipped",
      outcome: "cached",
      message: "Magpie 30 秒内已刷新过，本次使用缓存额度。",
      cards,
    }
  }
  if (result?.ok && cards.length > 0 && failedCards === 0) {
    return {
      status: "success",
      outcome: "refreshed",
      message: `${cards.length} 张额度卡已刷新`,
      cards,
    }
  }
  const empty = result?.ok
    ? Array.isArray(body) ? "Magpie 返回 0 张额度卡" : "Magpie 返回空额度数据（退出码 0）"
    : ""
  return {
    status: "failed",
    outcome: "failed",
    message: safeLogText(
      empty || result?.error || result?.stderr || result?.stdout || "额度刷新没有返回有效数据",
      "",
      300,
    ),
    cards,
  }
}

export async function runRefresh({
  config,
  runMagpieJson = defaultRunMagpieJson,
  provider = "",
  log = appendDailyLog,
}) {
  const startedAt = nowIso()
  const run = newRunId()
  const wanted = sanitize(provider, 80).trim()
  if (wanted && !providerEnabled(config, wanted)) {
    return {
      action: "refresh",
      run,
      status: "skipped",
      summary: "额度刷新已跳过：该供应商已停用。",
      error: "",
      accounts: [],
      startedAt,
      finishedAt: nowIso(),
    }
  }
  const providers = wanted ? [wanted] : await enabledProviderIds(config)
  if (!providers.length) {
    return {
      action: "refresh",
      run,
      status: "skipped",
      summary: "额度刷新已跳过：没有启用中的供应商。",
      error: "",
      accounts: [],
      startedAt,
      finishedAt: nowIso(),
    }
  }

  const accounts = []
  for (const id of providers) {
    const { result, body } = await runJsonCommand({
      args: ["quota", "refresh", id, "--json"],
      config,
      runMagpieJson,
      timeoutMs: Math.max(30_000, Math.min(50_000, Number(config.timeoutMs) || 50_000)),
    })
    const classified = classifyRefreshResult(result, body)
    accounts.push({
      provider: id,
      account: "",
      action: "refresh",
      status: classified.status,
      outcome: classified.outcome,
      message: classified.message,
    })
    await log({
      logPath: config.logPath,
      logMaxBytes: config.logMaxBytes,
      entry: {
        ts: nowIso(),
        day: beijingDay(),
        provider: id,
        run,
        event: "refresh",
        status: classified.status,
        outcome: classified.outcome,
        message: classified.message,
        ...(classified.status === "failed" ? { error: classified.message } : {}),
      },
    }).catch((error) => {
      console.warn(`[quota-keepalive] cannot write quota refresh log: ${errorText(error)}`)
    })
  }
  await refreshSnapshot(config, null)
  const success = accounts.filter((item) => item.status === "success").length
  const failed = accounts.filter((item) => item.status === "failed").length
  const skipped = accounts.filter((item) => item.status === "skipped").length
  return {
    action: "refresh",
    run,
    status: failed ? "failed" : success ? "success" : "skipped",
    summary: refreshSummary(success, failed, skipped),
    error: failed
      ? safeLogText(accounts.filter((item) => item.status === "failed")
          .map((item) => `${item.provider}: ${item.message}`)
          .join("; "), "", 300)
      : "",
    accounts,
    startedAt,
    finishedAt: nowIso(),
  }
}

async function logCommand(config, entry, run = newRunId()) {
  await appendDailyLog({
    logPath: config.logPath,
    logMaxBytes: config.logMaxBytes,
    entry: {
      ts: nowIso(),
      day: beijingDay(),
      provider: "daily-keepalive",
      run,
      ...entry,
    },
  })
}

async function refreshSnapshot(config, accountsData) {
  await writeDailySnapshot({
    ...(config.configHome ? { configHome: config.configHome } : {}),
    logPath: config.logPath,
    snapshotPath: config.snapshotPath,
    commandPath: config.commandPath,
    accountsData,
  }).catch((error) => {
    console.warn(`[daily-keepalive] ${snapshotWarning(error)}`)
  })
}

async function runDetect({ config, runMagpieJson }) {
  const startedAt = nowIso()
  const run = newRunId()
  const { result, body } = await runJsonCommand({
    args: ["accounts", "--json"],
    config,
    runMagpieJson,
  })
  const rows = Array.isArray(body) ? body : []
  const inventory = await readInventory({
    ...(config.configHome ? { configHome: config.configHome } : {}),
    logPath: config.logPath,
    accountsData: rows,
  })
  const failed = !result.ok || !Array.isArray(body)
  await refreshSnapshot(config, rows)
  return {
    action: "detect",
    run,
    status: failed ? "failed" : "success",
    summary: failed
      ? failureSummary("detect", result)
      : `检测完成：${inventory.providers.length} 个供应商，${inventory.accounts.length} 个账号。`,
    error: failed ? safeLogText(result.error || result.stderr, "", 300) : "",
    accounts: inventory.accounts.map((account) => ({
      provider: account.provider,
      account: account.label,
      action: "detect",
      status: "success",
      outcome: "detected",
      message: "",
    })),
    startedAt,
    finishedAt: nowIso(),
  }
}

async function runCheckin({ config, runMagpieJson, checkin, force, providers = [], accounts: onlyAccounts = [] }) {
  const startedAt = nowIso()
  const run = newRunId()
  if (typeof checkin === "function") {
    const result = await checkin({ force, providers, accounts: onlyAccounts })
    await refreshSnapshot(config, null)
    return {
      action: "checkin",
      run,
      status: result.status,
      summary: result.summary,
      error: result.status === "failed" ? safeLogText(result.summary, "", 300) : "",
      accounts: result.accounts,
      startedAt,
      finishedAt: nowIso(),
    }
  }
  const { result, body } = await runJsonCommand({
    args: ["accounts", "checkin", "--json"],
    config,
    runMagpieJson,
  })
  const rows = Array.isArray(body) ? body : []
  const inventory = await readInventory({
    ...(config.configHome ? { configHome: config.configHome } : {}),
    logPath: config.logPath,
    accountsData: [],
  })
  const accounts = commandAccounts(rows, inventory.providers)
  const failedCount = accounts.filter((account) => account.status === "failed").length
  const status = rows.length === 0 || failedCount > 0 || !result.ok ? "failed" : "success"
  const summary = rows.length === 0
    ? failureSummary("checkin", result)
    : `签到完成：${accounts.filter((account) => account.status === "success").length} 成功，${
        accounts.filter((account) => account.status === "skipped").length
      } 跳过，${failedCount} 失败。`
  for (const account of accounts) {
    await appendDailyLog({
      logPath: config.logPath,
      logMaxBytes: config.logMaxBytes,
      entry: {
        ts: nowIso(),
        day: beijingDay(),
        provider: account.provider,
        run,
        event: "checkin",
        status: account.status,
        outcome: account.outcome,
        account: accountTag(`${account.provider}|${account.account}`),
        credit: account.credit,
        streak: account.streak,
        message: account.message,
      },
    })
  }
  await refreshSnapshot(config, null)
  return {
    action: "checkin",
    run,
    status,
    summary,
    error: status === "failed" ? safeLogText(result.error || result.stderr, "", 300) : "",
    accounts,
    startedAt,
    finishedAt: nowIso(),
  }
}

async function runKeepalive({ config, keepalive, force, providers = [], accounts: onlyAccounts = [] }) {
  const startedAt = nowIso()
  const run = newRunId()
  if (typeof keepalive !== "function") {
    return {
      action: "keepalive",
      run,
      status: "failed",
      summary: "保活失败：供应商登录适配器不可用。",
      error: "provider login adapter unavailable",
      accounts: [],
      startedAt,
      finishedAt: nowIso(),
    }
  }
  const result = await keepalive({ force, providers, accounts: onlyAccounts })
  await refreshSnapshot(config, null)
  return {
    action: "keepalive",
    run,
    status: result.status,
    summary: result.summary,
    error: result.status === "failed" ? safeLogText(result.summary, "", 300) : "",
    accounts: result.accounts,
    startedAt,
    finishedAt: nowIso(),
  }
}

async function runAll({ config, runMagpieJson, checkin, keepalive, force, providers = [], accounts: onlyAccounts = [] }) {
  const startedAt = nowIso()
  const run = newRunId()
  const checked = await runCheckin({ config, runMagpieJson, checkin, force, providers, accounts: onlyAccounts })
  const sent = await runKeepalive({ config, keepalive, force, providers, accounts: onlyAccounts })
  const status = checked.status === "failed" || sent.status === "failed"
    ? "failed"
    : sent.status
  return {
    action: "all",
    run,
    status,
    summary: `${checked.summary} ${sent.summary}`,
    error: [checked.error, sent.error].filter(Boolean).join(" "),
    accounts: [...checked.accounts, ...sent.accounts],
    startedAt,
    finishedAt: nowIso(),
  }
}

export async function runCommand({
  action,
  config,
  runMagpieJson = defaultRunMagpieJson,
  checkin = null,
  keepalive = null,
  force = false,
  provider = "",
  account = "",
}) {
  if (!ACTIONS.has(action)) throw new Error(`unknown command action: ${action}`)
  if (action === "refresh") {
    if (refreshRunning) {
      return {
        action: "refresh",
        run: newRunId(),
        status: "skipped",
        summary: "额度刷新已跳过：上一轮刷新仍在执行。",
        error: "",
        accounts: [],
        startedAt: nowIso(),
        finishedAt: nowIso(),
      }
    }
    refreshRunning = true
    try {
      return await runRefresh({ config, runMagpieJson, provider })
    } finally {
      refreshRunning = false
    }
  }
  const runners = { detect: runDetect, checkin: runCheckin, keepalive: runKeepalive, all: runAll }
  const providers = provider ? [provider] : []
  const accounts = account ? [account] : []
  return runners[action]({ config, runMagpieJson, checkin, keepalive, force, providers, accounts })
}

export async function consumePendingCommand({
  config,
  rawOptions = {},
  runMagpieJson = defaultRunMagpieJson,
  checkin = null,
  keepalive = null,
}) {
  const id = sanitize(rawOptions.pendingId, 80)
  const action = sanitize(rawOptions.pendingAction, 24)
  const provider = sanitize(rawOptions.pendingProvider, 80)
  const account = sanitize(rawOptions.pendingAccount, 80)
  if (!id || !ACTIONS.has(action)) return null
  const queued = await enqueueCommand({
    commandPath: config.commandPath,
    action,
    source: "page",
    id,
    provider,
  })
  if (queued?.busy) {
    return { id, action, status: queued.command?.status || "queued", busy: true }
  }
  const command = await claimCommand({ commandPath: config.commandPath, id })
  if (!command || command.action !== action) return null

  void (async () => {
    let outcome
    try {
      outcome = await runCommand({
        action,
        config,
        runMagpieJson,
        checkin,
        keepalive,
        force: true,
        provider,
        account,
      })
      await recordCommandResult({
        commandPath: config.commandPath,
        id,
        status: outcome.status,
        summary: outcome.summary,
        error: outcome.error,
        provider,
        accounts: outcome.accounts,
      })
      await logCommand(config, {
        event: action,
        status: outcome.status,
        outcome: outcome.status,
        message: outcome.summary,
        error: outcome.error,
      }, outcome.run)
    } catch (error) {
      const said = safeLogText(errorText(error), "", 300)
      await recordCommandResult({
        commandPath: config.commandPath,
        id,
        status: "failed",
        summary: `${ACTION_LABELS[action]}失败：${said}`,
        error: said,
      }).catch(() => {})
      await logCommand(config, {
        event: action,
        status: "failed",
        outcome: "failed",
        error: said,
      }, outcome?.run)
    } finally {
      await refreshSnapshot(config, null)
    }
  })()

  return { id, action, status: "running" }
}

export { defaultRunMagpieJson, parseJsonOutput }
