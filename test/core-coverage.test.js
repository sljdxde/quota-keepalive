import { afterEach, describe, expect, test } from "bun:test"
import { mkdir, stat, writeFile } from "node:fs/promises"
import { join } from "node:path"
import { checkinWorkBuddy } from "../src/checkin.js"
import { createProviderCheckin } from "../src/checkin-runner.js"
import {
  finishCommand,
  readState,
  recordCommandResult,
  withLock,
  writeState,
} from "../src/commands.js"
import {
  defaultKeepaliveModel,
  successfulActivity,
} from "../src/inventory.js"
import { appendDailyLog } from "../src/log.js"
import { runCommand } from "../src/runner.js"
import { writeAtomic } from "../src/snapshot.js"
import {
  acquireLock,
  readDocument,
  runDailyOnce,
  writeDocument,
} from "../src/state.js"
import {
  createProviderKeepalive,
  saveAuthEntry,
  withAuthLock,
} from "../src/workbuddy.js"
import { tempDir } from "./support.js"

const cleanups = []
const realFetch = globalThis.fetch
afterEach(async () => {
  globalThis.fetch = realFetch
  while (cleanups.length) {
    const cleanup = cleanups.pop()
    if (cleanup) await cleanup()
  }
})

async function dir(prefix = "quota-coverage-") {
  const item = await tempDir(prefix)
  cleanups.push(item.cleanup)
  return item
}

function codedError(code, message = code) {
  return Object.assign(new Error(message), { code })
}

function handle(overrides = {}) {
  return {
    writeFile: async () => {},
    sync: async () => {},
    close: async () => {},
    ...overrides,
  }
}

function fileOps(overrides = {}) {
  return {
    chmod: async () => {},
    mkdir: async () => {},
    open: async () => handle(),
    readFile: async () => "",
    rename: async () => {},
    stat: async () => ({ mtimeMs: Date.now() }),
    unlink: async () => {},
    ...overrides,
  }
}

