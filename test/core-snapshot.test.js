import { afterEach, describe, expect, test } from "bun:test"
import { mkdir, readFile, readdir, writeFile } from "node:fs/promises"
import { join } from "node:path"
import {
  buildRunHistory,
  buildTodayRunHistory,
  carryForwardQuotas,
  defaultSnapshotPath,
  publicCommand,
  publicRecord,
  readPreviousSnapshot,
  snapshotWarning,
  summarize,
  writeAtomic,
  writeDailySnapshot,
} from "../src/snapshot.js"
import { enqueueCommand, claimCommand } from "../src/commands.js"
import { appendDailyLog } from "../src/log.js"
import { tempDir } from "./support.js"

const cleanups = []
afterEach(async () => {
  while (cleanups.length) {
    const cleanup = cleanups.pop()
    if (cleanup) await cleanup()
  }
})

async function dir(prefix = "quota-snapshot-") {
  const item = await tempDir(prefix)
  cleanups.push(item.cleanup)
  return item
}

describe("public snapshot records", () => {
  test("sanitizes records and drops invalid account tags", () => {
    const record = publicRecord({
      ts: "2026-10-30T01:00:00.000Z",
      day: "2026-10-30",
      provider: "p",
      event: "checkin",
      status: "success",
      outcome: "claimed",
      model: "m",
      account: "not-a-tag",
      run: "r",
      attempts: 1,
      inputTokens: 2,
      credit: 3,
      streak: 4,
      durationMs: 5,
      retry: true,
      persisted: false,
      keepalive: true,
      message: "token=secret",
      error: "Bearer x",
      ignored: "x",
    })
    expect(record).toMatchObject({
      attempts: 1,
      retry: true,
      persisted: false,
      keepalive: true,
      message: "token=[redacted]",
      error: "Bearer [redacted]",
    })
    expect(record.account).toBeUndefined()
    expect(record.ignored).toBeUndefined()
    expect(publicRecord({ account: "acct-123456789012" }).account).toBe("acct-123456789012")
    expect(publicRecord({ status: 1, message: 1, error: "" })).toEqual({})
  })

  test("validates and bounds command payloads", () => {
    expect(publicCommand(null)).toBeNull()
    expect(publicCommand([])).toBeNull()
    expect(publicCommand({ id: "", action: "x", status: "queued" })).toBeNull()
    const command = publicCommand({
      id: "id",
      action: "checkin",
      provider: "p",
      status: "success",
      createdAt: "a",
      startedAt: "b",
      finishedAt: "c",
      summary: "token=secret",
      error: "Bearer x",
      accounts: Array.from({ length: 82 }, () => ({
        provider: "p",
        account: "a",
        action: "checkin",
        status: "success",
        outcome: "claimed",
        message: "ok",
        extra: "drop",
      })),
    })
    expect(command).toMatchObject({ id: "id", action: "checkin", provider: "p", status: "success" })
    expect(command.summary).toBe("token=[redacted]")
    expect(command.accounts).toHaveLength(80)
    expect(command.accounts[0]).not.toHaveProperty("extra")
  })
})

