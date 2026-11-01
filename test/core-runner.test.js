import { afterEach, describe, expect, test } from "bun:test"
import { chmod, mkdir, readFile, unlink, writeFile } from "node:fs/promises"
import { join } from "node:path"
import {
  classifyRefreshResult,
  consumePendingCommand,
  defaultRunMagpieJson,
  enabledProviderIds,
  parseJsonOutput,
  runInBackgroundTask,
  runCommand,
  runRefresh,
} from "../src/runner.js"
import { readDailyLogs } from "../src/log.js"
import { readCommand } from "../src/commands.js"
import { tempDir } from "./support.js"

const cleanups = []
afterEach(async () => {
  while (cleanups.length) {
    const cleanup = cleanups.pop()
    if (cleanup) await cleanup()
  }
})

async function dir(prefix = "quota-runner-") {
  const item = await tempDir(prefix)
  cleanups.push(item.cleanup)
  return item
}

function cfg(tmp, overrides = {}) {
  return {
    configHome: tmp.path("config"),
    logPath: tmp.path("daily.log.jsonl"),
    logMaxBytes: 64 * 1024,
    statePath: tmp.path("state.json"),
    snapshotPath: tmp.path("ui", "data.js"),
    commandPath: tmp.path("command.json"),
    timeoutMs: 1_000,
    ...overrides,
  }
}

function result(stdout = "", { ok = true, stderr = "", error = "", exitCode = 0 } = {}) {
  return { ok, exitCode, stdout, stderr, error }
}

async function waitFor(check, timeoutMs = 2_000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const value = await check()
    if (value) return value
    await new Promise((resolve) => setTimeout(resolve, 20))
  }
  throw new Error("timed out waiting for runner")
}

describe("JSON command output", () => {
  test("parses plain, prefixed, and malformed output", () => {
    expect(parseJsonOutput("")).toBeNull()
    expect(parseJsonOutput("[{\"x\":1}]")).toEqual([{ x: 1 }])
    expect(parseJsonOutput("log line\n[{\"x\":1}]\n")).toEqual([{ x: 1 }])
    expect(parseJsonOutput("log line\n{\"x\":1}\ntrailing")).toEqual({ x: 1 })
    expect(parseJsonOutput("no json")).toBeNull()
    expect(parseJsonOutput("[not-json]")).toBeNull()
    expect(parseJsonOutput("{} {")).toEqual({})
  })

  test("runs a configured magpie command and reports failures", async () => {
    const tmp = await dir()
    const bin = tmp.path("bin")
    await mkdir(bin, { recursive: true })
    const magpie = join(bin, "magpie")
    await writeFile(magpie, "#!/bin/sh\nprintf '<json>'\nprintf '%s' \"$*\" >/dev/null\n", "utf8")
    await chmod(magpie, 0o755)
    const ok = await defaultRunMagpieJson(["accounts", "--json"], {
      timeoutMs: 5_000,
      command: magpie,
    })
    expect(ok).toMatchObject({ ok: true, exitCode: 0, stdout: "<json>" })

    await writeFile(magpie, "#!/bin/sh\necho bad >&2\nexit 7\n", "utf8")
    await chmod(magpie, 0o755)
    const failed = await defaultRunMagpieJson([], { timeoutMs: 1_000, command: magpie })
    expect(failed.ok).toBe(false)
    expect(failed.exitCode).toBe(7)
    expect(failed.stderr).toContain("bad")

    await writeFile(magpie, "#!/bin/sh\nsleep 1\n", "utf8")
    await chmod(magpie, 0o755)
    const timeout = await defaultRunMagpieJson([], { timeoutMs: 20, command: magpie })
    expect(timeout.ok).toBe(false)
    expect(timeout.exitCode).toBe(1)

    const missing = await defaultRunMagpieJson([], {
      command: tmp.path("empty", "magpie"),
    })
    expect(missing.ok).toBe(false)
    expect(missing.exitCode).toBe(1)
  })
})