describe("command and state filesystem edges", () => {
  test("reports non-missing read and write cleanup failures", async () => {
    const tmp = await dir()
    await expect(readState(tmp.dir)).rejects.toMatchObject({ code: "EISDIR" })

    const circular = { version: 1 }
    circular.self = circular
    await expect(writeState(tmp.path("circular.json"), circular)).rejects.toThrow()
  })

  test("retries command locks when a contender disappears or releases", async () => {
    let opens = 0
    const vanished = fileOps({
      open: async () => {
        opens++
        if (opens === 1) throw codedError("EEXIST")
        return handle()
      },
      stat: async () => { throw codedError("ENOENT") },
    })
    expect(await withLock("/tmp/lock", async () => "ok", 100, vanished)).toBe("ok")

    let delayed = 0
    const released = fileOps({
      open: async () => {
        delayed++
        if (delayed === 1) throw codedError("EEXIST")
        return handle()
      },
      stat: async () => ({ mtimeMs: Date.now() }),
    })
    expect(await withLock("/tmp/lock", async () => "later", 100, released)).toBe("later")

    let staleOpens = 0
    let staleUnlinks = 0
    const stale = fileOps({
      open: async () => {
        staleOpens++
        if (staleOpens === 1) throw codedError("EEXIST")
        return handle()
      },
      stat: async () => ({ mtimeMs: Date.now() - 31_000 }),
      unlink: async () => {
        staleUnlinks++
        throw codedError("EIO")
      },
    })
    expect(await withLock("/tmp/lock", async () => "recovered", 100, stale))
      .toBe("recovered")
    expect(staleUnlinks).toBe(2)
  })

  test("covers lock cleanup callbacks and repeated command filters", async () => {
    const tmp = await dir()
    const closeFailure = fileOps({
      open: async () => handle({
        writeFile: async () => { throw new Error("disk full") },
        close: async () => { throw codedError("EIO") },
      }),
      unlink: async () => { throw codedError("EIO") },
    })
    await expect(writeState(tmp.path("state.json"), { version: 1 }, closeFailure))
      .rejects.toThrow("disk full")

    let opens = 0
    const lockFailure = fileOps({
      open: async () => {
        opens++
        if (opens === 1) {
          return handle({
            writeFile: async () => { throw codedError("EEXIST") },
            close: async () => { throw codedError("EIO") },
          })
        }
        return handle()
      },
      stat: async () => ({ mtimeMs: Date.now() - 5_000 }),
      unlink: async () => { throw codedError("EIO") },
    })
    expect(await withLock("/tmp/lock", async () => "ok", 100, lockFailure)).toBe("ok")

    const path = tmp.path("command.json")
    const state = {
      version: 1,
      current: { id: "run", action: "checkin", status: "queued" },
      consumed: ["old"],
      keepaliveClaims: [],
    }
    await writeState(path, state)
    expect(await finishCommand({ commandPath: path, id: "run", status: "success" }))
      .toMatchObject({ status: "success" })

    state.current.status = "queued"
    state.consumed = ["old"]
    await writeState(path, state)
    expect(await recordCommandResult({
      commandPath: path,
      id: "run",
      status: "success",
      accounts: [],
    })).toMatchObject({ status: "success" })
  })

  test("covers state document recovery and lock retry branches", async () => {
    const tmp = await dir()
    await expect(readDocument(tmp.dir)).rejects.toThrow("cannot read")

    let renames = 0
    const retrying = fileOps({
      rename: async () => {
        renames++
        if (renames < 3) throw codedError("EACCES")
      },
    })
    await writeDocument(tmp.path("state.json"), { version: 1, accounts: {} }, retrying)
    expect(renames).toBe(3)

    const writeFailure = fileOps({
      open: async () => handle({ writeFile: async () => { throw new Error("disk full") } }),
    })
    await expect(writeDocument(tmp.path("bad.json"), {}, writeFailure)).rejects.toThrow("disk full")

    const cleanupFailure = fileOps({
      open: async () => handle({
        writeFile: async () => { throw new Error("disk full") },
        close: async () => { throw codedError("EIO") },
      }),
      readFile: async () => { throw codedError("ENOENT") },
      unlink: async () => { throw codedError("EIO") },
    })
    await expect(writeDocument(tmp.path("cleanup.json"), {}, cleanupFailure))
      .rejects.toThrow("disk full")

    const writeOpenFailure = fileOps({
      open: async () => handle({
        writeFile: async () => { throw codedError("EIO") },
        close: async () => { throw codedError("EIO") },
      }),
    })
    await expect(acquireLock("/tmp/lock", 0, 1_000, writeOpenFailure))
      .rejects.toThrow("cannot lock")

    let vanishedOpens = 0
    const vanishedLock = fileOps({
      open: async () => {
        vanishedOpens++
        if (vanishedOpens === 1) throw codedError("EEXIST")
        return handle()
      },
      stat: async () => { throw codedError("ENOENT") },
    })
    const release = await acquireLock("/tmp/lock", 100, 1_000, vanishedLock)
    await release()

    let staleOpens = 0
    const staleLock = fileOps({
      open: async () => {
        staleOpens++
        if (staleOpens === 1) throw codedError("EEXIST")
        return handle()
      },
      stat: async () => ({ mtimeMs: Date.now() - 5_000 }),
    })
    await (await acquireLock("/tmp/lock", 100, 1_000, staleLock))()

    let freshOpens = 0
    const freshLock = fileOps({
      open: async () => {
        freshOpens++
        if (freshOpens === 1) throw codedError("EEXIST")
        return handle()
      },
      stat: async () => ({ mtimeMs: Date.now() }),
    })
    await (await acquireLock("/tmp/lock", 100, 1_000, freshLock))()

    const unlockFailure = fileOps({ unlink: async () => { throw codedError("EIO") } })
    await expect((await acquireLock("/tmp/lock", 100, 1_000, unlockFailure))())
      .rejects.toThrow("EIO")

    let staleFailureOpens = 0
    const staleFailure = fileOps({
      open: async () => {
        staleFailureOpens++
        if (staleFailureOpens === 1) throw codedError("EEXIST")
        return handle()
      },
      stat: async () => ({ mtimeMs: Date.now() - 5_000 }),
      unlink: async () => { throw codedError("EIO") },
    })
    await expect((await acquireLock("/tmp/lock", 100, 1_000, staleFailure))())
      .rejects.toThrow("EIO")
  })

  test("returns unavailable for unreadable state and persists failed tasks honestly", async () => {
    const tmp = await dir()
    const unreadable = await runDailyOnce({
      statePath: tmp.dir,
      account: "a",
      day: "2026-10-30",
      maxAttempts: 2,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => "never",
    })
    expect(unreadable.kind).toBe("unavailable")

    const existing = tmp.path("existing.json")
    await writeFile(existing, JSON.stringify({
      version: 1,
      accounts: { a: { day: "2026-10-30", status: "ok", attempts: 1 } },
    }), "utf8")
    expect(await runDailyOnce({
      statePath: existing,
      account: "a",
      day: "2026-10-30",
      maxAttempts: 2,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => "never",
    })).toMatchObject({ kind: "skipped", reason: "done-today" })

    const failedResult = tmp.path("failed-result.json")
    let saves = 0
    const failed = await runDailyOnce({
      statePath: failedResult,
      account: "a",
      day: "2026-10-30",
      maxAttempts: 2,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => { throw new Error("provider failed") },
      writeState: async () => {
        saves++
        if (saves > 1) throw new Error("disk full")
      },
    })
    expect(failed).toMatchObject({ kind: "sent", ok: false, persisted: false })

    const releaseFailureOps = fileOps({
      readFile: async () => { throw codedError("ENOENT") },
      unlink: async () => { throw codedError("EIO") },
    })
    expect(await runDailyOnce({
      statePath: tmp.path("release.json"),
      account: "a",
      day: "2026-10-30",
      maxAttempts: 2,
      lockWaitMs: 100,
      timeoutMs: 100,
      task: async () => "ok",
      fileOps: releaseFailureOps,
    })).toMatchObject({ kind: "sent", ok: true })
  })
})

