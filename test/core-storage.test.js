import { afterEach, describe, expect, test } from "bun:test"
import { chmod, mkdir, readFile, stat, unlink, writeFile } from "node:fs/promises"
import { join } from "node:path"
import {
  claimCommand,
  claimForcedKeepalive,
  commandActionSpends,
  enqueueCommand,
  finishCommand,
  newCommandId,
  readCommand,
  readState,
  recordCommandResult,
  withLock,
  writeState,
} from "../src/commands.js"
import {
  PLUGIN_DIR,
  PLUGIN_NAME,
  pluginSpecMatches,
  readLivePluginOptions,
} from "../src/live-options.js"
import {
  accountTag,
  appendDailyLog,
  appendUnlocked,
  formatLogRecord,
  formatLogRecords,
  newRunId,
  readDailyLogs,
  rotateIfNeeded,
  safeLogText,
} from "../src/log.js"
import { StateBusyError, StateFileError, runDailyOnce } from "../src/state.js"
import { tempDir } from "./support.js"

const cleanups = []
afterEach(async () => {
  while (cleanups.length) await cleanups.pop()()
})

async function dir(prefix) {
  const item = await tempDir(prefix)
  cleanups.push(item.cleanup)
  return item
}

function clock(at) {
  return new Date(at)
}