describe("run history and quota carry-forward", () => {
  test("groups anchors and children across every action type", () => {
    const base = Date.parse("2026-10-30T01:00:00.000Z")
    const iso = (offset) => new Date(base + offset).toISOString()
    const records = [
      { provider: "daily-keepalive", event: "detect", status: "success", ts: iso(0), day: "2026-10-30", run: "detect" },
      { provider: "p", event: "checkin", status: "success", ts: iso(-1), day: "2026-10-30", account: "a1", run: "r1" },
      { provider: "p", event: "checkin", status: "failed", ts: iso(-2), day: "2026-10-30", account: "a2", run: "r1" },
      { provider: "daily-keepalive", event: "checkin", status: "success", ts: iso(10), day: "2026-10-30", run: "r1" },
      { provider: "daily-keepalive", event: "schedule", status: "failed", ts: iso(20), day: "2026-10-30", outcome: "failed", error: "bad", run: "s" },
      { provider: "p", event: "keepalive-model", status: "failed", ts: iso(21), day: "2026-10-30", account: "a1" },
      { provider: "p", event: "keepalive", status: "success", ts: iso(22), day: "2026-10-30", account: "a1" },
      { provider: "daily-keepalive", event: "all", status: "success", ts: iso(30), day: "2026-10-30", run: "all" },
      { provider: "p", event: "checkin", status: "success", ts: "bad", day: "2026-10-30" },
      { provider: "p", event: "checkin", status: "success", ts: iso(31), day: "2026-10-30" },
      { provider: "p", event: "keepalive", status: "success", ts: iso(-100), day: "2026-10-30", account: "too-old" },
      { provider: "p", event: "keepalive", status: "success", ts: iso(32), day: "2026-10-29", account: "wrong-day" },
      { provider: "p", event: "refresh", status: "success", ts: iso(39), day: "2026-10-30", run: "refresh" },
      { provider: "quota-keepalive", event: "refresh", status: "success", ts: iso(40), day: "2026-10-30", run: "refresh" },
      { provider: "daily-keepalive", event: "keepalive", status: "success", ts: "bad", day: "2026-10-30" },
      null,
    ]
    const groups = buildRunHistory(records)
    expect(groups).toHaveLength(5)
    expect(groups.map((group) => Date.parse(group.at))).toEqual(
      groups.map((group) => Date.parse(group.at)).sort((left, right) => right - left),
    )
    expect(groups[0]).toMatchObject({ event: "refresh", id: "refresh" })
    expect(groups[0].children).toHaveLength(1)
    expect(groups[0].providers).toEqual(["p"])
    expect(groups.find((group) => group.event === "all")).toMatchObject({ id: "all" })
    expect(groups.find((group) => group.event === "schedule")).toMatchObject({
      status: "failed",
      message: "bad",
    })
    expect(groups.find((group) => group.event === "checkin").children.length).toBeGreaterThan(0)
    expect(buildRunHistory(null)).toEqual([])
  })

  test("surfaces standalone provider keepalive runs without a summary anchor", () => {
    const base = Date.parse("2026-10-30T01:00:00.000Z")
    const iso = (offset) => new Date(base + offset).toISOString()
    const day = "2026-10-30"
    const records = [
      {
        provider: "p",
        event: "started",
        status: "started",
        keepalive: true,
        account: "a1",
        ts: iso(0),
        day,
        run: "ok",
      },
      {
        provider: "p",
        event: "keepalive-model",
        status: "failed",
        account: "a1",
        ts: iso(1_000),
        day,
        run: "ok",
      },
      {
        provider: "p",
        event: "keepalive",
        status: "success",
        outcome: "sent",
        account: "a1",
        ts: iso(2_000),
        day,
        run: "ok",
      },
      {
        provider: "p",
        event: "started",
        status: "started",
        keepalive: true,
        account: "a2",
        ts: iso(3_000),
        day,
        run: "bad",
      },
      {
        provider: "p",
        event: "keepalive",
        status: "failed",
        outcome: "failed",
        account: "a2",
        ts: iso(4_000),
        day,
        run: "bad",
        error: "bad",
      },
      {
        provider: "p",
        event: "started",
        status: "started",
        keepalive: true,
        account: "a3",
        ts: iso(5_000),
        day,
        run: "open",
      },
      {
        provider: "p",
        event: "started",
        status: "started",
        keepalive: true,
        account: "a4",
        ts: "bad",
        day,
        run: "invalid-time",
      },
      {
        provider: "p",
        event: "keepalive",
        status: "success",
        outcome: "sent",
        account: "a4",
        ts: iso(6_000),
        day,
        run: "invalid-time",
      },
    ]
    const groups = buildRunHistory(records)
    expect(groups).toHaveLength(3)
    expect(groups[0]).toMatchObject({
      id: "invalid-time",
      durationMs: 0,
    })
    expect(groups[1]).toMatchObject({
      id: "bad",
      event: "keepalive",
      status: "failed",
      message: "bad",
      providers: ["p"],
      accounts: 1,
      successes: 0,
      failures: 1,
      durationMs: 1_000,
    })
    expect(groups[2]).toMatchObject({
      id: "ok",
      event: "keepalive",
      status: "success",
      message: "保活成功。",
      accounts: 1,
      successes: 1,
      failures: 0,
      durationMs: 2_000,
    })
    expect(groups[2].children).toHaveLength(3)
  })

  test("lists every run for one day including skipped runs without child details", () => {
    const records = [
      {
        provider: "quota-keepalive",
        event: "schedule",
        status: "success",
        ts: "2026-10-30T01:00:00.000Z",
        day: "2026-10-30",
        run: "success",
        message: "done",
      },
      {
        provider: "p",
        event: "checkin",
        status: "success",
        ts: "2026-10-30T00:59:00.000Z",
        day: "2026-10-30",
        run: "success",
      },
      {
        provider: "quota-keepalive",
        event: "schedule",
        status: "skipped",
        ts: "2026-10-30T02:00:00.000Z",
        day: "2026-10-30",
        run: "skipped",
        message: "already done",
      },
      {
        provider: "quota-keepalive",
        event: "schedule",
        status: "failed",
        ts: "2026-10-29T02:00:00.000Z",
        day: "2026-10-29",
        run: "older",
      },
    ]
    const runs = buildTodayRunHistory(records, "2026-10-30")
    expect(runs.map((run) => run.id)).toEqual(["skipped", "success"])
    expect(runs[0]).toMatchObject({ status: "skipped", message: "already done" })
    expect(runs[1]).toMatchObject({ status: "success", successes: 1 })
    expect(runs.every((run) => !("children" in run))).toBe(true)
    expect(buildTodayRunHistory(records, "2026-10-28")).toEqual([])
  })

  test("carries only missing quotas from a previous snapshot", () => {
    const inventory = {
      providers: [{
        id: "p",
        name: "P",
        accounts: [
          { label: "existing", quota: { plan: "new" } },
          { label: "missing" },
          { label: "unknown" },
        ],
      }],
      accounts: [],
    }
    const previous = {
      providers: [
        null,
        { id: "p", accounts: [
          null,
          { label: "existing", quota: { plan: "old" } },
          { label: "missing", quota: { plan: "carried" } },
          { label: "", quota: { plan: "skip" } },
        ] },
      ],
    }
    const result = carryForwardQuotas(inventory, previous)
    expect(result.providers[0].accounts.map((account) => account.quota)).toEqual([
      { plan: "new" },
      { plan: "carried" },
      null,
    ])
    expect(result.accounts).toHaveLength(3)
    expect(carryForwardQuotas(inventory, null)).toBe(inventory)
    expect(carryForwardQuotas(null, previous)).toBeNull()
  })

  test("reads JSON and JavaScript snapshots and tolerates bad data", async () => {
    const tmp = await dir()
    const path = tmp.path("ui", "data.js")
    await mkdir(join(tmp.dir, "ui"), { recursive: true })
    await writeFile(path.replace(/\.js$/, ".json"), JSON.stringify({ version: 1 }), "utf8")
    expect(await readPreviousSnapshot(path)).toEqual({ version: 1 })
    await writeFile(path.replace(/\.js$/, ".json"), "{bad", "utf8")
    await writeFile(path, "window.DailyKeepaliveSnapshot = {\"version\":2};\n", "utf8")
    expect(await readPreviousSnapshot(path)).toEqual({ version: 2 })
    await writeFile(path, "other = {};", "utf8")
    expect(await readPreviousSnapshot(path)).toBeNull()
    expect(await readPreviousSnapshot(tmp.path("missing.js"))).toBeNull()
  })
})

