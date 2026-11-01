import { afterEach, describe, expect, test } from "bun:test"
import { chmod, mkdir, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { QuotaKeepalivePlugin } from "../index.js"
import { beijingDay } from "../src/common.js"
import { checkinWorkBuddy } from "../src/checkin.js"
import { resolveKeepaliveOptions } from "../src/config.js"
import { rankedModels } from "../src/inventory.js"
import {
  accountTag,
  appendDailyLog,
  formatLogRecords,
  readDailyLogs,
} from "../src/log.js"
import { runCommand } from "../src/runner.js"
import { writeDailySnapshot } from "../src/snapshot.js"
import { createProviderKeepalive } from "../src/workbuddy.js"

const realFetch = globalThis.fetch
const cleanups = []

afterEach(async () => {
  globalThis.fetch = realFetch
  while (cleanups.length) await cleanups.pop()()
})

function json(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  })
}

function sse(body, headers = { "content-type": "text/event-stream" }) {
  return new Response(body, { status: 200, headers })
}

function openAIReply(model, text = "OK") {
  return sse(`data: ${JSON.stringify({ choices: [{ delta: { content: text } }], model })}\n\ndata: [DONE]\n\n`)
}

function anthropicReply(model, text = "OK") {
  return sse(
    `event: message_start\ndata: ${JSON.stringify({ type: "message_start", message: { model } })}\n\n` +
    `event: content_block_delta\ndata: ${JSON.stringify({ type: "content_block_delta", delta: { text } })}\n\n` +
    "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
  )
}

function alreadyCheckedIn() {
  return json({ code: 1001, msg: "今日已签到" })
}

const MODELS = [
  { id: "free-cheap", name: "Free Cheap", free: true, rate: 0, cost: { input: 0, output: 0 }, context: 200_000 },
  { id: "paid-next", name: "Paid Next", free: false, rate: 0.01, cost: { input: 0, output: 0 }, context: 200_000 },
  { id: "paid-last", name: "Paid Last", free: false, rate: 0.2, cost: { input: 0, output: 0 }, context: 200_000 },
]

function provider(id, models = MODELS, accountIds = [`${id}-account`]) {
  return {
    id,
    name: id,
    checkin: true,
    models,
    accounts: accountIds.map((accountId, index) => ({
      key: index ? `${id}#${index}` : id,
      type: "oauth",
      accountId,
      models: models.map((model) => model.id),
    })),
  }
}

function authFor(providers) {
  const out = {}
  for (const item of providers) {
    for (const account of item.accounts) {
      out[account.key] = {
        type: "oauth",
        access: `access-${item.id}-${account.key}`,
        refresh: `refresh-${item.id}-${account.key}`,
        expires: Date.now() + 3_600_000,
        uid: account.accountId,
      }
    }
  }
  return out
}

async function harness({
  providers = [provider("workbuddy")],
  unavailable = new Set(),
  handler,
  options = {},
  loadFactory,
} = {}) {
  const dir = await mkdtemp(join(tmpdir(), "quota-keepalive-"))
  cleanups.push(() => rm(dir, { recursive: true, force: true }))
  const configHome = join(dir, "config")
  const statePath = join(dir, "state.json")
  const logPath = join(dir, "daily.log.jsonl")
  const snapshotPath = join(dir, "ui", "data.js")
  const commandPath = join(dir, "command.json")
  await mkdir(configHome, { recursive: true })
  await writeFile(join(configHome, "plugin-providers.json"), JSON.stringify(providers), "utf8")
  await writeFile(join(configHome, "plugin-auth.json"), JSON.stringify(authFor(providers)), "utf8")

  const calls = []
  const factories = {}
  for (const item of providers) {
    factories[item.id] = async () => ({
      auth: {
        async loader() {
          return {
            baseURL: `https://${item.id}.example/v1`,
            apiKey: "placeholder",
            async fetch(input, init = {}) {
              const url = String(input)
              let body = init.body
              if (typeof body === "string") {
                try {
                  body = JSON.parse(body)
                } catch {}
              }
              const call = { provider: item.id, url, init, body }
              calls.push(call)
              if (handler) return handler(call, calls)
              const model = body?.model || ""
              if (unavailable.has(model)) {
                return json({ error: { message: `model ${model} unavailable` } }, 404)
              }
              const protocol = ["minimax-code", "minimax-code-global", "zcode"].includes(item.id)
                ? "anthropic"
                : "openai"
              return protocol === "anthropic"
                ? anthropicReply(model)
                : openAIReply(model)
            },
          }
        },
      },
    })
  }

  const rawOptions = {
    statePath,
    logPath,
    snapshotPath,
    commandPath,
    configHome,
    inputTokens: 1_000,
    timeoutMs: 30_000,
    lockWaitMs: 100,
    modelCandidates: 4,
    watchOptions: false,
    ...options,
  }
  const input = {
    client: { auth: { set: async () => {} } },
    magpie: { option: () => false },
  }
  const plugin = await QuotaKeepalivePlugin(input, rawOptions)
  const config = resolveKeepaliveOptions(rawOptions)
  const runKeepalive = createProviderKeepalive({
    input,
    config,
    configHome,
    loadFactory: loadFactory || (async (id) => {
      if (!factories[id]) throw new Error(`missing test factory for ${id}`)
      return factories[id]
    }),
  })
  return {
    calls,
    commandPath,
    config,
    configHome,
    dir,
    logPath,
    plugin,
    providers,
    rawOptions,
    runKeepalive,
    snapshotPath,
    statePath,
  }
}