describe("command queue", () => {
  test("validates state, queues once, claims, records, and finishes", async () => {
    const tmp = await dir("quota-commands-")
    const path = tmp.path("command.json")
    await expect(enqueueCommand({ commandPath: path, action: "bad" })).rejects.toThrow("action must be")
    await expect(readState(path)).resolves.toMatchObject({ version: 1, current: null, consumed: [], keepaliveClaims: [] })

    const first = await enqueueCommand({ commandPath: path, action: "checkin", source: "x".repeat(100), id: "id-1" })
    expect(first.queued).toBe(true)
    expect(first.command).toMatchObject({ id: "id-1", action: "checkin", status: "queued" })
    expect(first.command.source.length).toBe(40)
    const duplicate = await enqueueCommand({ commandPath: path, action: "checkin", id: "id-1" })
    expect(duplicate).toMatchObject({ queued: false, command: { id: "id-1" } })
    const busy = await enqueueCommand({ commandPath: path, action: "detect" })
    expect(busy.busy).toBe(true)

    const claim = await claimCommand({ commandPath: path, id: "id-1" })
    expect(claim).toMatchObject({ id: "id-1", status: "running" })
    expect(await claimCommand({ commandPath: path, id: "id-1" })).toBeNull()
    expect(await claimCommand({ commandPath: path, id: "wrong" })).toBeNull()
    expect(await claimForcedKeepalive({ commandPath: path, account: "a" })).toBe(false)

    await expect(finishCommand({ commandPath: path, id: "id-1", status: "unknown" })).rejects.toThrow("status must be")
    expect(await finishCommand({ commandPath: path, id: "wrong", status: "success" })).toBeNull()
    const finished = await finishCommand({
      commandPath: path,
      id: "id-1",
      status: "success",
      error: "token=secret",
    })
    expect(finished).toMatchObject({ status: "success", error: "token=[redacted]" })
    expect((await readCommand(path)).status).toBe("success")
    expect(await claimCommand({ commandPath: path })).toBeNull()
  })

  test("claims forced keepalive once per running account", async () => {
    const tmp = await dir("quota-claims-")
    const path = tmp.path("command.json")
    await enqueueCommand({ commandPath: path, action: "keepalive", id: "k" })
    expect(await claimForcedKeepalive({ commandPath: path, account: "a" })).toBe(false)
    await claimCommand({ commandPath: path, id: "k" })
    expect(await claimForcedKeepalive({ commandPath: path, account: "" })).toBe(false)
    expect(await claimForcedKeepalive({ commandPath: path, account: "a" })).toBe(true)
    expect(await claimForcedKeepalive({ commandPath: path, account: "a" })).toBe(false)
    const state = await readState(path)
    expect(state.keepaliveClaims).toEqual(["a"])
  })

  test("records a result with bounded sanitized account rows and clears claims", async () => {
    const tmp = await dir("quota-record-")
    const path = tmp.path("command.json")
    await enqueueCommand({ commandPath: path, action: "all", id: "run" })
    await claimCommand({ commandPath: path, id: "run" })
    await claimForcedKeepalive({ commandPath: path, account: "a" })
    const result = await recordCommandResult({
      commandPath: path,
      id: "run",
      status: "success",
      summary: "token=secret",
      error: "Bearer token",
      accounts: Array.from({ length: 82 }, (_, index) => ({
        provider: "p",
        account: `a-${index}`,
        action: "checkin",
        status: "success",
        outcome: "claimed",
        message: "ok",
        ignored: "drop",
      })),
    })
    expect(result.status).toBe("success")
    expect(result.summary).toBe("token=[redacted]")
    expect(result.accounts).toHaveLength(80)
    expect(result.accounts[0]).not.toHaveProperty("ignored")
    expect((await readState(path)).keepaliveClaims).toEqual([])
    expect(await recordCommandResult({ commandPath: path, id: "wrong", status: "failed" })).toBeNull()
  })

  test("handles malformed state and stale or running claims", async () => {
    const tmp = await dir("quota-state-")
    const path = tmp.path("command.json")
    await writeFile(path, "not-json", "utf8")
    await expect(readState(path)).rejects.toThrow("not valid JSON")
    expect(await readCommand(path)).toBeNull()
    await writeFile(path, JSON.stringify({ version: 99 }), "utf8")
    await expect(readState(path)).rejects.toThrow("unsupported command shape")

    const state = {
      version: 1,
      current: {
        id: "old",
        action: "checkin",
        status: "running",
        createdAt: clock("2026-10-30T00:00:00.000Z").toISOString(),
        startedAt: clock("2026-10-30T00:00:00.000Z").toISOString(),
      },
      consumed: [],
      keepaliveClaims: [],
    }
    await writeFile(path, JSON.stringify(state), "utf8")
    const originalNow = Date.now
    Date.now = () => Date.parse("2026-10-30T00:10:01.000Z")
    try {
      expect(await claimCommand({ commandPath: path, id: "old" })).toMatchObject({ status: "running" })
    } finally {
      Date.now = originalNow
    }
    state.current.status = "finished"
    await writeFile(path, JSON.stringify(state), "utf8")
    expect(await claimCommand({ commandPath: path, id: "old" })).toBeNull()
    state.current.status = "queued"
    state.consumed = ["old"]
    await writeFile(path, JSON.stringify(state), "utf8")
    expect(await claimCommand({ commandPath: path, id: "old" })).toBeNull()
  })

  test("exposes deterministic ids and spending actions", () => {
    expect(newCommandId(clock("2026-10-30T01:02:03.456Z"))).toMatch(/^20261030T010203Z-[a-f0-9]{8}$/)
    expect(commandActionSpends("keepalive")).toBe(true)
    expect(commandActionSpends("all")).toBe(true)
    expect(commandActionSpends("checkin")).toBe(false)
  })

  test("reports write failures and lock contention", async () => {
    const tmp = await dir("quota-lock-")
    const missing = tmp.path("missing", "command.json")
    await expect(withLock(missing, async () => 1, 0)).rejects.toMatchObject({ code: "ENOENT" })
    await writeFile(tmp.path("parent-file"), "x", "utf8")
    await expect(writeState(tmp.path("parent-file", "state.json"), { version: 1 })).rejects.toBeTruthy()

    const path = tmp.path("busy.json")
    await writeFile(`${path}.lock`, "{}", "utf8")
    await expect(withLock(path, async () => 1, 0)).rejects.toThrow("busy")
    const old = new Date(Date.now() - 31_000)
    await import("node:fs/promises").then(({ utimes }) => utimes(`${path}.lock`, old, old))
    expect(await withLock(path, async () => 2, 100)).toBe(2)
  })
})

