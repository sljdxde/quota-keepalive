import { resolveKeepaliveOptions } from "./src/config.js"
import { beijingDay } from "./src/common.js"
import {
  formatLogRecords,
  readDailyLogs,
} from "./src/log.js"
import { appendDailyLog, newRunId } from "./src/log.js"
import { readLivePluginOptions } from "./src/live-options.js"
import { consumePendingCommand, runCommand } from "./src/runner.js"
import { snapshotWarning, writeDailySnapshot } from "./src/snapshot.js"
import { createProviderCheckin } from "./src/checkin-runner.js"
import { createWorkBuddyKeepalive } from "./src/workbuddy.js"
import { readCommand } from "./src/commands.js"

const LOG_STATUSES = new Set(["started", "success", "failed", "skipped"])
const SCHEDULER = Symbol.for("quota-keepalive.scheduler")
const OPTIONS_WATCHER = Symbol.for("quota-keepalive.options-watcher")
const QUOTA_REFRESHER = Symbol.for("quota-keepalive.quota-refresher")
const REFRESH_INTERVAL_MS = 60_000

export function stopScheduler() {
  const previous = globalThis[SCHEDULER]
  if (previous?.timer) clearInterval(previous.timer)
  if (previous) delete globalThis[SCHEDULER]
}

export function startScheduler({ config, checkin, keepalive }) {
  // Every behavior setting must participate in the signature. Otherwise a
  // model or provider/account preference change would leave the running timer
  // on the previous closures and configuration.
  const signature = JSON.stringify(config)
  const previous = globalThis[SCHEDULER]
  if (previous?.signature === signature) return
  stopScheduler()
  if (!config.scheduleEnabled) return
  let running = false
  const timer = setInterval(async () => {
    if (running) return
    running = true
    try {
      const result = await runCommand({
        action: config.scheduleAction,
        config,
        checkin,
        keepalive,
        force: false,
      })
      await appendDailyLog({
        logPath: config.logPath,
        logMaxBytes: config.logMaxBytes,
        entry: {
          ts: new Date().toISOString(),
          day: beijingDay(),
          provider: "quota-keepalive",
          run: result.run || newRunId(),
          event: "schedule",
          status: result.status,
          outcome: result.status,
          message: result.summary,
        },
      })
      await writeDailySnapshot({
        ...(config.configHome ? { configHome: config.configHome } : {}),
        logPath: config.logPath,
        snapshotPath: config.snapshotPath,
        commandPath: config.commandPath,
      })
    } catch (error) {
      await appendDailyLog({
        logPath: config.logPath,
        logMaxBytes: config.logMaxBytes,
        entry: {
          ts: new Date().toISOString(),
          day: beijingDay(),
          provider: "quota-keepalive",
          run: newRunId(),
          event: "schedule",
          status: "failed",
          outcome: "failed",
          error: error instanceof Error ? error.message : String(error),
        },
      })
    } finally {
      running = false
    }
  }, config.scheduleMinutes * 60_000)
  timer.unref?.()
  globalThis[SCHEDULER] = { timer, signature }
}

export function stopOptionsWatcher() {
  const previous = globalThis[OPTIONS_WATCHER]
  if (previous?.timer) clearInterval(previous.timer)
  if (previous) delete globalThis[OPTIONS_WATCHER]
}

export function stopQuotaRefresher() {
  const previous = globalThis[QUOTA_REFRESHER]
  if (previous?.timer) clearInterval(previous.timer)
  if (previous?.initial) clearTimeout(previous.initial)
  if (previous) delete globalThis[QUOTA_REFRESHER]
}

export async function refreshQuotasNow({
  config,
  run = runCommand,
  command = readCommand,
  log = appendDailyLog,
}) {
  const current = await command(config.commandPath).catch(() => null)
  if (current && ["queued", "running"].includes(current.status)) {
    return { status: "skipped", summary: "额度刷新已跳过：已有任务正在执行。" }
  }
  let result
  try {
    result = await run({ action: "refresh", config })
  } catch (error) {
    result = {
      run: newRunId(),
      status: "failed",
      summary: `额度刷新失败：${error?.message || error}`,
      error: error?.message || String(error),
    }
  }
  await log({
    logPath: config.logPath,
    logMaxBytes: config.logMaxBytes,
    entry: {
      ts: new Date().toISOString(),
      day: beijingDay(),
      provider: "quota-keepalive",
      run: result.run || newRunId(),
      event: "refresh",
      status: result.status,
      outcome: result.status,
      message: result.summary,
      ...(result.error ? { error: result.error } : {}),
    },
  }).catch(() => {})
  await writeDailySnapshot({
    ...(config.configHome ? { configHome: config.configHome } : {}),
    logPath: config.logPath,
    snapshotPath: config.snapshotPath,
    commandPath: config.commandPath,
  }).catch((error) => {
    console.warn(`[quota-keepalive] ${snapshotWarning(error)}`)
  })
  return result
}

export function startQuotaRefresher({
  config,
  refresh = refreshQuotasNow,
  intervalMs = REFRESH_INTERVAL_MS,
  firstDelayMs = 30_000,
}) {
  const signature = JSON.stringify([
    config.configHome || "",
    config.commandPath,
    config.providerPreferences,
  ])
  const previous = globalThis[QUOTA_REFRESHER]
  if (previous?.signature === signature) return
  stopQuotaRefresher()

  let running = false
  const tick = async () => {
    if (running) return
    running = true
    try {
      await refresh({ config })
    } catch (error) {
      console.warn(`[quota-keepalive] cannot refresh quotas: ${error?.message || error}`)
    } finally {
      running = false
    }
  }
  const initial = setTimeout(tick, firstDelayMs)
  const timer = setInterval(tick, intervalMs)
  initial.unref?.()
  timer.unref?.()
  globalThis[QUOTA_REFRESHER] = { timer, initial, signature }
}