async function records(path) {
  const raw = await readFile(path, "utf8")
  return raw.trim().split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line))
}

describe.serial("WorkBuddy CN check-in", () => {
  function checkinHarness(handler, auth = {}) {
    const calls = []
    globalThis.fetch = async (input, init = {}) => {
      const call = { url: String(input), init }
      calls.push(call)
      return handler(call, calls)
    }
    const account = {
      type: "oauth",
      access: "secret-access-token",
      refresh: "secret-refresh-token",
      expires: Date.now() + 60_000,
      ...auth,
    }
    return {
      calls,
      checkin: () => checkinWorkBuddy({ fetchImpl: globalThis.fetch, timeoutMs: 30_000 }),
      getAuth: async () => ({ ...account }),
    }
  }

  test("claims an active, unchecked day", async () => {
    const h = checkinHarness(({ url }) => {
      const path = new URL(url).pathname
      if (path.endsWith("/checkin-activity-status")) {
        return json({ code: 0, data: { active: true, today_checked_in: false, streak_days: 2 } })
      }
      if (path.endsWith("/daily-checkin")) {
        return json({ code: 0, data: { credit: 7, streak_days: 3, message: "claimed" } })
      }
      return json({ code: 0, data: {} })
    })
    const result = await h.checkin()
    expect(result).toMatchObject({ outcome: "claimed", credit: 7, streak: 3 })
  })

  test("confirms an existing check-in through the claim endpoint", async () => {
    const h = checkinHarness(({ url }) => {
      if (new URL(url).pathname.endsWith("/checkin-activity-status")) {
        return json({ code: 0, data: { active: true, today_checked_in: true, today_credit: 4, streak_days: 6 } })
      }
      return alreadyCheckedIn()
    })
    const result = await h.checkin()
    expect(result).toMatchObject({ outcome: "done", credit: 4, streak: 6 })
  })

  test("rejects empty or non-object data", async () => {
    for (const data of [null, [], "text"]) {
      const h = checkinHarness(() => json({ code: 0, data }))
      const result = await h.checkin()
      expect(result.outcome).toBe("failed")
      expect(result.message).toContain("without readable data")
    }
  })

  test("does not treat an HTML 200 credential probe as a working sign-in", async () => {
    const h = checkinHarness(({ url }) => {
      const path = new URL(url).pathname
      if (path.endsWith("/checkin-activity-status")) return new Response("Unauthorized", { status: 401 })
      if (path === "/billing/meter/get-user-resource-summary") {
        return new Response("<html>login</html>", {
          status: 200,
          headers: { "content-type": "text/html" },
        })
      }
      return new Response("Unauthorized", { status: 401 })
    })
    const result = await h.checkin()
    expect(result.outcome).toBe("failed")
    expect(result.message).toContain("sign in again")
  })
})