describe("live plugin options", () => {
  test("matches local paths and exact npm specs but rejects lookalikes and ranges", () => {
    for (const spec of [
      PLUGIN_DIR,
      PLUGIN_NAME,
      `${PLUGIN_NAME}@latest`,
      `${PLUGIN_NAME}@0.1.8`,
      `npm:${PLUGIN_NAME}`,
      `npm:${PLUGIN_NAME}@latest`,
      `npm:${PLUGIN_NAME}@0.1.8`,
      `npm:${PLUGIN_NAME}@0.1.8-beta.1`,
      `npm:${PLUGIN_NAME}@0.1.8+build.1`,
    ]) {
      expect(pluginSpecMatches(spec)).toBe(true)
    }

    for (const spec of [
      "",
      null,
      "relative",
      "./relative",
      `${PLUGIN_NAME}-other`,
      `${PLUGIN_NAME}/subpath`,
      `${PLUGIN_NAME}@^0.1.8`,
      `${PLUGIN_NAME}@~0.1.8`,
      `${PLUGIN_NAME}@0.1`,
      `${PLUGIN_NAME}@*`,
      `${PLUGIN_NAME}@v0.1.8`,
      `npm:${PLUGIN_NAME}@next`,
      `npm:${PLUGIN_NAME}@^0.1.8`,
      `npm:${PLUGIN_NAME}-other`,
      "github:sljdxde/quota-keepalive",
      "https://example.com/quota-keepalive.tgz",
    ]) {
      expect(pluginSpecMatches(spec)).toBe(false)
    }
  })

  test("reads only the matching plugin entry with a supported options shape", async () => {
    const tmp = await dir("quota-live-")
    expect(await readLivePluginOptions(tmp.path("missing"))).toEqual({})
    await writeFile(tmp.path("plugins.json"), "{bad", "utf8")
    expect(await readLivePluginOptions(tmp.dir)).toEqual({})
    await writeFile(tmp.path("plugins.json"), JSON.stringify({ plugins: {} }), "utf8")
    expect(await readLivePluginOptions(tmp.dir)).toEqual({})
    await writeFile(tmp.path("plugins.json"), JSON.stringify({
      plugins: [
        { spec: "relative", options: { nope: true } },
        { spec: `${PLUGIN_NAME}-other`, options: { nope: true } },
        { spec: `npm:${PLUGIN_NAME}@latest`, options: { yes: true } },
        { spec: PLUGIN_DIR, options: { duplicate: true } },
      ],
    }), "utf8")
    expect(await readLivePluginOptions(tmp.dir)).toEqual({ yes: true })

    for (const options of [null, "invalid", [], 1]) {
      await writeFile(tmp.path("plugins.json"), JSON.stringify({
        plugins: [{ spec: PLUGIN_DIR, options }],
      }), "utf8")
      expect(await readLivePluginOptions(tmp.dir)).toEqual({})
    }
  })
})

