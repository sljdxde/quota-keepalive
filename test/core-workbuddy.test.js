import { afterEach, describe, expect, test } from "bun:test"
import { mkdir, readFile, utimes, writeFile } from "node:fs/promises"
import { join } from "node:path"
import { ProviderChatError, sendProviderChat } from "../src/provider-chat.js"
import {
  PROVIDER_ADAPTERS,
  accountResult,
  authIdentity,
  createProviderKeepalive,
  createWorkBuddyKeepalive,
  importMagpiePlugin,
  loadProviderFactory,
  resultFields,
  saveAuthEntry,
  sendWithCandidates,
  withAuthLock,
  writeLog,
} from "../src/workbuddy.js"
import { appendDailyLog, readDailyLogs } from "../src/log.js"
import { taskControlId } from "../src/preferences.js"
import { json, openAiStream, tempDir } from "./support.js"

const cleanups = []
afterEach(async () => {
  while (cleanups.length) {
    const cleanup = cleanups.pop()
    if (cleanup) await cleanup()
  }
})

async function dir(prefix = "quota-workbuddy-") {
  const item = await tempDir(prefix)
  cleanups.push(item.cleanup)
  return item
}

function config(overrides = {}) {
  return {
    logPath: "",
    logMaxBytes: 64 * 1024,
    statePath: "",
    maxAttempts: 3,
    inputTokens: 1_000,
    timeoutMs: 1_000,
    lockWaitMs: 100,
    modelCandidates: 4,
    modelPreferences: {},
    ...overrides,
  }
}

const MODELS = [
  { id: "free", name: "Free", free: true, rate: 0 },
  { id: "paid", name: "Paid", rate: 1 },
]

function provider(overrides = {}) {
  return {
    id: "workbuddy",
    name: "WorkBuddy",
    models: MODELS,
    accounts: [{ key: "key", accountId: "account" }],
    ...overrides,
  }
}

describe("provider package loading", () => {
  test("imports directly and falls back to Magpie's plugin directory", async () => {
    expect(await importMagpiePlugin("node:fs", "/missing")).toHaveProperty("readFile")
    const tmp = await dir()
    const packageDir = tmp.path("plugins", "node_modules", "@magpie-community", "opencode-workbuddy-auth")
    await mkdir(packageDir, { recursive: true })
    await writeFile(join(packageDir, "index.mjs"), "export const WorkBuddyAuthPlugin = () => 'ok'\n", "utf8")
    const factory = await loadProviderFactory("workbuddy", tmp.dir)
    expect(factory()).toBe("ok")
    await expect(loadProviderFactory("not-a-provider", tmp.dir)).rejects.toThrow("no registered keepalive adapter")
    await expect(importMagpiePlugin("@missing/package", tmp.dir)).rejects.toThrow("is unavailable")
  })

  test("reports packages without the expected export", async () => {
    const tmp = await dir()
    const packageDir = tmp.path("plugins", "node_modules", "@magpie-community", "opencode-workbuddy-auth")
    await mkdir(packageDir, { recursive: true })
    await writeFile(join(packageDir, "index.mjs"), "export const Other = 1\n", "utf8")
    await expect(loadProviderFactory("workbuddy", tmp.dir)).rejects.toThrow("does not export")
    expect(Object.keys(PROVIDER_ADAPTERS)).toContain("codearts")
  })
})