describe("snapshot writing", () => {
  test("validates paths, writes both formats, and includes command state", async () => {
    const tmp = await dir()
    const configHome = tmp.path("config")
    const ui = tmp.path("ui")
    await mkdir(ui, { recursive: true })
    const logPath = tmp.path("daily.log.jsonl")
    const snapshotPath = tmp.path("ui", "data.js")
    const commandPath = tmp.path("command.json")
    await writeFile(logPath, "", "utf8")
    await enqueueCommand({ commandPath, action: "detect", id: "detect" })
    await claimCommand({ commandPath, id: "detect" })
    await appendDailyLog({
      logPath,
      entry: {
        ts: "2026-10-30T01:00:00.000Z",
        day: "2026-10-30",
        provider: "daily-keepalive",
        event: "detect",
        status: "success",
        run: "run",
        message: "done",
      },
    })
    await appendDailyLog({
      logPath,
      entry: {
        ts: "2026-10-30T01:01:00.000Z",
        day: "2026-10-30",
        provider: "daily-keepalive",
        event: "checkin",
        status: "success",
        run: "checkin",
        message: "checked in",
      },
    })
    await expect(writeDailySnapshot({ logPath: "relative", snapshotPath })).rejects.toThrow("logPath")
    await expect(writeDailySnapshot({ logPath, snapshotPath: "relative" })).rejects.toThrow("snapshotPath")
    const payload = await writeDailySnapshot({
      logPath,
      snapshotPath,
      configHome,
      commandPath,
      writeCompanion: true,
    })
    expect(payload).toMatchObject({
      version: 5,
      timezone: "Asia/Shanghai",
      logEnabled: true,
      rotated: false,
      malformed: 0,
    })
    expect(payload.command).toMatchObject({ id: "detect", status: "running" })
    expect(payload.records.map((record) => record.event)).toEqual(["checkin"])
    expect(payload.runHistory[0]).toMatchObject({ event: "checkin", id: "checkin" })
    expect(Array.isArray(payload.todayRuns)).toBe(true)
    const companion = JSON.parse(await readFile(snapshotPath.replace(/\.js$/, ".json"), "utf8"))
    expect(companion.version).toBe(5)
    expect(await readFile(snapshotPath, "utf8")).toStartWith("window.DailyKeepaliveSnapshot =")
    expect((await readdir(ui)).sort()).toEqual(["data.js", "data.json"])
  })

  test("writes atomically and reports write failures", async () => {
    const tmp = await dir()
    const path = tmp.path("nested", "file.txt")
    await writeAtomic(path, "hello")
    expect(await readFile(path, "utf8")).toBe("hello")
    await mkdir(tmp.path("target"), { recursive: true })
    await expect(writeAtomic(tmp.path("target"), "x")).rejects.toBeTruthy()
    expect(snapshotWarning(new Error("token=secret"))).toBe(
      "cannot update the daily keepalive page snapshot: token=[redacted]",
    )
  })

  test("summarizes today's account activity and all-time failures", () => {
    const today = new Date().toISOString().slice(0, 10)
    const records = [
      { ts: `${today}T01:00:00.000Z`, day: today, event: "checkin", status: "failed", account: "a" },
      { ts: `${today}T01:00:01.000Z`, day: today, event: "keepalive", status: "success", account: "b" },
      { ts: `${today}T01:00:02.000Z`, day: today, event: "schedule", status: "success" },
      { ts: "bad", day: today, event: "checkin", status: "success" },
    ]
    const accounts = [
      { provider: "p", label: "one", activity: { today: {
        checkin: { success: true, status: "success" },
        keepalive: { success: true, status: "success" },
      } } },
      { provider: "p", label: "two", activity: { today: {
        checkin: { success: false, status: "skipped" },
        keepalive: { success: false, status: "failed" },
      } } },
      { provider: "p", label: "three", activity: { today: {
        checkin: { success: false, status: "failed" },
        keepalive: { success: false, status: "skipped" },
      } } },
      { provider: "p", label: "four", activity: { today: {
        checkin: { success: false, status: "other" },
        keepalive: { success: false, status: "other" },
      } } },
      {},
    ]
    expect(summarize(records, accounts)).toEqual({
      today: {
        checkin: { success: 1, skipped: 1, failed: 1 },
        keepalive: { success: 1, skipped: 1, failed: 1 },
        accounts: 5,
        checkinDone: 1,
        keepaliveDone: 1,
        failed: 2,
      },
      latestAt: `${today}T01:00:02.000Z`,
      failures: 1,
    })
    expect(summarize([], [])).toMatchObject({ today: { checkinDone: 0, keepaliveDone: 0, failed: 0 } })
    expect(summarize(null, null)).toEqual({
      today: {
        checkin: { success: 0, skipped: 0, failed: 0 },
        keepalive: { success: 0, skipped: 0, failed: 0 },
        accounts: 0,
        checkinDone: 0,
        keepaliveDone: 0,
        failed: 0,
      },
      latestAt: "",
      failures: 0,
    })
    expect(summarize([null, 1, "bad"], [null, 1])).toMatchObject({
      today: { accounts: 2, failed: 0 },
      failures: 0,
    })
  })
})

test("default snapshot path points at the page data", () => {
  expect(defaultSnapshotPath().endsWith(join("ui", "data.js"))).toBe(true)
})
