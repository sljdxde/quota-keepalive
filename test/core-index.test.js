import { afterEach, describe, expect, test } from "bun:test"
import { mkdir, writeFile } from "node:fs/promises"
import { join } from "node:path"
import {
  QuotaKeepalivePlugin,
  logTool,
  refreshQuotasNow,
  startOptionsWatcher,
  startQuotaRefresher,
  startScheduler,
  stopOptionsWatcher,
  stopQuotaRefresher,
  stopScheduler,
} from "../index.js"
import pluginModule from "../index.js"
import { PLUGIN_DIR } from "../src/live-options.js"
import { resolveKeepaliveOptions } from "../src/config.js"
import { appendDailyLog, readDailyLogs } from "../src/log.js"
import { readSchedule, scheduleSignature } from "../src/schedule.js"
import { tempDir } from "./support.js"

const cleanups = []
const realSetInterval = globalThis.setInterval
const realClearInterval = globalThis.clearInterval
const realSetTimeout = globalThis.setTimeout
const realClearTimeout = globalThis.clearTimeout
afterEach(async () => {
  stopScheduler()
  stopOptionsWatcher()
  stopQuotaRefresher()
  globalThis.setInterval = realSetInterval
  globalThis.clearInterval = realClearInterval
  globalThis.setTimeout = realSetTimeout
  globalThis.clearTimeout = realClearTimeout
  while (cleanups.length) {
    const cleanup = cleanups.pop()
    if (cleanup) await cleanup()
  }
})

async function dir(prefix = "quota-index-") {
  const item = await tempDir(prefix)
  cleanups.push(item.cleanup)
  return item
}

function installIntervalCapture() {
  const calls = []
  globalThis.setInterval = (callback, ms) => {
    const timer = {
      callback,
      ms,
      cleared: false,
      unrefCalled: false,
      unref() { this.unrefCalled = true },
    }
    calls.push(timer)
    return timer
  }
  globalThis.clearInterval = (timer) => {
    if (timer) timer.cleared = true
  }
  return calls
}

function installTimeoutCapture() {
  const calls = []
  globalThis.setTimeout = (callback, ms) => {
    const timer = {
      callback,
      ms,
      cleared: false,
      unrefCalled: false,
      unref() { this.unrefCalled = true },
    }
    calls.push(timer)
    return timer
  }
  globalThis.clearTimeout = (timer) => {
    if (timer) timer.cleared = true
  }
  return calls
}

function baseConfig(tmp, overrides = {}) {
  return resolveKeepaliveOptions({
    configHome: tmp.path("config"),
    statePath: tmp.path("state.json"),
    logPath: tmp.path("daily.log.jsonl"),
    snapshotPath: tmp.path("ui", "data.js"),
    commandPath: tmp.path("command.json"),
    schedulePath: tmp.path("schedule.json"),
    watchOptions: false,
    scheduleEnabled: false,
    ...overrides,
  })
}

describe("log MCP tool", () => {
  test("validates arguments, filters files, and reports empty or malformed logs", async () => {
    const tmp = await dir()
    const logPath = tmp.path("daily.log.jsonl")
    const tool = logTool({ logPath }).logs
    expect(tool.description).toContain("hashed account tags")
    const empty = await tool.execute({ file: "previous" })
    expect(empty).toContain("No quota keepalive log records matched")
    for (const args of [
      { limit: 0 },
      { limit: 201 },
      { limit: 1.5 },
      { day: "30-10-2026" },
      { status: "other" },
      { file: "other" },
    ]) {
      await expect(tool.execute(args)).rejects.toBeInstanceOf(Error)
    }
    await appendDailyLog({
      logPath,
      entry: {
        ts: "2026-10-30T01:00:00.000Z",
        day: "2026-10-30",
        provider: "workbuddy",
        event: "checkin",
        status: "success",
        message: "ok",
      },
    })
    await writeFile(logPath, `${await (await import("node:fs/promises")).readFile(logPath, "utf8")}bad\n`, "utf8")
    const output = await tool.execute({ day: "2026-10-30", status: "success", limit: "10" })
    expect(output).toContain("Skipped 1 malformed")
    expect(output).toContain("provider=workbuddy")
    await expect(tool.execute({ limit: "bad" })).rejects.toThrow("integer")
  })
})

