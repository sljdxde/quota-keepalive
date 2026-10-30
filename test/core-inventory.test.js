import { afterEach, describe, expect, test } from "bun:test"
import { mkdir, writeFile } from "node:fs/promises"
import { beijingDay } from "../src/common.js"
import {
  KEEPALIVE_PROVIDERS,
  cardKey,
  defaultKeepaliveModel,
  maskAccount,
  quotaByAccount,
  rankedModels,
  readInventory,
} from "../src/inventory.js"
import { accountTag } from "../src/log.js"
import { tempDir } from "./support.js"

const cleanups = []
afterEach(async () => {
  while (cleanups.length) await popCleanup();
})

async function popCleanup() {
  const cleanup = cleanups.pop()
  if (cleanup) await cleanup()
}

async function dir() {
  const item = await tempDir("quota-inventory-")
  cleanups.push(item.cleanup)
  return item
}

describe("model ranking", () => {
  test("uses provider order by default and tolerates malformed input", () => {
    expect(defaultKeepaliveModel(null)).toBeNull()
    expect(defaultKeepaliveModel([null, {}, { id: "" }])).toBeNull()
    expect(defaultKeepaliveModel([
      { id: "paid", name: "Paid", rate: 2, cost: { input: 3, output: 4 } },
      { id: "free-b", name: "Free B", free: true, rate: 1, cost: { input: 2, output: 2 } },
      { id: "free-a", name: "Free A", free: true, rate: 0, cost: { input: 1, output: 1 } },
    ])).toMatchObject({ id: "paid", name: "Paid", free: false, rate: 2, cost: 7 })
    expect(defaultKeepaliveModel([{ id: "tie-a" }, { id: "tie-b" }]).id).toBe("tie-a")
    expect(defaultKeepaliveModel([
      { id: "auto" },
      { id: "deepseek-v4.1-flash" },
    ], {
      provider: "workbuddy",
    }).id).toBe("deepseek-v4.1-flash")
    expect(defaultKeepaliveModel([
      { id: "auto" },
      { id: "deepseek-v4.1-flash" },
    ], {
      provider: "workbuddy",
      preferred: "auto",
    }).id).toBe("auto")
  })

  test("ranks, filters, dedupes, and applies preferred models", () => {
    const models = [
      { id: "equal-a", name: "Equal" },
      { id: "equal-b", name: "Equal" },
      { id: "missing-context", context: 100 },
      { id: "huge", free: true, context: 200_000 },
      { id: "allowed", rate: 1, npm: "@ai-sdk/anthropic", api: { npm: "fallback" } },
      { id: "not-allowed" },
    ]
    const ranked = rankedModels(models, ["equal-a", "equal-b", "missing-context", "huge", "allowed"], {
      inputTokens: 500,
      maxCandidates: 20,
      preferred: "missing",
    })
    expect(ranked.map((model) => model.id)).not.toContain("missing-context")
    expect(ranked[0].id).toBe("equal-a")
    expect(rankedBytes(ranked).length).toBe(4)
    expect(rankedModels(models, [], { maxCandidates: 0 }).length).toBe(4)
    expect(rankedModels([
      { id: "z", rate: 1 },
      { id: "a", rate: 1 },
    ], [], { preferred: "a" })[0].id).toBe("a")
    expect(rankedModels(null)).toEqual([])
  })

  test("ranks recent real availability before price and provider order", () => {
    const day = beijingDay()
    const records = [
      {
        ts: `${day}T02:00:00.000Z`,
        day,
        provider: "p",
        account: accountTag("p|a"),
        event: "keepalive-model",
        model: "cheap",
        status: "failed",
      },
      {
        ts: `${day}T03:00:00.000Z`,
        day,
        provider: "p",
        account: accountTag("p|b"),
        event: "keepalive-model",
        model: "paid",
        status: "success",
      },
    ]
    const models = [
      { id: "cheap", free: true, rate: 0 },
      { id: "paid", rate: 5 },
      { id: "new", rate: 0.1 },
    ]
    expect(rankedModels(models, [], {
      records,
      provider: "p",
      account: accountTag("p|a"),
    }).map((model) => model.id)).toEqual(["paid", "new", "cheap"])
    expect(rankedModels(models, [], {
      records: [...records, {
        ts: `${day}T04:00:00.000Z`,
        day,
        provider: "p",
        account: accountTag("p|a"),
        event: "keepalive-model",
        model: "cheap",
        status: "success",
      }],
      provider: "p",
      account: accountTag("p|a"),
    }).map((model) => model.id)).toEqual(["cheap", "paid", "new"])

    expect(rankedModels([
      { id: "auto" },
      { id: "deepseek-v4.1-flash" },
    ], [], {
      records: [{
        ts: `${day}T05:00:00.000Z`,
        day,
        provider: "workbuddy",
        account: accountTag("workbuddy|a"),
        event: "keepalive-model",
        model: "auto",
        status: "success",
      }],
      provider: "workbuddy",
      account: accountTag("workbuddy|a"),
    })[0].id).toBe("auto")

    expect(rankedModels([
      { id: "auto" },
      { id: "deepseek-v4.1-flash" },
    ], [], {
      records: [{
        ts: `${day}T06:00:00.000Z`,
        day,
        provider: "workbuddy",
        account: accountTag("workbuddy|a"),
        event: "keepalive",
        status: "success",
        model: "auto",
      }],
      provider: "workbuddy",
      account: accountTag("workbuddy|a"),
    })[0].id).toBe("auto")
  })
})