describe("structured logs", () => {
  test("sanitizes account text and ids", () => {
    expect(accountTag("")).toBe("")
    expect(accountTag("account")).toMatch(/^acct-[a-f0-9]{12}$/)
    expect(safeLogText("account=secret control\u0000 text", "secret", 80)).toBe("account=[redacted account] control text")
    expect(safeLogText("alpha beta", "alpha|beta", 80)).toBe("[redacted account] [redacted account]")
    expect(safeLogText({ value: 1 }, "", 80)).toBe("[object Object]")
    expect(safeLogText("x", "", 0)).toBe("x")
  })

  test("writes, rotates, reads, and formats logs", async () => {
    const tmp = await dir("quota-log-")
    const path = tmp.path("daily.jsonl")
    expect(newRunId(clock("2026-10-30T01:02:03.456Z"))).toMatch(/^20261030T010203Z-[a-f0-9]{8}$/)
    expect(await appendDailyLog({ logPath: "relative", entry: {} })).toBe(false)
    expect(await appendDailyLog({ logPath: path, entry: [] })).toBe(false)
    const circular = {}
    circular.self = circular
    expect(await appendDailyLog({ logPath: path, entry: circular })).toBe(false)
    expect(await appendDailyLog({ logPath: path, logMaxBytes: 0, entry: {
      ts: "2026-10-30T01:00:00.000Z",
      day: "2026-10-30",
      provider: "workbuddy",
      event: "checkin",
      status: "success",
      outcome: "claimed",
      account: accountTag("x"),
      credit: 1,
      streak: 2,
      message: "token=secret",
      error: "Bearer abc",
    } })).toBe(true)
    await appendDailyLog({ logPath: path, entry: {
      ts: "2026-10-30T02:00:00.000Z",
      day: "2026-10-31",
      provider: "x",
      event: "keepalive",
      status: "failed",
      inputTokens: 1,
      durationMs: 2,
      retry: true,
      persisted: false,
      keepalive: true,
      run: "run",
      model: "m",
      attempts: 1,
    } })
    await appendDailyLog({ logPath: path, entry: {
      ts: "bad-date",
      event: "checkin",
      status: "success",
    } })
    await writeFile(path, `${await readFile(path, "utf8")}\nnot-json\n{}\n`, "utf8")

    const read = await readDailyLogs({ logPath: path, day: "2026-10-30", status: "success", limit: 1 })
    expect(read).toMatchObject({ malformed: 2 })
    expect(read.records).toHaveLength(1)
    expect(formatLogRecord(read.records[0])).toContain("provider=workbuddy")
    expect(formatLogRecord({
      ts: "bad-date",
      event: "checkin",
      status: "success",
      run: "",
      message: "x",
    })).toContain("bad-date")
    expect(formatLogRecords(null)).toBe("")
    expect((await readDailyLogs({ logPath: path, includePrevious: false, limit: 10_000 })).records.length).toBeGreaterThan(0)
    await expect(readDailyLogs({ logPath: "relative" })).rejects.toThrow("absolute")
    await expect(readDailyLogs({ logPath: tmp.dir })).rejects.toThrow("cannot read")

    const rotated = tmp.path("rotate.jsonl")
    await writeFile(rotated, "x".repeat(100), "utf8")
    await rotateIfNeeded(rotated, 95, 10)
    expect((await stat(`${rotated}.1`)).isFile()).toBe(true)
    expect((await stat(`${rotated}.1`)).mode & 0o777).toBe(0o600)
  })

  test("covers filesystem failure handling without touching real logs", async () => {
    const refused = Object.assign(new Error("refused"), { code: "EACCES" })
    await expect(rotateIfNeeded("/x", 1, 1, {
      stat: async () => { throw refused },
    })).rejects.toThrow("refused")
    await rotateIfNeeded("/x", 1, 1, {
      stat: async () => ({ size: 0 }),
    })
    let renames = 0
    await rotateIfNeeded("/x", 1, 2, {
      stat: async () => ({ size: 10 }),
      rename: async () => {
        renames++
        if (renames === 1) throw Object.assign(new Error("exists"), { code: "EEXIST" })
      },
      unlink: async () => {},
      chmod: async () => { throw refused },
    })
    await expect(rotateIfNeeded("/x", 1, 2, {
      stat: async () => ({ size: 10 }),
      rename: async () => { throw Object.assign(new Error("gone"), { code: "ENOENT" }) },
    })).resolves.toBeUndefined()
    await expect(rotateIfNeeded("/x", 1, 2, {
      stat: async () => ({ size: 10 }),
      rename: async () => { throw Object.assign(new Error("bad"), { code: "EIO" }) },
    })).rejects.toThrow("bad")
    await expect(rotateIfNeeded("/x", 1, 2, {
      stat: async () => ({ size: 10 }),
      rename: async () => { throw Object.assign(new Error("exists"), { code: "EEXIST" }) },
      unlink: async () => { throw refused },
    })).rejects.toThrow("refused")

    const closeError = Object.assign(new Error("close"), { code: "EIO" })
    const ops = {
      mkdir: async () => {},
      stat: async () => { throw Object.assign(new Error("missing"), { code: "ENOENT" }) },
      open: async () => ({
        chmod: async () => {},
        writeFile: async () => {},
        close: async () => { throw closeError },
      }),
    }
    await appendUnlocked("/x", 1, "line", ops)
  })
})