describe("scheduler", () => {
  test("deduplicates signatures, clears old timers, and runs once at a time", async () => {
    const timers = installTimeoutCapture()
    const tmp = await dir()
    const config = baseConfig(tmp)
    let release
    let runs = 0
    const keepalive = async () => {
      runs++
      await new Promise((resolve) => { release = resolve })
      return { status: "success", summary: "完成", accounts: [] }
    }
    await startScheduler({ config, keepalive })
    expect(timers).toHaveLength(0)
    const enabled = { ...config, scheduleEnabled: true, scheduleAction: "keepalive", scheduleMinutes: 5 }
    await startScheduler({ config: enabled, keepalive })
    expect(timers).toHaveLength(1)
    expect(timers[0].ms).toBeGreaterThan(299_000)
    expect(timers[0].ms).toBeLessThanOrEqual(300_000)
    expect(timers[0].unrefCalled).toBe(true)
    await startScheduler({ config: enabled, keepalive })
    expect(timers).toHaveLength(1)

    const first = timers[0].callback()
    const second = timers[0].callback()
    await second
    expect(runs).toBe(1)
    release()
    await first
    expect((await readDailyLogs({ logPath: config.logPath })).records.some((record) => record.event === "schedule")).toBe(true)
    expect(timers).toHaveLength(2)
    expect((await readSchedule(config.schedulePath)).lastStatus).toBe("success")

    const changed = { ...enabled, scheduleMinutes: 10 }
    await startScheduler({ config: changed, keepalive })
    expect(timers).toHaveLength(3)
    expect(timers[1].cleared).toBe(true)

    const modelChanged = {
      ...changed,
      modelPreferences: { workbuddy: "hy3" },
    }
    await startScheduler({ config: modelChanged, checkin: null, keepalive })
    expect(timers).toHaveLength(4)
    expect(timers[2].cleared).toBe(true)
    stopScheduler()
    expect(timers[3].cleared).toBe(true)
  })

  test("records scheduler failures", async () => {
    const timers = installTimeoutCapture()
    const tmp = await dir()
    const config = baseConfig(tmp, {
      scheduleEnabled: true,
      scheduleAction: "all",
      scheduleMinutes: 5,
    })
    const invalid = { ...config, scheduleAction: "bad" }
    await startScheduler({ config: invalid, keepalive: null })
    await timers[0].callback()
    const records = (await readDailyLogs({ logPath: config.logPath })).records
    expect(records.some((record) => record.event === "schedule" && record.status === "failed")).toBe(true)
  })

  test("keeps the scheduler alive when the page snapshot cannot be written", async () => {
    const timers = installTimeoutCapture()
    const tmp = await dir()
    const snapshotDir = tmp.path("snapshot-dir")
    await mkdir(snapshotDir)
    const config = baseConfig(tmp, {
      scheduleEnabled: true,
      scheduleAction: "keepalive",
      scheduleMinutes: 5,
      snapshotPath: snapshotDir,
    })
    await startScheduler({
      config,
      keepalive: async () => ({ status: "success", summary: "完成", accounts: [] }),
    })
    await timers[0].callback()
    expect((await readDailyLogs({ logPath: config.logPath })).records.some((record) => record.event === "schedule")).toBe(true)
  })

  test("catches up an overdue persisted schedule and records read failures", async () => {
    const timers = installTimeoutCapture()
    const tmp = await dir()
    const config = baseConfig(tmp, {
      scheduleEnabled: true,
      scheduleAction: "keepalive",
      scheduleMinutes: 5,
    })
    const expired = new Date(Date.now() - 60_000).toISOString()
    await writeFile(config.schedulePath, JSON.stringify({
      version: 1,
      signature: scheduleSignature(config),
      lastRunAt: new Date(Date.now() - 360_000).toISOString(),
      nextRunAt: expired,
      lastStatus: "success",
      lastSummary: "old",
    }), "utf8")
    await startScheduler({
      config,
      keepalive: async () => ({ status: "success", summary: "完成", accounts: [] }),
    })
    expect(timers[0].ms).toBe(0)
    await timers[0].callback()
    const state = await readSchedule(config.schedulePath)
    expect(Date.parse(state.nextRunAt)).toBeGreaterThan(Date.now())
    stopScheduler()

    const warnings = []
    const realWarn = console.warn
    console.warn = (line) => warnings.push(String(line))
    try {
      await startScheduler({
        config,
        keepalive: async () => ({ status: "success", summary: "完成", accounts: [] }),
        read: async () => { throw new Error("bad schedule") },
        persist: async () => { throw new Error("disk full") },
      })
    } finally {
      console.warn = realWarn
    }
    expect(warnings.some((line) => line.includes("cannot read the persisted schedule"))).toBe(true)
    expect(warnings.some((line) => line.includes("cannot save the schedule"))).toBe(true)
  })

  test("keeps rescheduling when logging and persistence fail", async () => {
    const timers = installTimeoutCapture()
    const tmp = await dir()
    const config = baseConfig(tmp, {
      scheduleEnabled: true,
      scheduleAction: "keepalive",
      scheduleMinutes: 5,
    })
    const warnings = []
    const realWarn = console.warn
    console.warn = (line) => warnings.push(String(line))
    let writes = 0
    try {
      await startScheduler({
        config,
        keepalive: async () => ({ status: "success", summary: "完成", accounts: [] }),
        log: async ({ entry }) => {
          if (entry.status === "success") throw new Error("log failed")
        },
        persist: async () => {
          writes++
          if (writes > 1) throw new Error("state failed")
        },
        snapshot: async () => {},
      })
      await timers[0].callback()
      expect(timers).toHaveLength(2)

      stopScheduler()
      const failed = { ...config, scheduleAction: "all" }
      await startScheduler({
        config: failed,
        keepalive: null,
        run: async () => { throw new Error("run failed") },
        log: async () => { throw new Error("failure log failed") },
        persist: async () => {},
        snapshot: async () => {},
      })
      await timers[2].callback()
    } finally {
      console.warn = realWarn
    }
    expect(warnings.some((line) => line.includes("cannot write the schedule log"))).toBe(true)
    expect(warnings.some((line) => line.includes("cannot save the completed schedule"))).toBe(true)
    expect(warnings.some((line) => line.includes("cannot write the failure log"))).toBe(true)
  })
})