describe("auth file locking", () => {
  test("serializes writes and removes the lock", async () => {
    const tmp = await dir()
    const path = tmp.path("plugin-auth.json")
    await writeFile(path, "{}", "utf8")
    expect(await withAuthLock(path, async () => "result", 100)).toBe("result")
    await expect(readFile(`${path}.lock`, "utf8")).rejects.toMatchObject({ code: "ENOENT" })
  })

  test("waits on fresh locks and removes stale locks", async () => {
    const tmp = await dir()
    const path = tmp.path("plugin-auth.json")
    await writeFile(`${path}.lock`, "{}", "utf8")
    await expect(withAuthLock(path, async () => 1, 0)).rejects.toThrow("sign-in file is busy")
    const old = new Date(Date.now() - 31_000)
    await utimes(`${path}.lock`, old, old)
    expect(await withAuthLock(path, async () => 2, 100)).toBe(2)
    await writeFile(tmp.path("parent-file"), "x", "utf8")
    await expect(withAuthLock(tmp.path("parent-file", "auth.json"), async () => 1, 0)).rejects.toBeTruthy()
  })

  test("validates and atomically saves sign-ins", async () => {
    const tmp = await dir()
    const path = tmp.path("plugin-auth.json")
    await expect(saveAuthEntry(path, "", {})).rejects.toThrow("invalid provider sign-in")
    await expect(saveAuthEntry(path, "key", [])).rejects.toThrow("invalid provider sign-in")
    await writeFile(path, "not-json", "utf8")
    await expect(saveAuthEntry(path, "key", {})).rejects.toThrow()
    await writeFile(path, "[]", "utf8")
    await expect(saveAuthEntry(path, "key", {})).rejects.toThrow("isn't an object")
    await writeFile(path, JSON.stringify({ existing: { value: 1 } }), "utf8")
    await saveAuthEntry(path, "key", { value: 2 })
    expect(JSON.parse(await readFile(path, "utf8"))).toEqual({
      existing: { value: 1 },
      key: { value: 2 },
    })
  })
})

describe("result mapping and logging", () => {
  test("maps every state result and identity shape", () => {
    expect(authIdentity({ uid: "u", enterpriseId: "e" }, "k")).toBe("u|e")
    expect(authIdentity({ uid: "u", realUserID: "r" }, "k")).toBe("u|r")
    expect(authIdentity({ refresh: "r" }, "k")).toBe("k")
    const cfg = config({ maxAttempts: 3 })
    expect(resultFields({ kind: "skipped", reason: "done-today", attempts: 1 }, cfg, "m")).toMatchObject({
      status: "skipped",
      outcome: "already-done",
    })
    expect(resultFields({ kind: "skipped", reason: "exhausted", attempts: 3 }, cfg, "m")).toMatchObject({
      status: "failed",
      outcome: "exhausted",
    })
    expect(resultFields({
      kind: "skipped",
      reason: "quota-exhausted",
      attempts: 1,
      error: "Credits exhausted",
    }, cfg, "m")).toMatchObject({
      status: "failed",
      outcome: "quota-exhausted",
      retry: false,
    })
    expect(resultFields({ kind: "skipped", reason: "other", attempts: 1, error: "why" }, cfg, "m")).toMatchObject({
      status: "failed",
      error: "why",
    })
    expect(resultFields({ kind: "busy", error: "busy" }, cfg, "m")).toMatchObject({
      status: "skipped",
      outcome: "busy",
      retry: true,
    })
    expect(resultFields({ kind: "unavailable", error: "no" }, cfg, "m")).toMatchObject({
      status: "failed",
      outcome: "unavailable",
    })
    expect(resultFields({
      kind: "sent",
      ok: true,
      attempts: 1,
      persisted: false,
      result: { model: "used" },
    }, cfg, "m")).toMatchObject({
      status: "success",
      outcome: "sent",
      model: "used",
      inputTokens: 1_000,
      persisted: false,
    })
    expect(resultFields({ kind: "sent", ok: false, attempts: 1, error: "retry" }, cfg, "m")).toMatchObject({
      status: "failed",
      outcome: "retry",
      retry: true,
    })
    expect(resultFields({ kind: "sent", ok: false, attempts: 3, error: "end" }, cfg, "m")).toMatchObject({
      outcome: "exhausted",
      retry: false,
    })
    expect(resultFields({
      kind: "sent",
      ok: false,
      attempts: 1,
      error: "HTTP 429: Credits exhausted",
      cause: { retryToday: false },
    }, cfg, "m")).toMatchObject({
      outcome: "quota-exhausted",
      retry: false,
    })
    expect(resultFields({ kind: "other" }, cfg, "m")).toMatchObject({
      status: "failed",
      outcome: "unknown",
    })
    expect(accountResult("p", "a", "success", "sent", "ok")).toMatchObject({
      provider: "p",
      account: "a",
      action: "keepalive",
    })
  })

  test("writes safe structured logs", async () => {
    const tmp = await dir()
    const logPath = tmp.path("daily.log.jsonl")
    await writeLog(config({ logPath }), "workbuddy", "account", {
      event: "keepalive",
      status: "failed",
      message: "token=secret",
      error: "account failed",
      run: "run",
    })
    const records = (await readDailyLogs({ logPath })).records
    expect(records).toHaveLength(1)
    expect(records[0]).toMatchObject({ provider: "workbuddy", run: "run" })
    expect(records[0].message).toBe("token=[redacted]")
    expect(records[0].error).toContain("[redacted account]")
    expect(records[0].account).toMatch(/^acct-/)
    expect(await writeLog(config({ logPath: "relative" }), "p", "a", { event: "x", status: "failed" })).toBe(false)
  })
})