describe("keepalive state", () => {
  test("rejects malformed state and tracks success without duplicate sends", async () => {
    const tmp = await dir("quota-run-state-")
    const path = tmp.path("state.json")
    await writeFile(path, "not-json", "utf8")
    const invalid = await runDailyOnce({
      statePath: path,
      account: "bad",
      day: "2026-10-30",
      maxAttempts: 3,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => "never",
    })
    expect(invalid.kind).toBe("unavailable")

    await writeFile(path, JSON.stringify({ version: 1, accounts: { x: { day: "2026-10-30", status: "bad", attempts: 1 } } }), "utf8")
    expect((await runDailyOnce({
      statePath: path,
      account: "x",
      day: "2026-10-30",
      maxAttempts: 3,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => "never",
    })).error).toContain("invalid entry")

    await writeFile(path, JSON.stringify({ version: 2, accounts: {} }), "utf8")
    expect((await runDailyOnce({
      statePath: path,
      account: "x",
      day: "2026-10-30",
      maxAttempts: 3,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => "never",
    })).error).toContain("unsupported shape")

    await writeFile(path, JSON.stringify({ version: 1, accounts: {} }), "utf8")
    const sent = await runDailyOnce({
      statePath: path,
      account: "good",
      day: "2026-10-30",
      maxAttempts: 3,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => "reply",
    })
    expect(sent).toMatchObject({ kind: "sent", ok: true, attempts: 1, persisted: true, result: "reply" })
    const skipped = await runDailyOnce({
      statePath: path,
      account: "good",
      day: "2026-10-30",
      maxAttempts: 3,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => "must not run",
    })
    expect(skipped).toMatchObject({ kind: "skipped", reason: "done-today", attempts: 1 })
    expect((await runDailyOnce({
      statePath: path,
      account: "good",
      day: "2026-10-30",
      maxAttempts: 3,
      lockWaitMs: 100,
      timeoutMs: 100,
      force: true,
      task: async () => "forced",
    })).kind).toBe("skipped")
    const raw = JSON.parse(await readFile(path, "utf8"))
    expect(raw.accounts.good).toMatchObject({ status: "ok", attempts: 1 })
  })

  test("stops automatic retries after a persisted quota failure", async () => {
    const tmp = await dir("quota-terminal-")
    const path = tmp.path("state.json")
    await writeFile(path, JSON.stringify({
      version: 1,
      accounts: {
        a: {
          day: "2026-10-30",
          status: "failed",
          attempts: 1,
          error: "HTTP 429: Credits exhausted; purchase an add-on pack",
        },
        b: {
          day: "2026-10-30",
          status: "failed",
          attempts: 1,
          error: "HTTP 402: Payment required",
        },
      },
    }), "utf8")
    expect(await runDailyOnce({
      statePath: path,
      account: "a",
      day: "2026-10-30",
      maxAttempts: 3,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => "must not run",
    })).toMatchObject({
      kind: "skipped",
      reason: "quota-exhausted",
      attempts: 1,
    })
    expect((await runDailyOnce({
      statePath: path,
      account: "a",
      day: "2026-10-30",
      maxAttempts: 3,
      lockWaitMs: 100,
      timeoutMs: 100,
      force: true,
      task: async () => "manual retry",
    })).kind).toBe("sent")
    expect(await runDailyOnce({
      statePath: path,
      account: "b",
      day: "2026-10-30",
      maxAttempts: 3,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => "must not run",
    })).toMatchObject({
      kind: "skipped",
      reason: "quota-exhausted",
    })
  })

  test("persists failures, exhausts retries, prunes old days, and reports locks", async () => {
    const tmp = await dir("quota-run-fail-")
    const path = tmp.path("state.json")
    for (let index = 1; index <= 2; index++) {
      const result = await runDailyOnce({
        statePath: path,
        account: "a",
        day: "2026-10-30",
        maxAttempts: 2,
        lockWaitMs: 100,
        timeoutMs: 100,
        task: async () => { throw new Error("token=secret") },
      })
      expect(result).toMatchObject({ kind: "sent", ok: false, attempts: index })
    }
    const exhausted = await runDailyOnce({
      statePath: path,
      account: "a",
      day: "2026-10-30",
      maxAttempts: 2,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => "never",
    })
    expect(exhausted).toMatchObject({ kind: "skipped", reason: "exhausted", attempts: 2 })

    const interruptedPath = tmp.path("interrupted.json")
    await writeFile(interruptedPath, JSON.stringify({
      version: 1,
      accounts: {
        interrupted: {
          day: "2026-10-30",
          status: "running",
          attempts: 2,
          at: "2026-10-30T00:00:00.000Z",
        },
      },
    }), "utf8")
    expect(await runDailyOnce({
      statePath: interruptedPath,
      account: "interrupted",
      day: "2026-10-30",
      now: new Date("2026-10-30T01:00:00.000Z"),
      maxAttempts: 2,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => "recovered",
    })).toMatchObject({
      kind: "sent",
      ok: true,
      attempts: 3,
    })

    const oldPath = tmp.path("old.json")
    await writeFile(oldPath, JSON.stringify({
      version: 1,
      accounts: {
        old: { day: "2026-01-01", status: "ok", attempts: 1 },
        keep: { day: "2026-10-30", status: "ok", attempts: 1 },
      },
    }), "utf8")
    expect((await runDailyOnce({
      statePath: oldPath,
      account: "new",
      day: "2026-10-30",
      maxAttempts: 2,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => "x",
    })).kind).toBe("sent")
    const pruned = JSON.parse(await readFile(oldPath, "utf8"))
    expect(pruned.accounts.old).toBeUndefined()
    expect(pruned.accounts.keep).toBeDefined()

    await writeFile(`${path}.lock`, "{}", "utf8")
    const busy = await runDailyOnce({
      statePath: path,
      account: "busy",
      day: "2026-10-30",
      maxAttempts: 2,
      lockWaitMs: 0,
      timeoutMs: 100,
      task: async () => "x",
    })
    expect(busy.kind).toBe("busy")
    expect(new StateBusyError("busy")).toMatchObject({ name: "StateBusyError" })
    expect(new StateFileError("bad")).toMatchObject({ name: "StateFileError" })
  })

  test("reports start and result persistence failures", async () => {
    const tmp = await dir("quota-run-disk-")
    const lockedDir = tmp.path("locked")
    await mkdir(lockedDir)
    const path = join(lockedDir, "state.json")
    await writeFile(path, JSON.stringify({ version: 1, accounts: {} }), "utf8")
    const startFailure = await runDailyOnce({
      statePath: path,
      account: "a",
      day: "2026-10-30",
      maxAttempts: 2,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => "x",
      writeState: async () => { throw new Error("disk full") },
    })
    expect(startFailure.kind).toBe("unavailable")
    expect(startFailure.error).toContain("cannot save keepalive state")

    const resultPath = tmp.path("result.json")
    const sent = await runDailyOnce({
      statePath: resultPath,
      account: "a",
      day: "2026-10-30",
      maxAttempts: 2,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => {
        await unlink(resultPath)
        await mkdir(resultPath)
        return "done"
      },
    })
    expect(sent).toMatchObject({ kind: "sent", ok: true, persisted: false })
  })

  test("prunes the in-memory cache after many accounts", async () => {
    const tmp = await dir("quota-memory-")
    const path = tmp.path("state.json")
    for (let index = 0; index < 258; index++) {
      await runDailyOnce({
        statePath: path,
        account: `account-${index}`,
        day: "2026-10-30",
        maxAttempts: 1,
        lockWaitMs: 100,
        timeoutMs: 100,
        task: async () => "ok",
      })
    }
    expect((await readFile(path, "utf8")).length).toBeGreaterThan(1_000)
  })
})