export function startOptionsWatcher({
  input,
  rawOptions,
  config,
  readOptions = readLivePluginOptions,
  start = startScheduler,
  consume = consumePendingCommand,
  makeCheckin = createProviderCheckin,
  makeKeepalive = createWorkBuddyKeepalive,
  startRefresh = startQuotaRefresher,
}) {
  stopOptionsWatcher()
  if (!config.watchOptions) return
  let running = false
  const timer = setInterval(async () => {
    if (running) return
    running = true
    try {
      const liveOptions = await readOptions(config.configHome)
      const liveRaw = { ...rawOptions, ...liveOptions }
      const liveConfig = resolveKeepaliveOptions(liveRaw)
      const checkin = makeCheckin({ input, config: liveConfig })
      const keepalive = makeKeepalive({ input, config: liveConfig })
      start({ config: liveConfig, checkin, keepalive })
      startRefresh({ config: liveConfig })
      await consume({
        config: liveConfig,
        rawOptions: liveRaw,
        checkin,
        keepalive,
      })
    } catch (error) {
      console.warn(`[quota-keepalive] cannot read live options: ${error?.message || error}`)
    } finally {
      running = false
    }
  }, 2_000)
  timer.unref?.()
  globalThis[OPTIONS_WATCHER] = { timer }
}

export function logTool(config) {
  return {
    logs: {
      description:
        "Reads recent quota check-in and keepalive logs. Logs use hashed account tags and never include tokens, full account ids, or model replies.",
      args: {
        type: "object",
        properties: {
          limit: {
            type: "integer",
            minimum: 1,
            maximum: 200,
            default: 50,
            description: "Number of most recent matching records to return.",
          },
          day: {
            type: "string",
            pattern: "^\\d{4}-\\d{2}-\\d{2}$",
            description: "Only return records for this Beijing date (YYYY-MM-DD).",
          },
          status: {
            type: "string",
            enum: ["started", "success", "failed", "skipped"],
            description: "Only return records with this status.",
          },
          file: {
            type: "string",
            enum: ["all", "current", "previous"],
            default: "all",
            description: "Read the current log, its previous rotated log, or both.",
          },
        },
        additionalProperties: false,
      },
      async execute(args = {}) {
        const limit = args.limit === undefined ? 50 : Number(args.limit)
        if (!Number.isInteger(limit) || limit < 1 || limit > 200) {
          throw new Error("limit must be an integer from 1 to 200")
        }
        if (args.day && !/^\d{4}-\d{2}-\d{2}$/.test(args.day)) {
          throw new Error("day must be YYYY-MM-DD")
        }
        if (args.status && !LOG_STATUSES.has(args.status)) {
          throw new Error("status must be started, success, failed, or skipped")
        }
        const file = args.file || "all"
        if (!["all", "current", "previous"].includes(file)) {
          throw new Error("file must be all, current, or previous")
        }

        const previousOnly = file === "previous"
        const result = await readDailyLogs({
          logPath: previousOnly ? `${config.logPath}.1` : config.logPath,
          includePrevious: file === "all",
          day: args.day || "",
          status: args.status || "",
          limit,
        })
        if (!result.records.length) {
          return `No quota keepalive log records matched. Looked in:\n${result.files.join("\n")}`
        }
        const warning = result.malformed
          ? `\nSkipped ${result.malformed} malformed or unrelated line(s).`
          : ""
        return `Quota Keepalive logs (${result.records.length} records, Beijing time)${warning}\n${
          formatLogRecords(result.records)
        }`
      },
    },
  }
}

// This plugin deliberately declares no provider/auth hook. The installed
// provider plugins keep owning sign-in, model lists, quota and check-in for
// WorkBuddy, CodeArts, MiniMax and every other provider. This plugin only
// orchestrates their tasks, so Magpie's plugin card stays provider-neutral.
export async function QuotaKeepalivePlugin(input, rawOptions = {}) {
  const config = resolveKeepaliveOptions(rawOptions)
  const checkin = createProviderCheckin({ input, config })
  const keepalive = createWorkBuddyKeepalive({ input, config })
  startScheduler({ config, checkin, keepalive })
  startQuotaRefresher({ config })
  startOptionsWatcher({ input, rawOptions, config })

  await consumePendingCommand({ config, rawOptions, checkin, keepalive }).catch((error) => {
    console.warn(`[quota-keepalive] cannot run the page command: ${error?.message || error}`)
  })
  await writeDailySnapshot({
    ...(config.configHome ? { configHome: config.configHome } : {}),
    logPath: config.logPath,
    snapshotPath: config.snapshotPath,
    commandPath: config.commandPath,
  }).catch((error) => {
    console.warn(`[quota-keepalive] ${snapshotWarning(error)}`)
  })

  return {
    tool: logTool(config),
  }
}

// Magpie treats every named export as a plugin entry unless the default
// export identifies one server. Keep testable helpers named while exposing
// only QuotaKeepalivePlugin to the host.
export default {
  id: "quota-keepalive",
  server: QuotaKeepalivePlugin,
}