describe("multi-provider keepalive runner", () => {
  test("skips unsupported, unscoped, missing auth, and model-less providers", async () => {
    const tmp = await dir()
    const configHome = tmp.path("config")
    await mkdir(configHome, { recursive: true })
    const providers = [
      provider({ id: "unsupported" }),
      provider({ id: "workbuddy", accounts: [] }),
      provider({ id: "codearts", accounts: [{ key: "no-auth", accountId: "no-auth" }] }),
      provider({ id: "qoder-cn", models: [], accounts: [{ key: "q", accountId: "q" }] }),
    ]
    await writeFile(join(configHome, "plugin-providers.json"), JSON.stringify(providers), "utf8")
    await writeFile(join(configHome, "plugin-auth.json"), JSON.stringify({
      q: { uid: "q" },
    }), "utf8")
    const run = createProviderKeepalive({
      config: config({
        configHome,
        logPath: tmp.path("daily.log.jsonl"),
        statePath: tmp.path("state.json"),
      }),
      loadFactory: async () => { throw new Error("must not load") },
    })
    const result = await run()
    expect(result.status).toBe("failed")
    expect(result.accounts).toEqual([
      expect.objectContaining({ provider: "codearts", outcome: "not-signed-in" }),
      expect.objectContaining({ provider: "qoder-cn", outcome: "no-model" }),
    ])
    expect(result.summary).toContain("1 失败")
    const filtered = await run({ providers: ["not-present"] })
    expect(filtered.summary).toBe("没有找到支持真实对话保活的供应商账号。")
    expect(createWorkBuddyKeepalive).toBe(createProviderKeepalive)
  })

  test("manual account keepalive bypasses its switch but not a disabled provider", async () => {
    const tmp = await dir()
    const configHome = tmp.path("config")
    await mkdir(configHome, { recursive: true })
    const accounts = [
      { key: "first", accountId: "first", models: ["free"] },
      { key: "second", accountId: "second", models: ["paid"] },
    ]
    const providers = [
      provider({ accounts }),
      provider({ id: "codearts", models: MODELS, accounts: [{ key: "code", accountId: "code" }] }),
    ]
    await writeFile(join(configHome, "plugin-providers.json"), JSON.stringify(providers), "utf8")
    await writeFile(join(configHome, "plugin-auth.json"), JSON.stringify({
      first: { uid: "first" },
      second: { uid: "second" },
      code: { uid: "code" },
    }), "utf8")
    const sent = []
    const run = createProviderKeepalive({
      config: config({
        configHome,
        logPath: tmp.path("daily.log.jsonl"),
        statePath: tmp.path("state.json"),
        providerPreferences: { codearts: { enabled: false } },
        accountPreferences: {
          [taskControlId("workbuddy", accounts[1], 1)]: { keepalive: false },
        },
      }),
      loadFactory: async () => async () => ({
        auth: {
          loader: async () => ({
            baseURL: "https://x.test/v1",
            fetch: async (_url, init) => {
              sent.push(JSON.parse(init.body).model)
              return openAiStream(JSON.parse(init.body).model)
            },
          }),
        },
      }),
    })

    const manual = await run({
      accounts: [taskControlId("workbuddy", accounts[1], 1)],
    })
    expect(manual.accounts).toHaveLength(1)
    expect(manual.accounts[0]).toMatchObject({ provider: "workbuddy", status: "success", outcome: "sent" })
    expect(sent).toEqual(["paid"])

    const disabled = await run({
      providers: ["codearts"],
      accounts: [taskControlId("codearts", providers[1].accounts[0])],
    })
    expect(disabled.accounts).toEqual([])
    expect(sent).toEqual(["paid"])
  })

  test("sends real messages, records fallback, and reports no providers", async () => {
    const tmp = await dir()
    const configHome = tmp.path("config")
    await mkdir(configHome, { recursive: true })
    const providers = [provider({
      models: MODELS,
      accounts: [{ key: "key", accountId: "account", models: ["free", "paid"] }],
    })]
    await writeFile(join(configHome, "plugin-providers.json"), JSON.stringify(providers), "utf8")
    await writeFile(join(configHome, "plugin-auth.json"), JSON.stringify({
      key: { uid: "uid", refresh: "refresh" },
    }), "utf8")
    const calls = []
    const loadFactory = async () => async ({ client }) => {
      await client.auth.set({ path: { id: "workbuddy" }, body: { value: 1 } })
      return {
        auth: {
          loader: async (stored) => {
            expect(await stored()).toMatchObject({ uid: "uid" })
            return {
              baseURL: "https://x.test/v1",
              fetch: async (_url, init) => {
                const body = JSON.parse(init.body)
                calls.push(body.model)
                if (body.model === "free") return json({ error: { message: "blocked" } }, 503)
                return openAiStream(body.model)
              },
            }
          },
        },
      }
    }
    const run = createProviderKeepalive({
      config: config({
        configHome,
        logPath: tmp.path("daily.log.jsonl"),
        statePath: tmp.path("state.json"),
      }),
      loadFactory,
    })
    const result = await run({ force: true })
    expect(result.status).toBe("success")
    expect(calls).toEqual(["free", "paid"])
    expect(result.accounts[0]).toMatchObject({ provider: "workbuddy", status: "success", outcome: "sent" })
    const logs = (await readDailyLogs({ logPath: tmp.path("daily.log.jsonl") })).records
    expect(logs.some((record) => record.event === "keepalive-model" && record.outcome === "fallback")).toBe(true)
    expect(JSON.parse(await readFile(join(configHome, "plugin-auth.json"), "utf8")).key).toEqual({ value: 1 })

    const missing = await createProviderKeepalive({
      config: config({ configHome: tmp.path("nothing"), logPath: tmp.path("none.log"), statePath: tmp.path("none.json") }),
      loadFactory: async () => { throw new Error("no") },
    })()
    expect(missing.status).toBe("skipped")
    expect(missing.summary).toBe("没有找到支持真实对话保活的供应商账号。")
  })

  test("survives an unreadable model history", async () => {
    const tmp = await dir()
    const configHome = tmp.path("config")
    await mkdir(configHome, { recursive: true })
    await writeFile(join(configHome, "plugin-providers.json"), JSON.stringify([provider()]), "utf8")
    await writeFile(join(configHome, "plugin-auth.json"), JSON.stringify({ key: { uid: "account" } }), "utf8")
    const run = createProviderKeepalive({
      config: config({
        configHome,
        logPath: tmp.dir,
        statePath: tmp.path("state.json"),
      }),
      loadFactory: async () => async () => ({
        auth: {
          loader: async () => ({
            baseURL: "https://x.test/v1",
            fetch: async () => openAiStream("free"),
          }),
        },
      }),
    })
    const result = await run({ force: true })
    expect(result.status).toBe("success")
    expect(result.accounts[0]).toMatchObject({ status: "success", outcome: "sent" })
  })

  test("reports loader, auth-write, and all-model failures", async () => {
    const tmp = await dir()
    const configHome = tmp.path("config")
    await mkdir(configHome, { recursive: true })
    await writeFile(join(configHome, "plugin-providers.json"), JSON.stringify([provider()]), "utf8")
    await writeFile(join(configHome, "plugin-auth.json"), JSON.stringify({ key: { uid: "u" } }), "utf8")
    const cfg = config({
      configHome,
      logPath: tmp.path("daily.log.jsonl"),
      statePath: tmp.path("state.json"),
      maxAttempts: 1,
      modelCandidates: 1,
    })
    const noLoader = createProviderKeepalive({
      config: cfg,
      loadFactory: async () => async () => ({ auth: {} }),
    })
    expect((await noLoader({ force: true })).status).toBe("failed")

    const otherProvider = createProviderKeepalive({
      config: cfg,
      loadFactory: async () => async ({ client }) => {
        await client.auth.set({ path: { id: "other" }, body: {} })
        return { auth: { loader: async () => ({ fetch: async () => openAiStream("free") }) } }
      },
    })
    expect((await otherProvider({ force: true })).status).toBe("failed")
  })

  test("stops fallback when a provider error says not to retry", async () => {
    const tmp = await dir()
    const logPath = tmp.path("daily.log.jsonl")
    const result = await sendWithCandidates({
      input: {},
      config: config({ logPath }),
      authPath: tmp.path("auth.json"),
      provider: "workbuddy",
      providerMeta: { name: "WB" },
      key: "key",
      stored: { uid: "u" },
      identity: "u",
      logAccount: "account",
      candidates: [
        { id: "free", name: "Free" },
        { id: "paid", name: "Paid" },
      ],
      day: "2026-10-30",
      run: "run",
      loadFactory: async () => async () => ({
        auth: { loader: async () => ({ baseURL: "https://x.test/v1", fetch: async () => new Response("") }) },
      }),
      sendChat: async ({ model }) => {
        if (model === "free") throw new ProviderChatError("stop", { provider: "workbuddy", model, retryNext: false })
        return { model, reply: "ok" }
      },
    }).catch((error) => error)
    expect(result.message).toBe("stop")
    const logs = (await readDailyLogs({ logPath })).records
    expect(logs).toHaveLength(1)
  })

  test("returns the fallback model when an earlier model works", async () => {
    const tmp = await dir()
    const result = await sendWithCandidates({
      input: {},
      config: config({ logPath: tmp.path("daily.log.jsonl") }),
      authPath: tmp.path("auth.json"),
      provider: "workbuddy",
      providerMeta: { name: "WB" },
      key: "key",
      stored: { uid: "u" },
      identity: "u",
      logAccount: "account",
      candidates: [
        { id: "free", name: "Free" },
        { id: "paid", name: "Paid" },
      ],
      day: "2026-10-30",
      run: "run",
      loadFactory: async () => async () => ({
        auth: { loader: async () => ({ baseURL: "https://x.test/v1", fetch: async () => new Response("") }) },
      }),
      sendChat: async ({ model }) => {
        if (model === "free") throw new Error("retry")
        return { model, reply: "ok" }
      },
    })
    expect(result).toEqual({ reply: { model: "paid", reply: "ok" }, model: "paid" })
  })

  test("rejects an unknown candidate provider and unavailable signed chat", async () => {
    const tmp = await dir()
    await expect(sendWithCandidates({
      input: {},
      config: config({ logPath: tmp.path("log") }),
      authPath: tmp.path("auth"),
      provider: "missing",
      key: "k",
      stored: {},
      candidates: [],
      day: "2026-10-30",
      run: "run",
      loadFactory: async () => async () => ({}),
    })).rejects.toThrow("no keepalive adapter")
    await expect(sendProviderChat({
      provider: "p",
      model: "m",
      loader: {},
      inputTokens: 1,
      timeoutMs: 1,
      day: "2026-10-30",
      key: "k",
    })).rejects.toThrow("no signed request")
  })
})