describe("quota refresher", () => {
  test("runs once at a time on the configured interval and reloads changed options", async () => {
    const intervals = installIntervalCapture()
    const timeouts = installTimeoutCapture()
    const tmp = await dir()
    const config = baseConfig(tmp)
    let runs = 0
    let release
    const refresh = async () => {
      runs++
      await new Promise((resolve) => { release = resolve })
    }
    startQuotaRefresher({ config, refresh })
    expect(timeouts).toHaveLength(1)
    expect(timeouts[0].ms).toBe(30_000)
    expect(timeouts[0].unrefCalled).toBe(true)
    expect(intervals).toHaveLength(1)
    expect(intervals[0].ms).toBe(60_000)
    expect(intervals[0].unrefCalled).toBe(true)

    const first = timeouts[0].callback()
    const overlapping = intervals[0].callback()
    await overlapping
    expect(runs).toBe(1)
    release()
    await first
    const second = intervals[0].callback()
    await new Promise((resolve) => realSetTimeout(resolve, 0))
    expect(runs).toBe(2)
    release()
    await second

    startQuotaRefresher({ config, refresh })
    expect(intervals).toHaveLength(1)
    const changed = { ...config, providerPreferences: { p: { enabled: false } } }
    startQuotaRefresher({ config: changed, refresh })
    expect(intervals).toHaveLength(2)
    expect(intervals[0].cleared).toBe(true)
    expect(timeouts[0].cleared).toBe(true)
    stopQuotaRefresher()
    expect(intervals[1].cleared).toBe(true)
    expect(timeouts[1].cleared).toBe(true)
  })

  test("contains refresher errors instead of leaving rejected timer promises", async () => {
    const intervals = installIntervalCapture()
    const timeouts = installTimeoutCapture()
    const warnings = []
    const realWarn = console.warn
    console.warn = (line) => warnings.push(String(line))
    try {
      const tmp = await dir()
      const config = baseConfig(tmp)
      startQuotaRefresher({
        config,
        refresh: async () => { throw new Error("offline") },
      })
      await timeouts[0].callback()
      await intervals[0].callback()
    } finally {
      console.warn = realWarn
    }
    expect(warnings.filter((line) => line.includes("cannot refresh quotas"))).toHaveLength(2)
  })

  test("records automatic refresh results and skips while another command runs", async () => {
    const tmp = await dir()
    const config = baseConfig(tmp)
    const busy = await refreshQuotasNow({
      config,
      command: async () => ({ status: "running" }),
      run: async () => {
        throw new Error("must not run")
      },
    })
    expect(busy).toMatchObject({
      status: "skipped",
      summary: "额度刷新已跳过：已有任务正在执行。",
    })

    const success = await refreshQuotasNow({
      config,
      command: async () => null,
      run: async () => ({ run: "refresh-ok", status: "success", summary: "完成" }),
    })
    expect(success.status).toBe("success")
    expect((await readDailyLogs({ logPath: config.logPath })).records.some((record) =>
      record.event === "refresh" &&
      record.provider === "quota-keepalive" &&
      record.status === "success"
    )).toBe(true)

    const failed = await refreshQuotasNow({
      config,
      command: async () => null,
      run: async () => { throw new Error("token=secret") },
    })
    expect(failed.status).toBe("failed")
    expect(failed.error).toContain("secret")

    const unreadableCommand = await refreshQuotasNow({
      config,
      command: async () => { throw new Error("queue unreadable") },
      run: async () => ({ run: "refresh-ok-2", status: "success", summary: "完成" }),
    })
    expect(unreadableCommand.status).toBe("success")

    const logFailure = await refreshQuotasNow({
      config,
      command: async () => null,
      run: async () => ({ run: "refresh-ok-3", status: "success", summary: "完成" }),
      log: async () => { throw new Error("disk full") },
    })
    expect(logFailure.status).toBe("success")

    const snapshotDir = tmp.path("snapshot-dir")
    await mkdir(snapshotDir)
    const warnings = []
    const realWarn = console.warn
    console.warn = (line) => warnings.push(String(line))
    try {
      await refreshQuotasNow({
        config: { ...config, snapshotPath: snapshotDir },
        command: async () => null,
        run: async () => ({ run: "refresh-ok-4", status: "success", summary: "完成" }),
      })
    } finally {
      console.warn = realWarn
    }
    expect(warnings.some((line) => line.includes("snapshot"))).toBe(true)
  })
})

