import { afterEach, describe, expect, test } from "bun:test"
import { mkdir, readFile, writeFile } from "node:fs/promises"
import { join } from "node:path"
import {
  checkinResultFields,
  createProviderCheckin,
} from "../src/checkin-runner.js"
import { readDailyLogs } from "../src/log.js"
import { taskControlId } from "../src/preferences.js"
import { tempDir } from "./support.js"

const cleanups = []
afterEach(async () => {
  while (cleanups.length) await cleanups.pop()()
})

async function temp() {
  const dir = await tempDir("quota-checkin-runner-")
  cleanups.push(dir.cleanup)
  return dir
}

function config(tmp, overrides = {}) {
  return {
    logPath: tmp.path("daily.log.jsonl"),
    logMaxBytes: 64 * 1024,
    statePath: tmp.path("state.json"),
    maxAttempts: 2,
    timeoutMs: 1_000,
    lockWaitMs: 100,
    providerPreferences: {},
    accountPreferences: {},
    ...overrides,
  }
}

function provider(overrides = {}) {
  return {
    id: "workbuddy",
    name: "WorkBuddy",
    checkin: true,
    accounts: [{ key: "key", accountId: "account" }],
    ...overrides,
  }
}

async function setup({
  providers = [provider()],
  auth = { key: { uid: "uid" } },
} = {}) {
  const tmp = await temp()
  const configHome = tmp.path("config")
  await mkdir(configHome, { recursive: true })
  await writeFile(join(configHome, "plugin-providers.json"), JSON.stringify(providers), "utf8")
  await writeFile(join(configHome, "plugin-auth.json"), JSON.stringify(auth), "utf8")
  return { tmp, configHome }
}