describe("provider integration edges", () => {
  test("retries the auth lock without weakening stale-lock handling", async () => {
    let opens = 0
    const vanished = fileOps({
      open: async () => {
        opens++
        if (opens === 1) throw codedError("EEXIST")
        return handle()
      },
      stat: async () => { throw codedError("ENOENT") },
    })
    expect(await withAuthLock("/tmp/auth.json", async () => "ok", 100, vanished)).toBe("ok")

    let delayed = 0
    const released = fileOps({
      open: async () => {
        delayed++
        if (delayed === 1) throw codedError("EEXIST")
        return handle()
      },
      stat: async () => ({ mtimeMs: Date.now() }),
    })
    expect(await withAuthLock("/tmp/auth.json", async () => "later", 100, released)).toBe("later")

    let cleanupOpens = 0
    const cleanupFailure = fileOps({
      open: async () => {
        cleanupOpens++
        if (cleanupOpens === 1) {
          return handle({
            writeFile: async () => { throw codedError("EEXIST") },
            close: async () => { throw codedError("EIO") },
          })
        }
        return handle()
      },
      stat: async () => ({ mtimeMs: Date.now() - 31_000 }),
      unlink: async () => { throw codedError("EIO") },
    })
    expect(await withAuthLock("/tmp/auth.json", async () => "clean", 100, cleanupFailure))
      .toBe("clean")
  })

  test("cleans up failed auth writes and rejects a loader without fetch", async () => {
    const tmp = await dir()
    const circular = {}
    circular.self = circular
    const cleanupFailure = fileOps({
      open: async () => handle({
        writeFile: async () => { throw new Error("disk full") },
        close: async () => { throw codedError("EIO") },
      }),
      readFile: async () => { throw codedError("ENOENT") },
      unlink: async () => { throw codedError("EIO") },
    })
    await expect(saveAuthEntry(
      tmp.path("plugin-auth.json"),
      "key",
      circular,
      cleanupFailure,
    )).rejects.toThrow("JSON.stringify")

    const config = {
      logPath: tmp.path("daily.log.jsonl"),
      statePath: tmp.path("state.json"),
      maxAttempts: 1,
      modelCandidates: 1,
      inputTokens: 1_000,
      timeoutMs: 100,
      lockWaitMs: 100,
    }
    const run = createProviderKeepalive({
      config,
      configHome: tmp.dir,
      loadFactory: async () => async () => ({
        auth: { loader: async () => ({}) },
      }),
    })
    await writeFile(join(tmp.dir, "plugin-providers.json"), JSON.stringify([{
      id: "workbuddy",
      name: "WorkBuddy",
      models: [{ id: "free", free: true }],
      accounts: [{ key: "k", accountId: "a" }],
    }]), "utf8")
    await writeFile(join(tmp.dir, "plugin-auth.json"), JSON.stringify({
      k: { uid: "u" },
    }), "utf8")
    const result = await run({ force: true })
    expect(result.status).toBe("failed")
    expect(result.accounts[0].message).toContain("no signed request")
    expect(typeof createProviderCheckin()).toBe("function")
    expect(typeof createProviderKeepalive()).toBe("function")

    const brokenHome = tmp.path("broken")
    await mkdir(join(brokenHome, "plugin-providers.json"), { recursive: true })
    await expect(createProviderKeepalive({
      config,
      configHome: brokenHome,
    })()).rejects.toMatchObject({ code: "EISDIR" })
  })

  test("handles an unconfirmed WorkBuddy claim", async () => {
    globalThis.fetch = async (input) => {
      const path = new URL(String(input)).pathname
      if (path.endsWith("/checkin-activity-status")) {
        return new Response(JSON.stringify({
          code: 0,
          data: { active: true, today_checked_in: false },
        }), { status: 200, headers: { "content-type": "application/json" } })
      }
      return new Response(JSON.stringify({ code: 0, data: {} }), {
        status: 200,
        headers: { "content-type": "application/json" },
      })
    }
    const result = await checkinWorkBuddy({ fetchImpl: globalThis.fetch, timeoutMs: 100 })
    expect(result.outcome).toBe("failed")
    expect(result.message).toContain("did not confirm")
  })
})