function rankedBytes(ranked) {
  return ranked.filter((model) => model.id !== "not-allowed")
}

describe("account labels", () => {
  test("masks empty, email, short, and long ids", () => {
    expect(maskAccount(null)).toBe("account-1")
    expect(maskAccount({ accountId: "ab@example.com" })).toBe("ab***@example.com")
    expect(maskAccount({ accountId: "short-id" })).toBe("short-id")
    expect(maskAccount({ accountId: "abcdefghijklmnopqrstuvwxyz-123456" })).toBe("abcdefghij...123456")
    expect(KEEPALIVE_PROVIDERS.has("codearts")).toBe(true)
  })
})

describe("inventory snapshots", () => {
  test("merges providers, quotas, check-ins, histories, and built-in accounts", async () => {
    const tmp = await dir()
    const today = beijingDay()
    await mkdir(tmp.dir, { recursive: true })
    const providers = [{
      id: "workbuddy",
      name: "WorkBuddy",
      signedIn: true,
      checkin: true,
      icon: "data:image/svg+xml;base64,AA==",
      models: [
        { id: "m1", name: "M1", free: true, rate: 0, cost: { input: 0, output: 0 }, context: 100_000 },
        { id: "m2", name: "M2", rate: 1, cost: { input: 2, output: 3 }, api: { npm: "@ai-sdk/anthropic" } },
      ],
      accounts: [
        { key: "k0", accountId: "ab@example.com", type: "oauth" },
        { key: "k1#1", accountId: "abcdefghijklmnopqrstuvwxyz-123456", type: "api", listError: "bad ab@example.com" },
        { key: "k2", accountId: "quota-only", type: "oauth" },
      ],
    }, {
      id: "unknown-provider",
      name: "",
      models: [],
      accounts: [],
    }, {
      id: "q",
      name: "Q",
      checkin: true,
      accounts: [{ key: "qkey", accountId: "new-user" }],
    }]
    const auth = {
      k0: { uid: "uid-0", enterpriseId: "" },
      "k1#1": { realUserID: "real-1", refresh: "refresh-1" },
      qkey: {},
    }
    const checkins = {
      "workbuddy|k0": {
        day: today,
        at: `${today}T01:00:00.000Z`,
        outcome: "claimed",
        credit: 100,
        streak: 2,
        msg: "direct check-in",
      },
      "workbuddy|k1#1": {
        day: today,
        at: `${today}T01:00:00.000Z`,
        outcome: "failed",
        msg: "direct failed",
      },
      "q|qkey": {
        day: today,
        at: `${today}T01:00:00.000Z`,
        outcome: "claimed",
        credit: 9,
        streak: 1,
        msg: "new account",
        message: "new account",
      },
      "claude|claude-user": {
        day: today,
        at: `${today}T01:00:00.000Z`,
        outcome: "done",
        credit: 3,
        streak: 2,
        msg: "built-in check-in",
      },
    }
    const quotas = {
      "workbuddy/ab@example.com": {
        at: `${today}T01:01:00.000Z`,
        quota: { plan: "pro", balance: "10", windows: [{ used: 1, remaining: 9, name: "Credits", unit: "pt" }], error: "bad ab@example.com" },
      },
      "workbuddy/abcdefghijklmnopqrstuvwxyz-123456": {
        at: `${today}T01:01:00.000Z`,
        quota: { windows: [], error: "quota failed" },
      },
      "workbuddy/quota-only": {
        at: `${today}T01:01:00.000Z`,
        quota: { plan: "free", windows: [{ remaining: 0 }], error: "quota-only failed" },
      },
      "unknown-provider/new-user": {
        at: `${today}T01:01:00.000Z`,
        quota: { plan: "", balance: "" },
      },
    }
    const history = {
      "workbuddy|ab@example.com": {
        Credits: [
          { at: `${today}T01:00:00.000Z`, left: 20 },
          { at: `${today}T02:00:00.000Z`, left: "bad" },
          { at: `${today}T03:00:00.000Z`, left: 22 },
        ],
        Invalid: [{ at: 1, left: 2 }],
      },
      malformed: { Credits: [{ at: `${today}T01:00:00.000Z`, left: 1 }] },
      "workbuddy|abcdefghijklmnopqrstuvwxyz-123456": {},
    }
    const records = []
    const now = `${today}T04:00:00.000Z`
    records.push(
      {
        ts: `${today}T02:00:00.000Z`,
        day: today,
        provider: "workbuddy",
        event: "checkin",
        status: "failed",
        outcome: "failed",
        account: accountTag("workbuddy|ab@example.com"),
        credit: 0,
        message: "failed",
      },
      {
        ts: `${today}T03:00:00.000Z`,
        day: today,
        provider: "workbuddy",
        event: "keepalive-model",
        status: "success",
        account: accountTag("ab@example.com"),
        model: "m1",
      },
      {
        ts: now,
        day: today,
        provider: "workbuddy",
        event: "keepalive",
        status: "success",
        outcome: "sent",
        account: accountTag("ab@example.com"),
        model: "m2",
        message: "sent",
      },
      {
        ts: now,
        day: today,
        provider: "workbuddy",
        event: "finished",
        status: "success",
        outcome: "done",
        account: accountTag("uid-0|"),
      },
      {
        ts: `${today}T02:30:00.000Z`,
        day: today,
        provider: "workbuddy",
        event: "checkin",
        status: "failed",
        outcome: "failed",
        account: accountTag("quota-only"),
      },
      {
        ts: `${today}T03:30:00.000Z`,
        day: today,
        provider: "workbuddy",
        event: "checkin",
        status: "success",
        outcome: "claimed",
        account: accountTag("quota-only"),
      },
      { ts: now, day: today, provider: "workbuddy", event: "unsupported", account: accountTag("x") },
      { ts: now, day: today, provider: "workbuddy", account: accountTag("x") },
      { ts: now, day: today, event: "checkin", account: "acct-123456789012" },
      { ts: now, day: today, provider: "workbuddy", event: "error", status: "failed", account: "acct-123456789012" },
    )
    const accountsData = [
      {
        agent: "workbuddy",
        user: "ab@example.com",
        active: true,
        on: true,
        windows: [{ name: "Credits", remaining: 9, limit: 10, display: "9 / 10", resetsAt: "later" }],
      },
      {
        agent: "workbuddy",
        user: "abcdefghijklmnopqrstuvwxyz-123456",
        plan: "basic",
        balance: "1",
        readAt: now,
        windows: [{ name: "Daily", used: 1 }],
      },
      { agent: "claude", user: "claude-user", alias: "Claude Alias", windows: [{ remaining: 3 }], lapsed: "claude-user" },
      { agent: "claude", user: "claude-user-2", error: "built in error" },
      { agent: "", user: "ignored" },
      { agent: "workbuddy", user: "not-found" },
    ]
    await writeFile(tmp.path("plugin-providers.json"), JSON.stringify(providers), "utf8")
    await writeFile(tmp.path("plugin-auth.json"), JSON.stringify(auth), "utf8")
    await writeFile(tmp.path("plugin-checkin.json"), JSON.stringify(checkins), "utf8")
    await writeFile(tmp.path("quotas.json"), JSON.stringify(quotas), "utf8")
    await writeFile(tmp.path("quota-history.json"), JSON.stringify(history), "utf8")

    const inventory = await readInventory({
      configHome: tmp.dir,
      logPath: tmp.path("daily.log.jsonl"),
      records,
      accountsData,
    })
    expect(inventory.source.log).toBe("daily-keepalive.log.jsonl")
    expect(inventory.providers.map((provider) => provider.id)).toEqual([
      "workbuddy",
      "q",
      "claude",
    ])
    const workbuddy = inventory.providers[0]
    expect(workbuddy).toMatchObject({
      signedIn: true,
      checkinSupported: true,
      keepaliveSupported: true,
      keepaliveNote: "",
      accountCount: 3,
    })
    expect(workbuddy.defaultModel).toMatchObject({ id: "m2" })
    expect(workbuddy.accounts[0]).toMatchObject({
      label: "ab***@example.com",
      keyTag: "k0",
      active: true,
      on: true,
      quota: { plan: "", windows: [{ name: "Credits" }] },
      latest: { event: "keepalive", status: "success", model: "m2" },
      lastModel: "m2",
      error: "",
    })
    expect(workbuddy.accounts[0].checkin).toMatchObject({ outcome: "claimed", credit: 100 })
    expect(workbuddy.accounts[0].activity.today.checkin).toMatchObject({
      success: false,
      credit: 100,
      message: "failed",
    })
    expect(workbuddy.accounts[0].activity.today.keepalive).toMatchObject({ success: true })
    expect(workbuddy.accounts[0].activity.today.keepalive.model).toBe("m2")
    expect(workbuddy.accounts[0].history).toHaveLength(2)
    expect(workbuddy.accounts[1].quota).toMatchObject({ plan: "basic", balance: "1" })
    expect(workbuddy.accounts[1].error).toBe("bad ab@example.com")
    expect(cardKey("WorkBuddy", "User")).toBe("WorkBuddy/user")
    expect(quotaByAccount(quotas).has("workbuddy/quota-only")).toBe(true)
    expect(workbuddy.accounts[2].quota).toMatchObject({ plan: "free" })
    expect(workbuddy.accounts[2].error).toBe("quota-only failed")
    const q = inventory.providers[1]
    expect(q.keepaliveNote).toContain("暂未提供")
    expect(q.checkinSupported).toBe(false)
    expect(q.accounts[0].activity.today.checkin).toMatchObject({ success: true, credit: 9, message: "new account" })
    const builtin = inventory.providers[2]
    expect(builtin).toMatchObject({
      name: "Claude Code",
      accountCount: 2,
      checkinSupported: false,
      keepaliveSupported: false,
      defaultModel: null,
    })
    expect(builtin.accounts[0].quota).toMatchObject({ windows: [{ remaining: 3 }] })
    expect(builtin.accounts[0].checkin).toMatchObject({
      outcome: "done",
      credit: 3,
      message: "built-in check-in",
    })
    expect(builtin.accounts[1].error).toContain("built in error")
    expect(inventory.accounts).toHaveLength(6)
    expect(inventory.records).toBe(records)
  })

  test("handles missing configuration and malformed top-level files", async () => {
    const tmp = await dir()
    await writeFile(tmp.path("plugin-providers.json"), JSON.stringify({ not: "array" }), "utf8")
    await writeFile(tmp.path("plugin-auth.json"), "[]", "utf8")
    await writeFile(tmp.path("plugin-checkin.json"), "[]", "utf8")
    await writeFile(tmp.path("quotas.json"), "[]", "utf8")
    await writeFile(tmp.path("quota-history.json"), "[]", "utf8")
    const inventory = await readInventory({ configHome: tmp.dir, accountsData: "bad" })
    expect(inventory.providers).toEqual([])
    const defaults = await readInventory()
    expect(Array.isArray(defaults.providers)).toBe(true)
  })
})