describe("runCommand", () => {
  test("detects inventory and handles a failed command", async () => {
    const tmp = await dir()
    const config = cfg(tmp)
    await mkdir(config.configHome, { recursive: true })
    await writeFile(join(config.configHome, "plugin-providers.json"), JSON.stringify([{
      id: "workbuddy",
      name: "WorkBuddy",
      checkin: true,
      accounts: [{ key: "k", accountId: "account" }],
      models: [{ id: "m" }],
    }]), "utf8")
    const passedArgs = []
    const ok = await runCommand({
      action: "detect",
      config,
      runMagpieJson: async (args, settings) => {
        passedArgs.push([args, settings])
        return result(JSON.stringify([{ agent: "workbuddy", user: "account" }]))
      },
    })
    expect(ok).toMatchObject({ action: "detect", status: "success" })
    expect(ok.accounts[0]).toMatchObject({ provider: "workbuddy", outcome: "detected" })
    expect(passedArgs[0][0]).toEqual(["accounts", "--json"])
    expect(passedArgs[0][1].timeoutMs).toBe(120_000)
    expect(await readFile(config.snapshotPath, "utf8")).toContain("WorkBuddy")

    const failed = await runCommand({
      action: "detect",
      config,
      runMagpieJson: async () => result("", { ok: false, stderr: "offline" }),
    })
    expect(failed).toMatchObject({ status: "failed" })
    expect(failed.summary).toContain("offline")
    expect(failed.error).toContain("offline")

    const malformed = await runCommand({
      action: "detect",
      config,
      runMagpieJson: async () => result("accounts command returned no JSON"),
    })
    expect(malformed).toMatchObject({ status: "failed" })
    expect(malformed.summary).toContain("no JSON")
  })

  test("maps every check-in provider and status outcome", async () => {
    const tmp = await dir()
    const config = cfg(tmp)
    await mkdir(config.configHome, { recursive: true })
    await writeFile(join(config.configHome, "plugin-providers.json"), JSON.stringify([
      { id: "vendor-id", name: "Vendor Name", accounts: [{ key: "v", accountId: "v" }], models: [] },
    ]), "utf8")
    const rows = [
      { by: "trae", user: "a", outcome: "claimed", credit: 1, streak: 2, msg: "user a" },
      { by: "minimax", user: "b", outcome: "done" },
      { by: "qoder", user: "c", outcome: "inactive", msg: "later" },
      { agent: "zcode", user: "d", outcome: "ineligible" },
      { vendor: "vendor-id", user: "e", outcome: "own-app" },
      { vendor: "vendor name", user: "f", outcome: "captcha" },
      { by: "plugin:custom", user: "g", outcome: "failed" },
      { user: "h", outcome: "other", credit: "bad", streak: 1.2 },
    ]
    const checkin = await runCommand({
      action: "checkin",
      config,
      runMagpieJson: async () => result(JSON.stringify(rows)),
    })
    expect(checkin).toMatchObject({ action: "checkin", status: "failed" })
    expect(checkin.accounts.map((account) => account.provider)).toEqual([
      "trae-cn",
      "minimax-code",
      "qoder-cn",
      "zcode",
      "vendor-id",
      "vendor-id",
      "custom",
      "workbuddy",
    ])
    expect(checkin.accounts.map((account) => account.status)).toEqual([
      "success",
      "success",
      "skipped",
      "skipped",
      "skipped",
      "skipped",
      "failed",
      "failed",
    ])
    expect(checkin.summary).toContain("2 成功")

    const noRows = await runCommand({
      action: "checkin",
      config,
      runMagpieJson: async () => result("", { ok: false, stderr: "no accounts" }),
    })
    expect(noRows.status).toBe("failed")
    expect(noRows.summary).toContain("no accounts")

    const okRows = await runCommand({
      action: "checkin",
      config,
      runMagpieJson: async () => result(JSON.stringify([{ user: "a", outcome: "claimed" }])),
    })
    expect(okRows.status).toBe("success")
    expect((await readDailyLogs({ logPath: config.logPath })).records.some((record) => record.event === "checkin")).toBe(true)
  })

  test("handles missing keepalive adapters and combines all actions", async () => {
    const tmp = await dir()
    const config = cfg(tmp)
    await expect(runCommand({ action: "bad", config })).rejects.toThrow("unknown command action")
    const missing = await runCommand({ action: "keepalive", config, keepalive: null })
    expect(missing).toMatchObject({ status: "failed", error: "provider login adapter unavailable" })

    const keepalive = async () => ({
      status: "success",
      summary: "保活完成：1 个账号已发送真实对话；0 个账号跳过。",
      accounts: [{ provider: "workbuddy", account: "a", action: "keepalive", status: "success", outcome: "sent" }],
    })
    const sent = await runCommand({ action: "keepalive", config, keepalive })
    expect(sent.status).toBe("success")

    const all = await runCommand({
      action: "all",
      config,
      runMagpieJson: async () => result(JSON.stringify([{ user: "a", outcome: "claimed" }])),
      keepalive,
    })
    expect(all).toMatchObject({ action: "all", status: "success" })
    expect(all.accounts).toHaveLength(2)
    expect(all.summary).toContain("签到完成")
    expect(all.summary).toContain("保活完成")
  })

  test("keeps check-in progress when inventory cannot be read and snapshot fails", async () => {
    const tmp = await dir()
    await writeFile(tmp.path("config-as-file"), "x", "utf8")
    const snapshotDir = tmp.path("snapshot-dir")
    await mkdir(snapshotDir)
    const warnings = []
    const oldWarn = console.warn
    console.warn = (line) => warnings.push(String(line))
    try {
      const out = await runCommand({
        action: "checkin",
        config: cfg(tmp, { configHome: tmp.path("config-as-file"), snapshotPath: snapshotDir }),
        runMagpieJson: async () => result(JSON.stringify([{ user: "a", outcome: "claimed" }])),
      })
      expect(out.status).toBe("success")
    } finally {
      console.warn = oldWarn
    }
    expect(warnings.some((line) => line.includes("cannot update"))).toBe(true)
  })
})