describe.serial("Multi-provider model availability", () => {
  test("keeps provider order unless successful availability history exists", () => {
    const result = rankedModels([
      { id: "expensive", rate: 2 },
      { id: "free", free: true },
      { id: "second", rate: 0.1 },
      { id: "not-listed", rate: 0 },
    ], ["expensive", "free", "second"], { maxCandidates: 3 })
    expect(result.map((item) => item.id)).toEqual(["expensive", "free", "second"])
    expect(rankedModels([
      { id: "expensive", rate: 2 },
      { id: "free", free: true },
      { id: "second", rate: 0.1 },
    ], ["expensive", "free", "second"], { preferred: "expensive" }).map((item) => item.id))
      .toEqual(["expensive", "free", "second"])
  })

  test("tries the first provider model and falls back when it is unavailable", async () => {
    const h = await harness({ unavailable: new Set(["free-cheap"]) })
    const result = await h.runKeepalive({ force: true })
    expect(result.status).toBe("success")
    expect(h.calls.map((call) => call.body.model)).toEqual(["free-cheap", "paid-next"])
    const log = await records(h.logPath)
    expect(log.some((record) => record.event === "keepalive-model" && record.outcome === "fallback")).toBe(true)
    expect(log.find((record) => record.event === "keepalive")).toMatchObject({
      status: "success",
      model: "paid-next",
    })
    expect(log.find((record) =>
      record.event === "keepalive-model" &&
      record.status === "success" &&
      record.outcome === "fallback"
    )).toMatchObject({ model: "paid-next" })
  })

  test("uses recent successful model history instead of price", async () => {
    const h = await harness()
    const day = beijingDay()
    await appendDailyLog({
      logPath: h.logPath,
      entry: {
        ts: `${day}T01:00:00.000Z`,
        day,
        provider: "workbuddy",
        account: accountTag("workbuddy|another-account"),
        event: "keepalive-model",
        status: "success",
        model: "paid-last",
      },
    })
    const result = await h.runKeepalive({ force: true })
    expect(result.status).toBe("success")
    expect(h.calls[0].body.model).toBe("paid-last")
  })

  test("records a first-choice model success for future selection", async () => {
    const h = await harness()
    expect((await h.runKeepalive({ force: true })).status).toBe("success")
    const selected = (await records(h.logPath)).find((record) =>
      record.event === "keepalive-model" &&
      record.status === "success" &&
      record.outcome === "selected"
    )
    expect(selected).toMatchObject({ model: "free-cheap" })
  })

  test("covers every adapted provider, not only WorkBuddy", async () => {
    const ids = [
      "workbuddy",
      "workbuddy-ai",
      "codearts",
      "minimax-code",
      "qoder-cn",
      "qwenwork",
      "trae-cn",
      "zcode",
      "opencode-zen-free",
    ]
    const h = await harness({ providers: ids.map((id) => provider(id)) })
    const result = await h.runKeepalive({ force: true })
    expect(result.status).toBe("success")
    expect(result.accounts).toHaveLength(ids.length)
    expect(new Set(result.accounts.map((item) => item.provider))).toEqual(new Set(ids))
    expect(h.calls.every((call) => call.body.model === "free-cheap")).toBe(true)
  })

  test("uses a provider-specific model preference before automatic ranking", async () => {
    const h = await harness({
      options: { modelPreferences: { workbuddy: "paid-last" } },
      unavailable: new Set(["paid-last"]),
    })
    const result = await h.runKeepalive({ force: true })
    expect(result.status).toBe("success")
    expect(h.calls.map((call) => call.body.model)).toEqual(["paid-last", "free-cheap"])
  })

  test("sends at most one successful message per account and day", async () => {
    const h = await harness()
    const first = await h.runKeepalive()
    const second = await h.runKeepalive()
    const forced = await h.runKeepalive({ force: true })
    expect(first.status).toBe("success")
    expect(second.status).toBe("skipped")
    expect(forced.status).toBe("skipped")
    expect(h.calls).toHaveLength(1)
    const keepalive = (await records(h.logPath)).filter((record) => record.event === "keepalive")
    expect(keepalive.map((record) => record.outcome)).toEqual(["sent", "already-done", "already-done"])
  })

  test("stops automatic retries after exhausted credits and allows a deliberate manual retry", async () => {
    let sends = 0
    const h = await harness({
      options: { maxAttempts: 3, modelCandidates: 1 },
      handler: () => {
        sends++
        if (sends === 1) {
          return json({
            error: { message: "Credits exhausted. Purchase add-on packs." },
          }, 429)
        }
        return openAIReply("free-cheap")
      },
    })
    const first = await h.runKeepalive()
    expect(first.status).toBe("failed")
    expect(first.accounts[0].outcome).toBe("quota-exhausted")
    expect(h.calls).toHaveLength(1)

    const automaticRetry = await h.runKeepalive()
    expect(automaticRetry.status).toBe("failed")
    expect(automaticRetry.accounts[0].outcome).toBe("quota-exhausted")
    expect(h.calls).toHaveLength(1)

    const manualRetry = await h.runKeepalive({ force: true })
    expect(manualRetry.status).toBe("success")
    expect(h.calls).toHaveLength(2)
  })

  test("fails honestly when all candidate models are unavailable", async () => {
    const h = await harness({
      options: { maxAttempts: 1 },
      unavailable: new Set(MODELS.map((model) => model.id)),
    })
    const result = await h.runKeepalive({ force: true })
    expect(result.status).toBe("failed")
    expect(h.calls).toHaveLength(3)
    expect(result.accounts[0].message).toContain("unavailable")
  })

  test("rejects a WorkBuddy activity request that ends with [DONE] and no activity evidence", async () => {
    const h = await harness({
      options: { maxAttempts: 1, modelCandidates: 1 },
      handler: () => sse("data: [DONE]\n\n"),
    })
    const result = await h.runKeepalive({ force: true })
    expect(result.status).toBe("failed")
    expect(result.accounts[0].outcome).toBe("exhausted")
  })

  test("uses private state and log files without credentials or replies", async () => {
    const h = await harness({
      handler: (call) => openAIReply(call.body.model, "MODEL_REPLY_MUST_NOT_BE_LOGGED"),
    })
    await h.runKeepalive({ force: true })
    const stateRaw = await readFile(h.statePath, "utf8")
    const logRaw = await readFile(h.logPath, "utf8")
    for (const raw of [stateRaw, logRaw]) {
      expect(raw).not.toContain("access-workbuddy")
      expect(raw).not.toContain("refresh-workbuddy")
      expect(raw).not.toContain("MODEL_REPLY_MUST_NOT_BE_LOGGED")
      expect(raw).toContain(beijingDay())
    }
    expect((await stat(h.statePath)).mode & 0o777).toBe(0o600)
    expect((await stat(h.logPath)).mode & 0o777).toBe(0o600)
    const keepalive = (await records(h.logPath)).find((record) => record.event === "keepalive")
    expect(keepalive.account).toBe(accountTag("workbuddy|workbuddy-account"))
  })

  test("reports a missing provider package instead of silently skipping", async () => {
    const h = await harness({
      options: { maxAttempts: 1, modelCandidates: 1 },
      loadFactory: async () => {
        throw new Error("provider login package unavailable")
      },
    })
    const result = await h.runKeepalive({ force: true })
    expect(result.status).toBe("failed")
    expect(result.accounts[0].message).toContain("unavailable")
  })
})

