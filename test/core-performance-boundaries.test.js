import { afterEach, describe, expect, test } from "bun:test"
import { mkdir, writeFile } from "node:fs/promises"
import { beijingDay } from "../src/common.js"
import {
  maskAccount,
  rankedModels,
  readInventory,
} from "../src/inventory.js"
import { accountTag } from "../src/log.js"
import { buildRunHistory, summarize } from "../src/snapshot.js"
import { tempDir } from "./support.js"

const cleanups = []

afterEach(async () => {
  while (cleanups.length) {
    const cleanup = cleanups.pop()
    if (cleanup) await cleanup()
  }
})

async function dir(prefix = "quota-performance-") {
  const item = await tempDir(prefix)
  cleanups.push(item.cleanup)
  return item
}

function timed(work) {
  const started = performance.now()
  const result = work()
  return { result, elapsedMs: performance.now() - started }
}

async function timedAsync(work) {
  const started = performance.now()
  const result = await work()
  return { result, elapsedMs: performance.now() - started }
}

describe("performance budgets", () => {
  test("reads a large multi-provider inventory within a bounded time", async () => {
    const tmp = await dir()
    const today = beijingDay()
    const providerCount = 60
    const accountsPerProvider = 10
    const providers = []
    const checkins = {}
    const quotas = {}
    const histories = {}
    const records = []

    for (let providerIndex = 0; providerIndex < providerCount; providerIndex++) {
      const provider = `bulk-${providerIndex}`
      const accounts = []
      for (let accountIndex = 0; accountIndex < accountsPerProvider; accountIndex++) {
        const accountId = `${provider}-user-${accountIndex}`
        const key = `${provider}#${accountIndex}`
        accounts.push({
          key,
          accountId,
          type: "oauth",
          models: ["m0", "m1", "m2", "m3", "m4"],
        })
        checkins[`${provider}|${key}`] = {
          day: today,
          at: `${today}T01:00:00.000Z`,
          outcome: "claimed",
          credit: 10,
          streak: 1,
          msg: "ok",
        }
        quotas[`${provider}/${accountId}`] = {
          at: `${today}T01:01:00.000Z`,
          quota: {
            plan: "free",
            windows: [{ name: "Credits", used: 1, remaining: 99, display: "1 / 100" }],
          },
        }
        histories[`${provider}|${accountId}`] = {
          Credits: Array.from({ length: 50 }, (_, index) => ({
            at: `${today}T${String(index % 24).padStart(2, "0")}:00:00.000Z`,
            left: 100 - index,
          })),
        }
        const account = accountTag(`${provider}|${accountId}`)
        for (let dayOffset = 0; dayOffset < 5; dayOffset++) {
          records.push(
            {
              ts: `${today}T0${dayOffset}:00:00.000Z`,
              day: today,
              provider,
              account,
              event: "checkin",
              status: "success",
              outcome: "claimed",
              credit: 10,
            },
            {
              ts: `${today}T0${dayOffset}:01:00.000Z`,
              day: today,
              provider,
              account,
              event: "keepalive",
              status: "success",
              outcome: "sent",
              model: "m0",
            },
          )
        }
      }
      providers.push({
        id: provider,
        name: provider,
        checkin: true,
        models: Array.from({ length: 5 }, (_, index) => ({
          id: `m${index}`,
          name: `Model ${index}`,
          rate: index,
        })),
        accounts,
      })
    }

    await Promise.all([
      writeFile(tmp.path("plugin-providers.json"), JSON.stringify(providers), "utf8"),
      writeFile(tmp.path("plugin-checkin.json"), JSON.stringify(checkins), "utf8"),
      writeFile(tmp.path("quotas.json"), JSON.stringify(quotas), "utf8"),
      writeFile(tmp.path("quota-history.json"), JSON.stringify(histories), "utf8"),
      writeFile(tmp.path("plugin-auth.json"), "{}", "utf8"),
    ])

    const { result: inventory, elapsedMs } = await timedAsync(() =>
      readInventory({ configHome: tmp.dir, records, accountsData: [] })
    )
    expect(elapsedMs).toBeLessThan(4_000)
    expect(inventory.providers).toHaveLength(providerCount)
    expect(inventory.accounts).toHaveLength(providerCount * accountsPerProvider)
    expect(inventory.providers[0].accounts[0].history).toHaveLength(40)
    expect(inventory.summary).toBeUndefined()
  })

  test("ranks thousands of models and caps the candidate list", () => {
    const models = Array.from({ length: 5_000 }, (_, index) => ({
      id: `model-${index}`,
      name: `Model ${index}`,
      rate: index % 17,
      context: 200_000,
    }))
    const { result, elapsedMs } = timed(() =>
      rankedModels(models, [], { maxCandidates: 99 })
    )
    expect(elapsedMs).toBeLessThan(1_500)
    expect(result).toHaveLength(12)
    expect(result[0].id).toBe("model-0")
  })

  test("groups a large run log and keeps only the newest 16 runs", () => {
    const base = Date.parse("2026-10-30T00:00:00.000Z")
    const records = []
    for (let anchorIndex = 0; anchorIndex < 40; anchorIndex++) {
      const anchorAt = base + anchorIndex * 20 * 60_000
      records.push({
        ts: new Date(anchorAt).toISOString(),
        day: "2026-10-30",
        provider: "daily-keepalive",
        event: "checkin",
        status: "success",
        run: `run-${anchorIndex}`,
      })
      for (let childIndex = 0; childIndex < 200; childIndex++) {
        records.push({
          ts: new Date(anchorAt - (childIndex + 1) * 1_000).toISOString(),
          day: "2026-10-30",
          provider: `provider-${childIndex % 12}`,
          account: accountTag(`provider-${childIndex % 12}|account-${childIndex}`),
          event: "checkin",
          status: childIndex % 19 === 0 ? "failed" : "success",
          run: `run-${anchorIndex}`,
        })
      }
    }
    const { result, elapsedMs } = timed(() => buildRunHistory(records))
    expect(elapsedMs).toBeLessThan(2_000)
    expect(result).toHaveLength(16)
    expect(result[0].id).toBe("run-39")
    expect(result[0].children).toHaveLength(200)
    expect(result[15].id).toBe("run-24")
  })

  test("summarizes a large record set in one bounded pass", () => {
    const today = beijingDay()
    const records = Array.from({ length: 100_000 }, (_, index) => ({
      ts: `${today}T01:${String(index % 60).padStart(2, "0")}:00.000Z`,
      day: today,
      event: index % 2 ? "keepalive" : "checkin",
      status: index % 11 === 0 ? "failed" : "success",
      account: accountTag(`provider|account-${index % 5_000}`),
    }))
    const accounts = Array.from({ length: 5_000 }, (_, index) => ({
      provider: "provider",
      label: `account-${index}`,
      activity: { today: {
        checkin: { success: index % 2 === 0, status: index % 2 === 0 ? "success" : "skipped" },
        keepalive: { success: index % 2 === 1, status: index % 2 === 1 ? "success" : "skipped" },
      } },
    }))
    const { result, elapsedMs } = timed(() => summarize(records, accounts))
    expect(elapsedMs).toBeLessThan(1_500)
    expect(result.today.accounts).toBe(5_000)
    expect(result.today.checkinDone).toBe(2_500)
    expect(result.today.keepaliveDone).toBe(2_500)
    expect(result.latestAt).toBe(`${today}T01:59:00.000Z`)
    expect(result.failures).toBeGreaterThan(9_000)
  })
})

