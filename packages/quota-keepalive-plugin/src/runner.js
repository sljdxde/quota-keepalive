import { execFile } from "node:child_process"
import { beijingDay, errorText, sanitize } from "./common.js"
import { claimCommand, enqueueCommand, recordCommandResult } from "./commands.js"
import { readInventory } from "./inventory.js"
import {
  accountTag,
  appendDailyLog,
  newRunId,
  safeLogText,
} from "./log.js"
import { snapshotWarning, writeDailySnapshot } from "./snapshot.js"

const ACTIONS = new Set(["detect", "checkin", "keepalive", "all"])
const ACTION_LABELS = Object.freeze({
  detect: "检测账号",
  checkin: "全部签到",
  keepalive: "全部保活",
  all: "签到并保活",
})

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

function defaultRunMagpieJson(args, { timeoutMs = 120_000 } = {}) {
  return new Promise((resolve) => {
    execFile(
      "magpie",
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

async function runJsonCommand({ args, config, runMagpieJson }) {
  const result = await runMagpieJson(args, {
    timeoutMs: Math.max(120_000, Number(config.timeoutMs) * 4),
  })
  return { result, body: parseJsonOutput(result.stdout) }
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
  const inventory = await readInventory({ logPath: config.logPath, accountsData: rows })
  const failed = !result.ok && rows.length === 0
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

async function runCheckin({ config, runMagpieJson }) {
  const startedAt = nowIso()
  const run = newRunId()
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
  }).catch(() => ({ providers: [] }))
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

async function runKeepalive({ config, keepalive }) {
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
  const result = await keepalive({ force: true })
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

async function runAll({ config, runMagpieJson, keepalive }) {
  const startedAt = nowIso()
  const run = newRunId()
  const checkin = await runCheckin({ config, runMagpieJson })
  const sent = await runKeepalive({ config, keepalive })
  const status = checkin.status === "failed" || sent.status === "failed"
    ? "failed"
    : sent.status
  return {
    action: "all",
    run,
    status,
    summary: `${checkin.summary} ${sent.summary}`,
    error: [checkin.error, sent.error].filter(Boolean).join(" "),
    accounts: [...checkin.accounts, ...sent.accounts],
    startedAt,
    finishedAt: nowIso(),
  }
}

export async function runCommand({
  action,
  config,
  runMagpieJson = defaultRunMagpieJson,
  keepalive = null,
}) {
  if (!ACTIONS.has(action)) throw new Error(`unknown command action: ${action}`)
  const runners = { detect: runDetect, checkin: runCheckin, keepalive: runKeepalive, all: runAll }
  return runners[action]({ config, runMagpieJson, keepalive })
}

export async function consumePendingCommand({
  config,
  rawOptions = {},
  runMagpieJson = defaultRunMagpieJson,
  keepalive = null,
}) {
  const id = sanitize(rawOptions.pendingId, 80)
  const action = sanitize(rawOptions.pendingAction, 24)
  if (!id || !ACTIONS.has(action)) return null
  const queued = await enqueueCommand({
    commandPath: config.commandPath,
    action,
    source: "page",
    id,
  })
  if (queued?.busy) {
    return { id, action, status: queued.command?.status || "queued", busy: true }
  }
  const command = await claimCommand({ commandPath: config.commandPath, id })
  if (!command || command.action !== action) return null

  void (async () => {
    let outcome
    try {
      outcome = await runCommand({ action, config, runMagpieJson, keepalive })
      await recordCommandResult({
        commandPath: config.commandPath,
        id,
        status: outcome.status,
        summary: outcome.summary,
        error: outcome.error,
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
      }, outcome?.run).catch(() => {})
    } finally {
      await refreshSnapshot(config, null)
    }
  })()

  return { id, action, status: "running" }
}

export { defaultRunMagpieJson, parseJsonOutput }