describe("ranking, logs, and runner edges", () => {
  test("covers equal and unrelated ranking/activity inputs", () => {
    expect(successfulActivity(null, "checkin")).toBe(false)
    expect(successfulActivity({ status: "skipped", outcome: "already-done" }, "keepalive")).toBe(true)
    expect(successfulActivity({ status: "skipped" }, "unknown")).toBe(false)
    expect(defaultKeepaliveModel([{ id: "same" }, { id: "same", name: "Same" }]).id).toBe("same")
  })

  test("reports log append failure after a successful preflight", async () => {
    const tmp = await dir()
    await writeFile(tmp.path("parent-file"), "x", "utf8")
    const options = {
      logPath: tmp.path("parent-file", "daily.log.jsonl"),
      entry: { ts: new Date().toISOString(), event: "checkin", status: "success" },
    }
    expect(await Promise.all([
      appendDailyLog(options),
      appendDailyLog(options),
    ])).toEqual([false, false])
    expect((await stat(tmp.path("parent-file"))).isFile()).toBe(true)
  })

  test("cleans up failed snapshot writes even when cleanup also fails", async () => {
    const tmp = await dir()
    const ops = {
      chmod: async () => {},
      mkdir: async () => {},
      open: async () => handle({
        writeFile: async () => { throw new Error("disk full") },
        close: async () => { throw codedError("EIO") },
      }),
      rename: async () => {},
      unlink: async () => { throw codedError("EIO") },
    }
    await expect(writeAtomic(tmp.path("snapshot.js"), "x", ops)).rejects.toThrow("disk full")
  })

  test("uses injected check-in and marks a combined run failed when either side fails", async () => {
    const tmp = await dir()
    const config = {
      configHome: tmp.path("config"),
      logPath: tmp.path("daily.log.jsonl"),
      logMaxBytes: 64 * 1024,
      snapshotPath: tmp.path("ui", "data.js"),
      commandPath: tmp.path("command.json"),
      timeoutMs: 1_000,
    }
    let forced = null
    const success = await runCommand({
      action: "checkin",
      config,
      checkin: async ({ force }) => {
        forced = force
        return {
          status: "success",
          summary: "签到完成",
          accounts: [{ provider: "p", account: "a", action: "checkin", status: "success", outcome: "claimed" }],
        }
      },
      force: true,
    })
    expect(success.status).toBe("success")
    expect(forced).toBe(true)

    const allFailed = await runCommand({
      action: "all",
      config,
      checkin: async () => ({ status: "failed", summary: "签到失败", accounts: [] }),
      keepalive: async () => ({ status: "success", summary: "保活完成", accounts: [] }),
    })
    expect(allFailed.status).toBe("failed")
    expect(allFailed.summary).toContain("签到失败")
  })
})