describe("boundary inputs", () => {
  test("clamps candidate limits and filters malformed model lists", () => {
    const models = Array.from({ length: 20 }, (_, index) => ({
      id: `m-${index}`,
      context: index === 0 ? 516 : 517,
    }))
    expect(rankedModels(models, "not-an-array", {
      inputTokens: 500,
      maxCandidates: Number.POSITIVE_INFINITY,
    }).map((model) => model.id)).toEqual([
      "m-1", "m-2", "m-3", "m-4", "m-5", "m-6",
      "m-7", "m-8", "m-9", "m-10", "m-11", "m-12",
    ])
    expect(rankedModels(models, ["", "  "], { maxCandidates: -1 })).toHaveLength(1)
    expect(rankedModels(models, [], { maxCandidates: Number.NaN })).toHaveLength(4)
    expect(rankedModels([null, 1, "bad", { id: "" }], [], { maxCandidates: 0 })).toEqual([])
  })

  test("masks extreme account identifiers without leaking the full value", () => {
    const long = `${"a".repeat(40)}@${"b".repeat(100)}.example`
    const masked = maskAccount({ accountId: long }, 99)
    expect(masked.length).toBeLessThanOrEqual(120)
    expect(masked).not.toBe(long)
    expect(maskAccount({ accountId: "   " }, 3)).toBe("account-4")
  })

  test("keeps run history stable for malformed records and event limits", () => {
    const base = Date.parse("2026-10-30T00:00:00.000Z")
    const records = [
      null,
      1,
      "bad",
      {},
      {
        ts: new Date(base).toISOString(),
        provider: "daily-keepalive",
        event: "detect",
        status: "success",
      },
      {
        ts: new Date(base + 1_000).toISOString(),
        provider: "quota-keepalive",
        event: "unknown",
        status: "success",
      },
    ]
    const groups = buildRunHistory(records)
    expect(groups).toHaveLength(1)
    expect(groups[0]).toMatchObject({
      event: "detect",
      status: "success",
      children: [],
      accounts: 0,
    })
  })
})