describe("live option watcher", () => {
  test("stops old watchers and ignores disabled watching", () => {
    const timers = installIntervalCapture()
    startOptionsWatcher({ input: {}, rawOptions: {}, config: baseConfig({ path: () => "/tmp/x" }, { watchOptions: false }) })
    expect(timers).toHaveLength(0)
    startOptionsWatcher({
      input: {},
      rawOptions: {},
      config: baseConfig({ path: () => "/tmp/x" }, { watchOptions: true }),
    })
    expect(timers).toHaveLength(1)
    startOptionsWatcher({
      input: {},
      rawOptions: {},
      config: baseConfig({ path: () => "/tmp/x" }, { watchOptions: true }),
    })
    expect(timers).toHaveLength(2)
    expect(timers[0].cleared).toBe(true)
    stopOptionsWatcher()
    expect(timers[1].cleared).toBe(true)
  })

  test("reloads page options and consumes a pending command", async () => {
    const timers = installIntervalCapture()
    const tmp = await dir()
    const configHome = tmp.path("config")
    await mkdir(configHome, { recursive: true })
    await writeFile(join(configHome, "plugins.json"), JSON.stringify({
      plugins: [{
        spec: PLUGIN_DIR,
        options: {
          scheduleEnabled: false,
          scheduleAction: "checkin",
          pendingId: "watch-run",
          pendingAction: "detect",
        },
      }],
    }), "utf8")
    const config = baseConfig(tmp, { configHome, watchOptions: true })
    const rawOptions = {
      configHome,
      statePath: config.statePath,
      logPath: config.logPath,
      snapshotPath: config.snapshotPath,
      commandPath: config.commandPath,
    }
    const input = { client: { auth: { set: async () => {} } } }
    let consumed = null
    let refreshed = null
    startOptionsWatcher({
      input,
      rawOptions,
      config,
      consume: async (args) => { consumed = args },
      startRefresh: async (args) => { refreshed = args },
    })
    await timers[0].callback()
    expect(consumed.rawOptions).toMatchObject({ pendingId: "watch-run", pendingAction: "detect" })
    expect(consumed.config.scheduleEnabled).toBe(false)
    expect(refreshed.config.configHome).toBe(configHome)
  })

  test("logs live-option failures without stopping the watcher", async () => {
    const timers = installIntervalCapture()
    const tmp = await dir()
    const warnings = []
    const realWarn = console.warn
    console.warn = (line) => warnings.push(String(line))
    try {
      const config = baseConfig(tmp, {
        commandPath: tmp.path("missing", "command.json"),
        watchOptions: true,
      })
      startOptionsWatcher({
        input: { client: { auth: { set: async () => {} } } },
        rawOptions: {
          commandPath: config.commandPath,
          pendingId: "x",
          pendingAction: "detect",
        },
        config,
      })
      await timers[0].callback()
    } finally {
      console.warn = realWarn
    }
    expect(warnings.some((line) => line.includes("cannot read live options"))).toBe(true)
  })
})

describe("plugin lifecycle", () => {
  test("exposes exactly one host server entry", () => {
    expect(pluginModule).toMatchObject({
      id: "quota-keepalive",
      server: QuotaKeepalivePlugin,
    })
  })

  test("returns the log tool and survives command and snapshot failures", async () => {
    const tmp = await dir()
    const snapshotDir = tmp.path("snapshot-dir")
    await mkdir(snapshotDir)
    const oldWarn = console.warn
    const warnings = []
    console.warn = (line) => warnings.push(String(line))
    try {
      const plugin = await QuotaKeepalivePlugin({
        client: { auth: { set: async () => {} } },
      }, {
        configHome: tmp.path("config"),
        statePath: tmp.path("state.json"),
        logPath: tmp.path("daily.log.jsonl"),
        snapshotPath: snapshotDir,
        commandPath: tmp.path("missing", "command.json"),
        watchOptions: false,
        pendingId: "x",
        pendingAction: "detect",
      })
      expect(plugin.tool.logs).toBeDefined()
    } finally {
      console.warn = oldWarn
    }
    expect(warnings.some((line) => line.includes("page command"))).toBe(true)
    expect(warnings.some((line) => line.includes("snapshot"))).toBe(true)
  })
})
