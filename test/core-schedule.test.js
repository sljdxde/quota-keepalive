import { afterEach, describe, expect, test } from "bun:test"
import { chmod, mkdir, open, readFile, rename, unlink, writeFile } from "node:fs/promises"
import {
  defaultSchedulePath,
  emptySchedule,
  nextRunAfter,
  readSchedule,
  scheduleSignature,
  writeSchedule,
} from "../src/schedule.js"
import { tempDir } from "./support.js"

const cleanups = []
afterEach(async () => {
  while (cleanups.length) {
    const cleanup = cleanups.pop()
    if (cleanup) await cleanup()
  }
})

async function dir(prefix = "quota-schedule-") {
  const item = await tempDir(prefix)
  cleanups.push(item.cleanup)
  return item
}

function codedError(code) {
  return Object.assign(new Error(code), { code })
}

describe("persisted scheduler state", () => {
  test("reads missing, valid, and invalid schedule files", async () => {
    expect(defaultSchedulePath().endsWith("quota-keepalive-schedule.json")).toBe(true)
    const tmp = await dir()
    const path = tmp.path("schedule.json")
    expect(await readSchedule(path)).toEqual(emptySchedule())

    await writeSchedule(path, {
      version: 1,
      signature: "x".repeat(2_100),
      lastRunAt: "2026-10-30T01:00:00.000Z",
      nextRunAt: "not-a-date",
      lastStatus: "success",
      lastSummary: "ok",
    })
    const saved = await readSchedule(path)
    expect(saved.signature).toHaveLength(2_000)
    expect(saved.lastRunAt).toBe("2026-10-30T01:00:00.000Z")
    expect(saved.nextRunAt).toBe("")

    await writeFile(path, "not-json", "utf8")
    await expect(readSchedule(path)).rejects.toThrow("not valid JSON")
    await writeFile(path, "[]", "utf8")
    await expect(readSchedule(path)).rejects.toThrow("unsupported shape")
    await expect(readSchedule(tmp.dir)).rejects.toThrow("cannot read")
  })

  test("cleans up failed schedule writes", async () => {
    const tmp = await dir()
    const path = tmp.path("schedule.json")
    const ops = {
      chmod,
      mkdir,
      open: async () => ({
        writeFile: async () => { throw new Error("disk full") },
        sync: async () => {},
        close: async () => { throw new Error("close failed") },
      }),
      readFile,
      rename,
      unlink: async () => { throw codedError("EIO") },
    }
    await expect(writeSchedule(path, emptySchedule(), ops)).rejects.toThrow("disk full")
  })

  test("computes a stable next run across restarts", () => {
    const now = Date.parse("2026-10-30T12:00:00.000Z")
    expect(scheduleSignature({ b: 1, a: 2 })).toBe('{"b":1,"a":2}')
    expect(nextRunAfter({ now, intervalMs: 60_000 })).toBe(now + 60_000)
    expect(nextRunAfter({
      now,
      intervalMs: 60_000,
      storedSignature: "old",
      signature: "new",
    })).toBe(now + 60_000)
    expect(nextRunAfter({
      now,
      intervalMs: 60_000,
      storedSignature: "same",
      signature: "same",
      nextRunAt: "2026-10-30T12:01:00.000Z",
    })).toBe(now + 60_000)
    expect(nextRunAfter({
      now,
      intervalMs: 60_000,
      storedSignature: "same",
      signature: "same",
      nextRunAt: "2026-10-30T12:05:00.000Z",
    })).toBe(now + 300_000)
    expect(nextRunAfter({
      now,
      intervalMs: 60_000,
      storedSignature: "same",
      signature: "same",
      lastRunAt: "2026-10-30T11:58:00.000Z",
    })).toBe(now)
    expect(nextRunAfter({ now, intervalMs: 0 })).toBe(now + 60_000)
  })
})