describe("provider check-in runner", () => {
  test("claims each enabled account once per day and keeps successful check-ins idempotent", async () => {
    const { tmp, configHome } = await setup()
    let calls = 0
    let saved = null
    const run = createProviderCheckin({
      input: {},
      config: config(tmp),
      configHome,
      loadFactory: async () => async ({ client }) => {
        await client.auth.set({ path: { id: "workbuddy" }, body: { uid: "new" } })
        saved = JSON.parse(await readFile(join(configHome, "plugin-auth.json"), "utf8"))
        return {
          auth: {
            checkin: async (getAuth) => {
              calls++
              expect(["uid", "new"]).toContain((await getAuth()).uid)
              return { outcome: "claimed", credit: 100, streak: 2, message: "claimed" }
            },
          },
        }
      },
    })
    const first = await run()
    expect(first).toMatchObject({
      status: "success",
      accounts: [{ provider: "workbuddy", status: "success", outcome: "claimed" }],
    })
    expect(first.summary).toContain("1 成功")
    expect(calls).toBe(1)
    expect(saved.key).toEqual({ uid: "new" })

    const again = await run()
    expect(again.status).toBe("skipped")
    expect(calls).toBe(1)
    const forced = await run({ force: true })
    expect(forced.status).toBe("skipped")
    expect(calls).toBe(1)
    const logs = (await readDailyLogs({ logPath: tmp.path("daily.log.jsonl") })).records
    expect(logs.filter((record) => record.event === "checkin")).toHaveLength(3)
  })

  test("honors provider and account switches before loading a provider", async () => {
    const providers = [
      provider({ id: "workbuddy" }),
      provider({ id: "codearts", accounts: [{ key: "c", accountId: "code" }] }),
      provider({ id: "qoder-cn", accounts: [{ key: "q", accountId: "qoder" }] }),
    ]
    const { tmp, configHome } = await setup({
      providers,
      auth: {
        key: { uid: "uid" },
        c: { uid: "code" },
        q: { uid: "qoder" },
      },
    })
    const cfg = config(tmp, {
      providerPreferences: { workbuddy: { enabled: false } },
      accountPreferences: {
        [taskControlId("codearts", providers[1].accounts[0])]: { checkin: false },
      },
    })
    const loaded = []
    const run = createProviderCheckin({
      input: {},
      config: cfg,
      configHome,
      loadFactory: async (id) => {
        loaded.push(id)
        return async () => ({ auth: { checkin: async () => ({ outcome: "done" }) } })
      },
    })
    const result = await run()
    expect(loaded).toEqual(["qoder-cn"])
    expect(result.status).toBe("success")
    expect(result.accounts).toHaveLength(1)
  })

  test("manual account execution bypasses its automatic switch but not a disabled provider", async () => {
    const providers = [
      provider({
        id: "workbuddy",
        accounts: [
          { key: "first", accountId: "first" },
          { key: "second", accountId: "second" },
        ],
      }),
      provider({ id: "codearts", accounts: [{ key: "code", accountId: "code" }] }),
    ]
    const { tmp, configHome } = await setup({
      providers,
      auth: {
        first: { uid: "first" },
        second: { uid: "second" },
        code: { uid: "code" },
      },
    })
    const cfg = config(tmp, {
      providerPreferences: { codearts: { enabled: false } },
      accountPreferences: {
        [taskControlId("workbuddy", providers[0].accounts[1], 1)]: { checkin: false },
      },
    })
    const loaded = []
    const run = createProviderCheckin({
      input: {},
      config: cfg,
      configHome,
      loadFactory: async (id) => {
        loaded.push(id)
        return async () => ({ auth: { checkin: async () => ({ outcome: "done" }) } })
      },
    })

    const manual = await run({
      accounts: [taskControlId("workbuddy", providers[0].accounts[1], 1)],
    })
    expect(manual.accounts).toHaveLength(1)
    expect(manual.accounts[0]).toMatchObject({ provider: "workbuddy", status: "success" })
    expect(loaded).toEqual(["workbuddy"])

    const disabled = await run({
      providers: ["codearts"],
      accounts: [taskControlId("codearts", providers[1].accounts[0])],
    })
    expect(disabled.accounts).toEqual([])
    expect(loaded).toEqual(["workbuddy"])
  })

  test("reports missing sign-ins, unsupported hooks, invalid replies, and failures", async () => {
    const providers = [
      provider({ id: "workbuddy", accounts: [
        { key: "missing", accountId: "missing" },
        { key: "empty", accountId: "empty" },
        { key: "invalid", accountId: "invalid" },
        { key: "throw", accountId: "throw" },
        { key: "failed", accountId: "failed" },
        { key: "inactive", accountId: "inactive" },
      ] }),
      provider({ id: "workbuddy-ai", checkin: false }),
      { id: "unsupported", checkin: true, accounts: [{ key: "x", accountId: "x" }] },
      { id: "workbuddy", checkin: true, accounts: [] },
    ]
    const { tmp, configHome } = await setup({
      providers,
      auth: {
        empty: {},
        invalid: {},
        throw: {},
        failed: {},
        inactive: {},
      },
    })
    const run = createProviderCheckin({
      input: {},
      config: config(tmp),
      configHome,
      loadFactory: async () => async () => ({
        auth: {
          checkin: async (getAuth) => {
            const auth = await getAuth()
            if (Object.keys(auth).length === 0) return { outcome: "failed", message: "not signed in" }
            if (auth.uid === "invalid") return undefined
            if (auth.uid === "throw") throw new Error("token=secret")
            if (auth.uid === "failed") return { outcome: "failed", message: "quota refused", credit: 7, streak: 3 }
            return { outcome: "inactive", message: "no campaign" }
          },
        },
      }),
    })

    // Give the synthetic auth entries a matching uid.
    const authPath = join(configHome, "plugin-auth.json")
    const auth = JSON.parse(await readFile(authPath, "utf8"))
    for (const name of ["empty", "invalid", "throw", "failed", "inactive"]) auth[name] = { uid: name }
    await writeFile(authPath, JSON.stringify(auth), "utf8")

    const result = await run()
    expect(result.status).toBe("failed")
    expect(result.accounts.map((item) => item.outcome)).toEqual([
      "not-signed-in",
      "inactive",
      "retry",
      "retry",
      "retry",
      "inactive",
    ])
    expect(result.summary).toContain("失败")
    const logs = (await readDailyLogs({ logPath: tmp.path("daily.log.jsonl") })).records
    expect(logs.some((record) => record.error?.includes("[redacted]"))).toBe(true)
  })

  test("handles no providers, provider filters, and non-ENOENT config errors", async () => {
    const { tmp, configHome } = await setup({ providers: [] })
    const run = createProviderCheckin({
      input: {},
      config: config(tmp),
      configHome,
      loadFactory: async () => { throw new Error("must not load") },
    })
    expect(await run()).toMatchObject({
      status: "skipped",
      summary: "没有找到支持签到的供应商账号。",
    })
    expect(await run({ providers: ["none"] })).toMatchObject({ status: "skipped" })

    const broken = await temp()
    await mkdir(broken.path("config", "plugin-providers.json"), { recursive: true })
    await expect(createProviderCheckin({
      input: {},
      config: config(broken),
      configHome: broken.path("config"),
      loadFactory: async () => async () => ({}),
    })()).rejects.toMatchObject({ code: "EISDIR" })
  })

  test("reports a provider that declares check-in but exposes no hook", async () => {
    const { tmp, configHome } = await setup({
      providers: [provider({ id: "codearts", accounts: [{ key: "c", accountId: "code" }] })],
      auth: { c: { uid: "code" } },
    })
    const run = createProviderCheckin({
      input: {},
      config: config(tmp),
      configHome,
      loadFactory: async () => async () => ({ auth: {} }),
    })
    const result = await run()
    expect(result.status).toBe("failed")
    expect(result.accounts[0]).toMatchObject({ outcome: "retry", status: "failed" })
  })

  test("maps every state result without losing provider details", () => {
    const cfg = { maxAttempts: 3 }
    expect(checkinResultFields({ kind: "skipped", reason: "done-today", attempts: 1 }, cfg)).toMatchObject({
      status: "skipped",
      outcome: "already-done",
    })
    expect(checkinResultFields({ kind: "skipped", reason: "exhausted", attempts: 3 }, cfg)).toMatchObject({
      status: "failed",
      outcome: "exhausted",
      error: expect.stringContaining("3"),
    })
    expect(checkinResultFields({
      kind: "skipped",
      reason: "quota-exhausted",
      attempts: 1,
      error: "credits exhausted",
    }, cfg)).toMatchObject({
      status: "failed",
      outcome: "quota-exhausted",
      retry: false,
      error: "credits exhausted",
    })
    expect(checkinResultFields({ kind: "busy", error: "lock" }, cfg)).toMatchObject({
      status: "skipped",
      outcome: "busy",
      retry: true,
    })
    expect(checkinResultFields({ kind: "unavailable", error: "disk" }, cfg)).toMatchObject({
      status: "failed",
      outcome: "unavailable",
    })
    expect(checkinResultFields({
      kind: "sent",
      ok: true,
      attempts: 1,
      result: { outcome: "claimed", credit: 9, streak: 2, message: "ok" },
    }, cfg, "free")).toMatchObject({
      status: "success",
      outcome: "claimed",
      credit: 9,
      streak: 2,
      retry: false,
    })
    expect(checkinResultFields({
      kind: "sent",
      ok: false,
      attempts: 1,
      cause: { checkin: { outcome: "failed", message: "nope" } },
      error: "ignored",
    }, cfg)).toMatchObject({
      status: "failed",
      outcome: "retry",
      retry: true,
      error: "nope",
    })
    expect(checkinResultFields({
      kind: "sent",
      ok: false,
      attempts: 3,
      error: "fallback",
    }, cfg, "fallback")).toMatchObject({
      status: "failed",
      outcome: "exhausted",
      retry: false,
      error: "fallback",
    })
  })
})