describe("quota refresh", () => {
  test("classifies missing cards and Magpie cache hits as skipped", () => {
    expect(classifyRefreshResult(result("", {
      ok: false,
      stderr: "no subscription, plan or key balance of qoder-cn · magpie quota lists them",
    }), null)).toMatchObject({
      status: "skipped",
      outcome: "no-card",
    })
    expect(classifyRefreshResult(result(JSON.stringify([{ provider: "provider" }]), {
      stderr: "read 12s ago: not asked again before 30s have passed, shown as it was read",
    }), [{ provider: "provider" }])).toMatchObject({
      status: "skipped",
      outcome: "cached",
    })
    expect(classifyRefreshResult(result("[]"), [])).toMatchObject({
      status: "skipped",
      outcome: "no-card",
      message: "该供应商当前没有可刷新的额度卡，已跳过。",
    })
    expect(classifyRefreshResult(result(""), null).message).toBe("Magpie 返回空额度数据（退出码 0）")
    expect(classifyRefreshResult(result("", { ok: false, stderr: "network offline" }), null)).toMatchObject({
      status: "failed",
      message: "network offline",
    })
  })

  test("refreshes one provider or all enabled providers and skips disabled ones", async () => {
    const tmp = await dir()
    const config = cfg(tmp, {
      providerPreferences: { disabled: { enabled: false } },
    })
    await mkdir(config.configHome, { recursive: true })
    await writeFile(join(config.configHome, "plugin-providers.json"), JSON.stringify([
      { id: "enabled", name: "Enabled", accounts: [{ key: "a" }] },
      { id: "disabled", name: "Disabled", accounts: [{ key: "b" }] },
    ]), "utf8")
    await writeFile(join(config.configHome, "quotas.json"), JSON.stringify({
      "disabled/user": { quota: { windows: [] } },
    }), "utf8")
    await writeFile(join(config.configHome, "plugin-checkin.json"), JSON.stringify({
      "disabled|key": { day: "2026-10-30" },
    }), "utf8")
    expect(await enabledProviderIds(config)).toEqual(["enabled"])

    const calls = []
    const runMagpieJson = async (args, settings) => {
      calls.push([args, settings])
      return result(JSON.stringify([{ provider: args[2] }]))
    }
    const single = await runRefresh({ config, runMagpieJson, provider: "enabled" })
    expect(single).toMatchObject({ action: "refresh", status: "success" })
    expect(single.accounts[0]).toMatchObject({
      provider: "enabled",
      outcome: "refreshed",
      status: "success",
    })
    expect(calls[0][0]).toEqual(["quota", "refresh", "enabled", "--json"])
    expect(calls[0][1].timeoutMs).toBe(30_000)

    const all = await runRefresh({ config, runMagpieJson })
    expect(all.status).toBe("success")
    expect(all.accounts.map((item) => item.provider)).toEqual(["enabled"])
    expect(calls.map(([args]) => args[2])).toEqual(["enabled", "enabled"])

    const skipped = await runRefresh({ config, runMagpieJson, provider: "disabled" })
    expect(skipped).toMatchObject({
      status: "skipped",
      summary: "额度刷新已跳过：该供应商已停用。",
    })
    expect(calls).toHaveLength(2)
  })

  test("writes refreshed cards to the page snapshot when quotas.json is empty", async () => {
    const tmp = await dir()
    const config = cfg(tmp)
    await mkdir(config.configHome, { recursive: true })
    await writeFile(join(config.configHome, "plugin-providers.json"), JSON.stringify([{
      id: "workbuddy",
      name: "WorkBuddy",
      accounts: [
        { key: "one", accountId: "one@example.com" },
        { key: "two", accountId: "two@example.com" },
      ],
    }]), "utf8")

    const refreshResult = await runRefresh({
      config,
      provider: "workbuddy",
      log: async () => {},
      runMagpieJson: async () => ({
        ok: true,
        exitCode: 0,
        stderr: "",
        error: "",
        stdout: JSON.stringify([
          {
            provider: "workbuddy",
            user: "one@example.com",
            plan: "Free",
            windows: [{ name: "Credits", remaining: 1299.37, limit: 3620 }],
            readAt: "2026-11-01T10:00:00Z",
          },
          {
            provider: "workbuddy",
            user: "two@example.com",
            plan: "Free",
            windows: [{ name: "Credits", remaining: 941.01, limit: 2400 }],
            readAt: "2026-11-01T10:01:00Z",
          },
        ]),
      }),
    })

    expect(refreshResult.status).toBe("success")
    const raw = await readFile(config.snapshotPath, "utf8")
    const snapshot = JSON.parse(raw
      .replace(/^window\.DailyKeepaliveSnapshot = /, "")
      .replace(/;\s*$/, ""))
    expect(snapshot.providers[0].accounts.map((account) => ({
      label: account.label,
      plan: account.quota?.plan,
      remaining: account.quota?.windows[0]?.remaining,
    }))).toEqual([
      { label: "on***@example.com", plan: "Free", remaining: 1299.37 },
      { label: "tw***@example.com", plan: "Free", remaining: 941.01 },
    ])
  })

  test("keeps cards returned by Magpie's refresh cache in the page snapshot", async () => {
    const tmp = await dir()
    const config = cfg(tmp)
    await mkdir(config.configHome, { recursive: true })
    await writeFile(join(config.configHome, "plugin-providers.json"), JSON.stringify([{
      id: "workbuddy",
      name: "WorkBuddy",
      accounts: [{ key: "one", accountId: "one@example.com" }],
    }]), "utf8")

    const cached = await runRefresh({
      config,
      provider: "workbuddy",
      log: async () => {},
      runMagpieJson: async () => result(JSON.stringify([{
        provider: "workbuddy",
        user: "one@example.com",
        plan: "Free",
        windows: [{ name: "Credits", remaining: 42, limit: 100 }],
      }]), {
        stderr: "read 5s ago: not asked again before 30s have passed, shown as it was read",
      }),
    })

    expect(cached).toMatchObject({ status: "skipped", accounts: [{ outcome: "cached" }] })
    const raw = await readFile(config.snapshotPath, "utf8")
    expect(raw).toContain('"remaining": 42')
    expect(raw).toContain('"limit": 100')
  })

  test("skips empty payloads, reports card errors, and handles no enabled providers", async () => {
    const tmp = await dir()
    const config = cfg(tmp)
    const empty = await runRefresh({
      config,
      provider: "provider",
      runMagpieJson: async () => result("[]"),
    })
    expect(empty.status).toBe("skipped")
    expect(empty.summary).toBe("额度刷新完成：1 个跳过。")

    const cardError = await runRefresh({
      config,
      provider: "provider",
      runMagpieJson: async () => result(JSON.stringify([{ error: "token=secret" }])),
    })
    expect(cardError.status).toBe("failed")
    expect(cardError.error).not.toContain("secret")

    const noCard = await runRefresh({
      config,
      provider: "qoder-cn",
      runMagpieJson: async () => result("", {
        ok: false,
        stderr: "no subscription, plan or key balance of qoder-cn · magpie quota lists them",
      }),
    })
    expect(noCard).toMatchObject({
      status: "skipped",
      summary: "额度刷新完成：1 个跳过。",
      error: "",
    })
    expect(noCard.accounts[0]).toMatchObject({
      provider: "qoder-cn",
      status: "skipped",
      outcome: "no-card",
    })

    const cached = await runRefresh({
      config,
      provider: "codex",
      runMagpieJson: async () => result(JSON.stringify([{ provider: "codex" }]), {
        stderr: "read 12s ago: not asked again before 30s have passed, shown as it was read",
      }),
    })
    expect(cached).toMatchObject({
      status: "skipped",
      summary: "额度刷新完成：1 个跳过。",
    })
    expect(cached.accounts[0]).toMatchObject({
      provider: "codex",
      status: "skipped",
      outcome: "cached",
    })

    const warnings = []
    const realWarn = console.warn
    console.warn = (line) => warnings.push(String(line))
    try {
      await runRefresh({
        config,
        provider: "provider",
        runMagpieJson: async () => result(JSON.stringify([{ provider: "provider" }])),
        log: async () => { throw new Error("disk full") },
      })
    } finally {
      console.warn = realWarn
    }
    expect(warnings.some((line) => line.includes("cannot write quota refresh log"))).toBe(true)

    await mkdir(config.configHome, { recursive: true })
    await writeFile(join(config.configHome, "plugin-providers.json"), "[]", "utf8")
    const disabledPreferences = Object.fromEntries([
      "codex", "claude", "copilot", "gemini", "antigravity", "zed", "factory", "mimo",
    ].map((id) => [id, { enabled: false }]))
    const none = await runRefresh({
      config: { ...config, providerPreferences: disabledPreferences },
      runMagpieJson: async () => result("[]"),
    })
    expect(none).toMatchObject({
      status: "skipped",
      summary: "额度刷新已跳过：没有启用中的供应商。",
    })
  })

  test("keeps a mixed successful and skipped refresh successful", async () => {
    const tmp = await dir()
    const config = cfg(tmp)
    await mkdir(config.configHome, { recursive: true })
    await writeFile(join(config.configHome, "plugin-providers.json"), JSON.stringify([
      { id: "good", name: "Good", accounts: [{ key: "a" }] },
      { id: "qoder-cn", name: "Qoder", accounts: [{ key: "b" }] },
      { id: "codex", name: "Codex", accounts: [{ key: "c" }] },
    ]), "utf8")
    const refreshed = await runRefresh({
      config,
      runMagpieJson: async (args) => {
        if (args[2] === "qoder-cn") {
          return result("", {
            ok: false,
            stderr: "no subscription, plan or key balance of qoder-cn · magpie quota lists them",
          })
        }
        if (args[2] === "codex") return result("[]")
        return result(JSON.stringify([{ provider: args[2] }]))
      },
    })
    expect(refreshed).toMatchObject({
      status: "success",
      summary: "额度刷新完成：1 个供应商成功，2 个跳过。",
      error: "",
    })
    expect(refreshed.accounts.map((item) => item.status)).toEqual(["success", "skipped", "skipped"])
  })

  test("skips a second refresh while the first one is still running", async () => {
    const tmp = await dir()
    const config = cfg(tmp)
    let release
    let resolveStarted
    const started = new Promise((resolve) => { resolveStarted = resolve })
    const first = runCommand({
      action: "refresh",
      config,
      provider: "provider",
      runMagpieJson: async () => {
        resolveStarted()
        await new Promise((resolve) => { release = resolve })
        return result(JSON.stringify([{ provider: "provider" }]))
      },
    })
    await started
    const second = await runCommand({
      action: "refresh",
      config,
      provider: "provider",
      runMagpieJson: async () => {
        throw new Error("must not run")
      },
    })
    expect(second).toMatchObject({
      status: "skipped",
      summary: "额度刷新已跳过：上一轮刷新仍在执行。",
    })
    release()
    await first
  })
})