describe.serial("Logs, scheduling, and page", () => {
  test("keeps provider-specific model preferences in options", () => {
    const options = resolveKeepaliveOptions({
      modelPreferences: {
        workbuddy: "hy3",
        codearts: "deepseek-v4.1-flash",
        invalid: "",
      },
    })
    expect(options.modelPreferences).toEqual({
      workbuddy: "hy3",
      codearts: "deepseek-v4.1-flash",
    })
  })

  test("stores and filters structured run records", async () => {
    const h = await harness()
    await h.runKeepalive({ force: true })
    const result = await readDailyLogs({
      logPath: h.logPath,
      day: beijingDay(),
      status: "success",
    })
    expect(result.records.length).toBeGreaterThan(0)
    expect(formatLogRecords(result.records)).toContain("provider=workbuddy")
    const output = await h.plugin.tool.logs.execute({ limit: 5, status: "success" })
    expect(output).toContain("Quota Keepalive logs")
  })

  test("rotates one previous log without losing the newest record", async () => {
    const dir = await mkdtemp(join(tmpdir(), "quota-log-"))
    cleanups.push(() => rm(dir, { recursive: true, force: true }))
    const logPath = join(dir, "daily.log.jsonl")
    for (let i = 0; i < 6; i++) {
      await appendDailyLog({
        logPath,
        logMaxBytes: 500,
        entry: {
          ts: new Date(1_800_000_000_000 + i).toISOString(),
          event: "checkin",
          status: "success",
          day: "2026-10-30",
          run: `run-${i}`,
          message: "x".repeat(220),
        },
      })
    }
    expect((await stat(`${logPath}.1`)).mode & 0o777).toBe(0o600)
    expect(await readFile(logPath, "utf8")).toContain("run-5")
  })

  test("runner records all provider check-in rows from Magpie", async () => {
    const h = await harness()
    const result = await runCommand({
      action: "checkin",
      config: h.config,
      runMagpieJson: async () => ({
        ok: true,
        exitCode: 0,
        stdout: JSON.stringify([
          { agent: "workbuddy", user: "a", outcome: "claimed", credit: 100 },
          { agent: "codearts", user: "b", outcome: "claimed", credit: 100 },
          { agent: "minimax-code", user: "c", outcome: "done" },
          { agent: "qoder-cn", user: "d", outcome: "claimed" },
          { agent: "trae-cn", user: "e", outcome: "ineligible", msg: "9074" },
        ]),
        stderr: "",
        error: "",
      }),
    })
    expect(result.status).toBe("success")
    expect(new Set(result.accounts.map((item) => item.provider))).toEqual(new Set([
      "workbuddy",
      "codearts",
      "minimax-code",
      "qoder-cn",
      "trae-cn",
    ]))
  })

  test("ships the native provider icons and the Q plugin icon", async () => {
    const root = new URL("..", import.meta.url)
    const pkg = JSON.parse(await readFile(new URL("package.json", root), "utf8"))
    expect(pkg.magpie.page.file).toBe("ui/index.html")
    expect(pkg.magpie.page.icon).toBe("ui/icon.svg")
    expect(pkg.magpie.icon).toStartWith("data:image/svg+xml;base64,")
    for (const name of [
      "workbuddy.svg",
      "codearts.svg",
      "minimax.svg",
      "qoder.svg",
      "trae.svg",
      "zcode.png",
      "opencode.svg",
    ]) {
      expect((await stat(new URL(`ui/provider-icons/${name}`, root))).isFile()).toBe(true)
    }
    const app = await readFile(new URL("ui/app.js", root), "utf8")
    expect(app).toContain("PROVIDER_ICON_FILES")
    expect(app).toContain("qwenwork-enterprise")
    expect(app).not.toContain("LOCAL_PROVIDER_ICONS")
    for (const name of ["qoder.svg", "opencode.svg", "trae.svg"]) {
      const icon = await readFile(new URL(`ui/provider-icons/${name}`, root), "utf8")
      expect(icon).not.toContain("currentColor")
      expect(icon).toMatch(/fill="#[0-9A-F]{6}"/)
    }
  })

  test("keeps detected quota when a later log refresh rewrites the snapshot", async () => {
    const dir = await mkdtemp(join(tmpdir(), "quota-snapshot-"))
    cleanups.push(() => rm(dir, { recursive: true, force: true }))
    const configHome = join(dir, "config")
    const logPath = join(dir, "daily.log.jsonl")
    const snapshotPath = join(dir, "ui", "data.js")
    await mkdir(configHome, { recursive: true })
    await writeFile(logPath, "", "utf8")
    await writeFile(
      join(configHome, "plugin-providers.json"),
      JSON.stringify([provider("workbuddy", MODELS, ["Test Account"])]),
      "utf8",
    )
    const accountsData = [{
      agent: "workbuddy",
      user: "Test Account",
      windows: [{ name: "Credits", used: 28.85, remaining: 71.15, display: "1012.84 / 3511" }],
    }]
    await writeDailySnapshot({ configHome, logPath, snapshotPath, accountsData })
    await writeDailySnapshot({ configHome, logPath, snapshotPath })
    const text = await readFile(snapshotPath, "utf8")
    const snapshot = JSON.parse(text.replace(/^window\.DailyKeepaliveSnapshot = /, "").replace(/;\s*$/, ""))
    expect(snapshot.providers[0].accounts[0].quota.windows[0].display).toBe("1012.84 / 3511")
    expect(snapshot.providers[0].models).toHaveLength(3)
    expect(snapshot.providers[0].models.find((model) => model.id === "paid-next")).toMatchObject({
      rate: 0.01,
      free: false,
    })
  })

  test("precomputes today's result, five-day history, and grouped runs", async () => {
    const h = await harness()
    const today = beijingDay()
    const tag = accountTag("workbuddy|workbuddy-account")
    const run = "run-test-checkin"
    await appendDailyLog({
      logPath: h.logPath,
      entry: {
        ts: `${today}T01:00:00.000Z`,
        day: today,
        provider: "workbuddy",
        run,
        event: "checkin",
        status: "success",
        outcome: "claimed",
        account: tag,
        credit: 100,
        streak: 3,
        message: "+100 积分",
      },
    })
    await appendDailyLog({
      logPath: h.logPath,
      entry: {
        ts: `${today}T01:00:30.000Z`,
        day: today,
        provider: "workbuddy",
        run: "run-test-keepalive",
        event: "keepalive",
        status: "success",
        outcome: "sent",
        account: tag,
        message: "sent",
      },
    })
    await appendDailyLog({
      logPath: h.logPath,
      entry: {
        ts: `${today}T01:00:31.000Z`,
        day: today,
        provider: "daily-keepalive",
        run,
        event: "checkin",
        status: "success",
        outcome: "success",
        message: "签到完成：1 成功，0 跳过，0 失败。",
      },
    })
    await writeDailySnapshot({
      configHome: h.configHome,
      logPath: h.logPath,
      snapshotPath: h.snapshotPath,
    })
    const text = await readFile(h.snapshotPath, "utf8")
    const snapshot = JSON.parse(text.replace(/^window\.DailyKeepaliveSnapshot = /, "").replace(/;\s*$/, ""))
    const account = snapshot.providers[0].accounts[0]
    expect(account.activity.today.checkin).toMatchObject({
      success: true,
      credit: 100,
      streak: 3,
    })
    expect(account.activity.today.keepalive).toMatchObject({ success: true, outcome: "sent" })
    expect(account.activity.days).toHaveLength(5)
    expect(snapshot.summary.today.checkinDone).toBe(1)
    expect(snapshot.summary.today.keepaliveDone).toBe(1)
    expect(snapshot.runHistory[0]).toMatchObject({
      id: run,
      event: "checkin",
      accounts: 1,
      successes: 1,
      failures: 0,
    })
    expect(snapshot.runHistory[0].children).toHaveLength(1)
  })

  test("the page exposes detection, check-in, scheduling, and logs while keepalive is paused", async () => {
    const root = new URL("..", import.meta.url)
    const html = await readFile(new URL("ui/index.html", root), "utf8")
    for (const id of ["detect", "checkin", "schedule-save", "logs"]) {
      expect(html).toContain(`id="${id}"`)
    }
    expect(html).not.toContain('id="keepalive"')
    expect(html).not.toContain('id="probe-mode"')
    expect(html).not.toContain("<span>发送保活</span>")
    expect(html).toContain("全部已适配供应商")
    const app = await readFile(new URL("ui/app.js", root), "utf8")
    expect(app).toContain("model-search")
    expect(app).toContain("modelPreferences")
    expect(app).toContain("fuzzyScore")
    expect(app).toContain("请先启用此供应商")
    expect(app).toContain("data-provider-refresh")
    expect(app).toContain("pendingProvider")
    expect(app).toContain("QuotaKeepaliveFormat")
    expect(app).toContain("quotaItems")
    expect(app).toContain("accountQuota")
    expect(app).toContain("剩余")
    expect(app).toContain("已用")
    expect(app).toContain("供应商原样显示")
    expect(app).toContain('item.kind === "percent"')
    expect(app).toContain("实际模型")
    expect(app).toContain("最近实际使用")
    expect(app).toContain("lastModel")
    expect(app).toContain("accountNotes")
    expect(app).toContain("provider-states")
    expect(app).toContain("QuotaKeepaliveStatus")
    expect(app).toContain("task-switch")
    expect(app).toContain("data-task-run")
    expect(app).toContain("pendingAccount")
    expect(app).toContain("account-task-row")
    expect(app).toContain("escapeHtml(state.short)")
    expect(app).toContain('task-switch-copy">自动')
    expect(app).not.toContain("account-badges")
    expect(app).not.toContain("is-keepalive")
    expect(app).not.toContain("task-toggle")
    expect(html).toContain('id="confirm-detail"')
    expect(html).toContain('id="refresh"')
    expect(html).toContain("<span>刷新额度</span>")
    expect(html).toContain('title="刷新全部启用供应商的实时额度"')
    expect(html).toContain('aria-label="刷新全部启用供应商的实时额度"')
    expect(html).not.toContain('<option value="keepalive">')
    expect(html).not.toContain('<option value="all">')
    expect(app).toContain("const KEEPALIVE_VISIBLE = false")
    expect(html).toContain("<h1>额度签到</h1>")
    expect(html).toContain('src="options-writer.js"')
    expect(app).toContain("writePageOptions")
    expect(app).toContain("stagePageOptions")
    expect(html).toContain('src="quota-format.js"')
    expect(html).toContain('src="status.js"')
    expect(html).not.toContain('src="data.js"')
    expect(app).toContain("await reloadSnapshot()")
  })
})