describe("page commands", () => {
  test("contains background task values, rejections, and synchronous throws", async () => {
    expect(await runInBackgroundTask(undefined)).toBeUndefined()
    expect(await runInBackgroundTask(null)).toBeNull()
    expect(await runInBackgroundTask(42)).toBe(42)
    expect(await runInBackgroundTask(() => 7)).toBe(7)
    expect(await runInBackgroundTask(Promise.resolve("ok"))).toBe("ok")
    expect(await runInBackgroundTask(Promise.reject(new Error("async boom")))).toBeUndefined()
    expect(await runInBackgroundTask(() => {
      throw new Error("sync boom")
    })).toBeUndefined()
  })

  test("returns before a pending command's background task finishes", async () => {
    const tmp = await dir()
    const config = cfg(tmp)
    const backgroundTasks = []
    const queued = await consumePendingCommand({
      config,
      rawOptions: { pendingId: "deferred-run", pendingAction: "detect" },
      runMagpieJson: async () => result(JSON.stringify([])),
      runInBackground: (task) => {
        backgroundTasks.push(task)
        return Promise.resolve()
      },
    })

    expect(queued).toMatchObject({ id: "deferred-run", status: "running" })
    expect(backgroundTasks).toHaveLength(1)
    expect((await readCommand(config.commandPath))?.status).toBe("running")

    await backgroundTasks[0]
    expect((await readCommand(config.commandPath))?.status).toBe("success")
  })

  test("ignores invalid pending options and reports a busy queue", async () => {
    const tmp = await dir()
    const config = cfg(tmp)
    expect(await consumePendingCommand({ config, rawOptions: {} })).toBeNull()
    expect(await consumePendingCommand({ config, rawOptions: { pendingId: "x", pendingAction: "bad" } })).toBeNull()
    await writeFile(config.commandPath, JSON.stringify({
      version: 1,
      current: { id: "other", action: "checkin", status: "queued" },
      consumed: [],
      keepaliveClaims: [],
    }), "utf8")
    const busy = await consumePendingCommand({
      config,
      rawOptions: { pendingId: "mine", pendingAction: "checkin" },
    })
    expect(busy).toMatchObject({ busy: true, status: "queued" })
  })

  test("runs a pending command asynchronously and records completion", async () => {
    const tmp = await dir()
    const config = cfg(tmp)
    const queued = await consumePendingCommand({
      config,
      rawOptions: { pendingId: "detect-run", pendingAction: "detect" },
      runMagpieJson: async () => result(JSON.stringify([])),
    })
    expect(queued).toMatchObject({ id: "detect-run", action: "detect", status: "running" })
    const command = await waitFor(async () => {
      const current = await readCommand(config.commandPath)
      return current?.status === "success" ? current : null
    })
    expect(command).toMatchObject({ id: "detect-run", status: "success" })
    expect(command.summary).toContain("检测完成")
    const logs = await waitFor(async () => {
      const records = (await readDailyLogs({ logPath: config.logPath })).records
      return records.some((record) => record.event === "detect") ? records : null
    })
    expect(logs.some((record) => record.event === "detect" && record.status === "success")).toBe(true)
  })

  test("passes a provider-scoped refresh through the page command queue", async () => {
    const tmp = await dir()
    const config = cfg(tmp)
    await mkdir(config.configHome, { recursive: true })
    await writeFile(join(config.configHome, "plugin-providers.json"), JSON.stringify([
      { id: "provider", name: "Provider", accounts: [{ key: "a" }] },
    ]), "utf8")
    const queued = await consumePendingCommand({
      config,
      rawOptions: {
        pendingId: "refresh-run",
        pendingAction: "refresh",
        pendingProvider: "provider",
      },
      runMagpieJson: async () => result(JSON.stringify([{ provider: "provider" }])),
    })
    expect(queued).toMatchObject({
      id: "refresh-run",
      action: "refresh",
      status: "running",
    })
    const command = await waitFor(async () => {
      const current = await readCommand(config.commandPath)
      return current?.status === "success" ? current : null
    })
    expect(command.provider).toBe("provider")
    expect(command.accounts[0].provider).toBe("provider")
    const records = await waitFor(async () => {
      const recent = (await readDailyLogs({ logPath: config.logPath })).records
      return recent.some((record) =>
        record.event === "refresh" &&
        record.provider === "daily-keepalive"
      ) ? recent : null
    })
    expect(records.some((record) =>
      record.event === "refresh" &&
      record.provider === "provider" &&
      record.status === "success"
    )).toBe(true)
  })

  test("passes a single-account manual task through the page command queue", async () => {
    const tmp = await dir()
    const config = cfg(tmp)
    const calls = []
    const queued = await consumePendingCommand({
      config,
      rawOptions: {
        pendingId: "account-run",
        pendingAction: "checkin",
        pendingProvider: "workbuddy",
        pendingAccount: "acct-123456789012",
      },
      checkin: async (args) => {
        calls.push(args)
        return {
          status: "success",
          summary: "签到完成：1 成功。",
          accounts: [{
            provider: "workbuddy",
            account: "account",
            action: "checkin",
            status: "success",
            outcome: "claimed",
          }],
        }
      },
    })
    expect(queued).toMatchObject({ id: "account-run", action: "checkin", status: "running" })
    const command = await waitFor(async () => {
      const current = await readCommand(config.commandPath)
      return current?.status === "success" ? current : null
    })
    expect(command.accounts[0]).toMatchObject({ provider: "workbuddy", outcome: "claimed" })
    expect(calls).toEqual([{
      force: true,
      providers: ["workbuddy"],
      accounts: ["acct-123456789012"],
    }])
  })

  test("records an unexpected command failure", async () => {
    const tmp = await dir()
    const config = cfg(tmp)
    await consumePendingCommand({
      config,
      rawOptions: { pendingId: "fail-run", pendingAction: "detect" },
      runMagpieJson: async () => { throw new Error("token=secret") },
    })
    const command = await waitFor(async () => {
      const current = await readCommand(config.commandPath)
      return current?.status === "failed" ? current : null
    })
    expect(command.error).toBe("token=[redacted]")
    const logs = await waitFor(async () => {
      const records = (await readDailyLogs({ logPath: config.logPath })).records
      return records.some((record) => record.event === "detect") ? records : null
    })
    expect(logs.some((record) => record.status === "failed")).toBe(true)
  })

  test("survives a failed command record while preserving the failure log", async () => {
    const tmp = await dir()
    await writeFile(tmp.path("log-parent"), "x", "utf8")
    const config = cfg(tmp, {
      logPath: tmp.path("log-parent", "daily.log.jsonl"),
    })
    await consumePendingCommand({
      config,
      rawOptions: { pendingId: "record-fail", pendingAction: "detect" },
      runMagpieJson: async () => {
        await unlink(config.commandPath)
        await mkdir(config.commandPath)
        throw new Error("provider offline")
      },
    })
    await new Promise((resolve) => setTimeout(resolve, 100))
    expect((await readFile(tmp.path("log-parent"), "utf8"))).toBe("x")
  })
})
