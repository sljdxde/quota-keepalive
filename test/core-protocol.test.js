import { describe, expect, test } from "bun:test"
import { homedir } from "node:os"
import { join } from "node:path"
import {
  accountKey,
  appendMessage,
  beijingDay,
  defaultCommandPath,
  defaultStatePath,
  errorText,
  finiteNumber,
  magpieConfigHome,
  sanitize,
  terminalQuotaFailure,
  text,
  truthy,
  withTimeout,
} from "../src/common.js"
import { resolveKeepaliveOptions } from "../src/config.js"
import {
  WorkBuddyReplyError,
  checkinWorkBuddy,
} from "../src/checkin.js"
import {
  assistantReply,
  buildKeepaliveBody,
  responseMessage,
  sendKeepalive,
  workbuddyProbeResult,
} from "../src/keepalive.js"
import {
  anthropicBody,
  endpointFor,
  errorMessage,
  messageFromBody,
  messageFromStream,
  ProviderChatError,
  sendProviderChat,
} from "../src/provider-chat.js"
import { json, openAiStream, sse } from "./support.js"

describe("common helpers", () => {
  test("normalizes values and Beijing days", () => {
    expect(beijingDay(new Date("2026-10-30T15:59:59.000Z"))).toBe("2026-10-30")
    expect(beijingDay(new Date("2026-10-30T16:00:00.000Z"))).toBe("2026-10-31")
    expect(finiteNumber("2.5")).toBe(2.5)
    expect(finiteNumber("nope", 7)).toBe(7)
    expect(text("x")).toBe("x")
    expect(text(1)).toBe("")
    expect(truthy(true)).toBe(true)
    expect(truthy(1)).toBe(true)
    expect(truthy("1")).toBe(true)
    expect(truthy("true")).toBe(true)
    expect(truthy("yes")).toBe(false)
    expect(accountKey({ uid: " u ", enterpriseId: "e" })).toBe("u|e")
    expect(accountKey({})).toBe("")
    expect(appendMessage("", " b ")).toBe("b")
    expect(appendMessage(" a ", "")).toBe("a")
    expect(appendMessage("a", "b")).toBe("a b")
  })

  test("builds config paths from XDG or the home directory", () => {
    const xdg = process.env.XDG_CONFIG_HOME
    try {
      process.env.XDG_CONFIG_HOME = "/tmp/quota-config"
      expect(magpieConfigHome()).toBe("/tmp/quota-config/magpie")
      expect(defaultStatePath()).toBe("/tmp/quota-config/magpie/workbuddy-daily-keepalive.json")
      expect(defaultCommandPath()).toBe("/tmp/quota-config/magpie/daily-keepalive-command.json")
      process.env.XDG_CONFIG_HOME = "relative"
      expect(magpieConfigHome()).toBe(join(homedir(), ".config", "magpie"))
      delete process.env.XDG_CONFIG_HOME
      expect(magpieConfigHome()).toBe(join(homedir(), ".config", "magpie"))
    } finally {
      if (xdg === undefined) delete process.env.XDG_CONFIG_HOME
      else process.env.XDG_CONFIG_HOME = xdg
    }
  })

  test("redacts credentials and truncates errors", async () => {
    const jwt = "eyJabcdef.abcdef.abcdef"
    expect(sanitize(`Bearer abc ${jwt} token=secret api_key: key`)).toBe(
      "Bearer [redacted] [redacted token] token=[redacted] api_key: [redacted]",
    )
    expect(sanitize("abcdef", 3)).toBe("abc")
    expect(errorText(new Error("token=secret"))).toBe("token=[redacted]")
    expect(errorText("plain")).toBe("plain")
    expect(terminalQuotaFailure(429, "Credits exhausted; purchase add-on packs")).toBe(true)
    expect(terminalQuotaFailure(429, "Your requests have exceeded the quota")).toBe(true)
    expect(terminalQuotaFailure(429, "rate limit, retry later")).toBe(false)
    expect(terminalQuotaFailure(500, "Credits exhausted")).toBe(false)
    expect(await withTimeout(async () => "ok", 0, "unused")).toBe("ok")
    await expect(withTimeout(() => new Promise(() => {}), 10, "deadline")).rejects.toThrow("deadline")
  })
})

describe("option resolution", () => {
  test("clamps invalid values and keeps valid settings", () => {
    const options = resolveKeepaliveOptions({
      model: "  model-id  ",
      modelPreferences: {
        " workbuddy ": " hy3 ",
        bad: 3,
        long: "x".repeat(300),
      },
      modelCandidates: "2",
      scheduleEnabled: true,
      scheduleAction: "all",
      scheduleMinutes: "60",
      configHome: "~/config",
      inputTokens: 25_000,
      probeMode: "context",
      probeMessage: "  custom probe  ",
      maxAttempts: 2,
      timeoutMs: 30_000,
      lockWaitMs: 0,
      watchOptions: false,
      statePath: "~/state.json",
      commandPath: "/tmp/command.json",
      logPath: "relative.log",
      logMaxBytes: 64 * 1024,
      snapshotPath: "/tmp/page.js",
      schedulePath: "/tmp/schedule.json",
    })
    expect(options).toMatchObject({
      model: "model-id",
      modelCandidates: 2,
      scheduleEnabled: true,
      scheduleAction: "all",
      scheduleMinutes: 60,
      configHome: join(homedir(), "config"),
      inputTokens: 25_000,
      probeMode: "context",
      probeMessage: "custom probe",
      maxAttempts: 2,
      timeoutMs: 30_000,
      lockWaitMs: 0,
      watchOptions: false,
      statePath: join(homedir(), "state.json"),
      commandPath: "/tmp/command.json",
      snapshotPath: "/tmp/page.js",
      schedulePath: "/tmp/schedule.json",
    })
    expect(options.modelPreferences.workbuddy).toBe("hy3")
    expect(options.modelPreferences.long).toBe("x".repeat(200))
    expect(options.modelPreferences.bad).toBeUndefined()
  })

  test("falls back for malformed options", () => {
    const options = resolveKeepaliveOptions({
      model: 1,
      modelPreferences: [],
      scheduleAction: "unknown",
      scheduleEnabled: "true",
      modelCandidates: 0,
      scheduleMinutes: 1,
      configHome: "",
      inputTokens: 10,
      maxAttempts: 99,
      timeoutMs: 1,
      lockWaitMs: 99_999,
      statePath: 1,
      commandPath: "relative",
      logPath: 1,
      logMaxBytes: 1,
      snapshotPath: null,
    })
    expect(options).toMatchObject({
      model: "",
      modelPreferences: {},
      scheduleEnabled: false,
      scheduleAction: "checkin",
      modelCandidates: 4,
      scheduleMinutes: 60,
      configHome: undefined,
      inputTokens: 25_000,
      probeMode: "activity",
      probeMessage: "Magpie daily connectivity check. Reply with OK.",
      maxAttempts: 3,
      timeoutMs: 90_000,
      lockWaitMs: 2_000,
      watchOptions: true,
    })
    expect(options.logPath.endsWith("workbuddy-daily.log.jsonl")).toBe(true)
    expect(options.snapshotPath.endsWith("ui/data.js")).toBe(true)
    expect(resolveKeepaliveOptions(null).watchOptions).toBe(true)
    expect(resolveKeepaliveOptions([]).scheduleAction).toBe("checkin")
  })
})

describe("WorkBuddy check-in", () => {
  test("rejects a missing signed fetch", async () => {
    await expect(checkinWorkBuddy({})).rejects.toThrow("signed fetch is unavailable")
  })

  test("reads every inactive and known refusal outcome", async () => {
    const inactive = await checkinWorkBuddy({
      fetchImpl: async () => json({ code: 0, data: { active: false } }),
    })
    expect(inactive).toMatchObject({ outcome: "inactive", credit: 0, streak: 0 })

    for (const [code, outcome] of [
      [1001, "done"],
      [10001, "done"],
      [1002, "ineligible"],
      [1003, "inactive"],
    ]) {
      const result = await checkinWorkBuddy({
        fetchImpl: async () => json({ code, msg: `code-${code}` }),
      })
      expect(result).toMatchObject({ outcome, credit: 0, streak: 0 })
    }

    const unknown = await checkinWorkBuddy({
      fetchImpl: async () => json({ code: 99, msg: "vendor refused" }),
    })
    expect(unknown.outcome).toBe("failed")
    expect(unknown.message).toContain("vendor refused")
  })

  test("handles non-JSON, HTTP errors, malformed bodies, and vendor messages", async () => {
    const cases = [
      async () => new Response("not-json", { status: 500, statusText: "boom" }),
      async () => json({ error: { message: "nested error" } }, 403),
      async () => new Response("<html>bad</html>", { status: 200 }),
      async () => json({ code: "not-number", data: {} }),
      async () => json({ code: 0, data: null }),
      async () => json({ code: 0, data: [] }),
      async () => json({ code: 0, data: "x" }),
    ]
    for (const fetchImpl of cases) {
      const result = await checkinWorkBuddy({ fetchImpl })
      expect(result.outcome).toBe("failed")
    }
  })

  test("distinguishes an expired sign-in from a desktop-only check-in", async () => {
    const fetch401 = async (input) => {
      if (String(input).endsWith("/billing/meter/get-user-resource-summary")) {
        return json({ code: 0, data: {} })
      }
      return new Response("Unauthorized", { status: 401 })
    }
    expect(await checkinWorkBuddy({ fetchImpl: fetch401 })).toMatchObject({
      outcome: "ineligible",
      message: expect.stringContaining("own app"),
    })

    const expired = await checkinWorkBuddy({
      fetchImpl: async (input) => {
        if (String(input).endsWith("/billing/meter/get-user-resource-summary")) {
          return json({ code: 7, data: {} })
        }
        return new Response("Unauthorized", { status: 401 })
      },
    })
    expect(expired).toMatchObject({
      outcome: "failed",
      message: expect.stringContaining("sign in again"),
    })

    const probeFails = await checkinWorkBuddy({
      fetchImpl: async (input) => {
        if (String(input).endsWith("/billing/meter/get-user-resource-summary")) {
          throw new Error("network down")
        }
        return new Response("Unauthorized", { status: 401 })
      },
    })
    expect(probeFails.outcome).toBe("failed")
  })

  test("confirms a claim response and carries status credit and streak", async () => {
    let call = 0
    const result = await checkinWorkBuddy({
      fetchImpl: async () => {
        call++
        if (call === 1) {
          return json({
            code: 0,
            data: { active: true, today_checked_in: true, today_credit: 12, streak_days: 4 },
          })
        }
        return json({ code: 0, data: { is_streak_day: true, credit: 0, message: "ok" } })
      },
    })
    expect(result).toMatchObject({ outcome: "done", credit: 12, streak: 4, message: "ok" })

    let secondCall = 0
    const noConfirmation = await checkinWorkBuddy({
      fetchImpl: async () => {
        secondCall++
        if (secondCall === 1) {
          return json({ code: 0, data: { active: true, today_checked_in: true, today_credit: 8 } })
        }
        return json({ code: 1001, msg: "already" })
      },
    })
    expect(noConfirmation).toMatchObject({ outcome: "done", credit: 8 })
  })

  test("exposes typed reply errors", () => {
    const error = new WorkBuddyReplyError("x", { status: 401, code: 1, body: "raw" })
    expect(error).toMatchObject({ name: "WorkBuddyReplyError", status: 401, code: 1, body: "raw" })
  })
})

describe("WorkBuddy keepalive protocol", () => {
  test("builds a bounded request body and parses message errors", () => {
    const body = buildKeepaliveBody({ model: "m", inputTokens: 100, day: "2026-10-30" })
    expect(body).toMatchObject({ model: "m", max_tokens: 1, stream: true })
    expect(body.messages[1].content).not.toContain("quick brown fox")
    expect(body.messages[1].content).toEndWith("Today is 2026-10-30.")
    const custom = buildKeepaliveBody({
      model: "m",
      inputTokens: 100,
      day: "2026-10-30",
      probeMessage: "custom check",
    })
    expect(custom.messages[1].content).toBe("custom check Today is 2026-10-30.")
    const context = buildKeepaliveBody({
      model: "m",
      inputTokens: 100,
      day: "2026-10-30",
      probeMode: "context",
    })
    expect(context.messages[1].content).toContain("quick brown fox")
    const activity = buildKeepaliveBody({
      model: "m",
      inputTokens: 1_000,
      day: "2026-10-30",
      probeMode: "activity",
    })
    expect(activity.messages[0].content).toBe("You are a connectivity check. Reply with a single word.")
    expect(activity.messages[1].content).toContain("quick brown fox")
    expect(activity.messages[1].content).not.toContain("Today is")
    expect(activity.messages[1].content.length).toBeGreaterThan(4_000)
    expect(responseMessage("not-json")).toBe("")
    expect(responseMessage("null")).toBe("")
    expect(responseMessage(JSON.stringify({ error: { message: "bad token=secret" } }))).toBe("bad token=[redacted]")
    expect(responseMessage(JSON.stringify({ msg: "oops" }))).toBe("oops")
    expect(responseMessage('data: {"code":14018,"msg":"Credits exhausted"}\n\n')).toBe("Credits exhausted")
  })

  test("only treats real streamed content as a reply", () => {
    expect(assistantReply("data: [DONE]\n\n")).toEqual({ ok: false, content: "" })
    expect(assistantReply("data: nope\n\n")).toEqual({ ok: false, content: "" })
    expect(assistantReply('data: {"choices":[null,{"delta":{"content":"A"}},{"message":{"content":"B"}},{"text":"C"}]}\n')).toEqual({
      ok: true,
      content: "ABC",
    })
    expect(assistantReply('data: {"choices":[{"delta":{"content":""}}]}\n')).toEqual({
      ok: false,
      content: "",
    })
    expect(assistantReply(
      'data: {"choices":[{"delta":{"content":[{"text":"A"},{"text":"B"}]}}]}\n',
    )).toEqual({
      ok: true,
      content: "AB",
    })
  })

  test("sends and validates keepalive replies", async () => {
    await expect(sendKeepalive({})).rejects.toThrow("signed fetch is unavailable")
    const success = await sendKeepalive({
      fetchImpl: async (_url, init) => {
        expect(init.headers["x-magpie-workbuddy-session"]).toContain("workbuddy-keepalive:key:2026-10-30")
        return openAiStream("model-a", "YES")
      },
      model: "model-a",
      inputTokens: 100,
      timeoutMs: 1_000,
      day: "2026-10-30",
      key: "key",
    })
    expect(success).toEqual({ model: "model-a", reply: "YES", activityVerified: true })
    const usageOnly = await sendKeepalive({
      fetchImpl: async () => sse('data: {"usage":{"prompt_tokens":25023,"credit":0.72}}\n\ndata: [DONE]\n\n'),
      model: "model-a",
      inputTokens: 25_000,
      timeoutMs: 1_000,
      day: "2026-10-30",
      key: "key",
    })
    expect(usageOnly).toMatchObject({
      model: "model-a",
      reply: "",
      activityVerified: true,
      credit: 0.72,
      promptTokens: 25023,
    })

    await expect(sendKeepalive({
      fetchImpl: async () => json({ error: { message: "quota" } }, 429),
      model: "m",
      inputTokens: 10,
      timeoutMs: 1_000,
      day: "2026-10-30",
      key: "k",
    })).rejects.toThrow("HTTP 429: quota")
    try {
      await sendKeepalive({
        fetchImpl: async () => json({
          error: { message: "Credits exhausted. Purchase add-on packs." },
        }, 429),
        model: "m",
        inputTokens: 10,
        timeoutMs: 1_000,
        day: "2026-10-30",
        key: "k",
      })
    } catch (error) {
      expect(error).toMatchObject({
        status: 429,
        retryNext: false,
        retryToday: false,
      })
    }
    await expect(sendKeepalive({
      fetchImpl: async () => new Response("", { status: 502 }),
      model: "m",
      inputTokens: 10,
      timeoutMs: 1_000,
      day: "2026-10-30",
      key: "k",
    })).rejects.toThrow("HTTP 502")
    await expect(sendKeepalive({
      fetchImpl: async () => sse("data: [DONE]\n\n"),
      model: "m",
      inputTokens: 10,
      timeoutMs: 1_000,
      day: "2026-10-30",
      key: "k",
    })).rejects.toThrow("without a model reply or usage evidence")
    await expect(sendKeepalive({
      fetchImpl: async () => new Response("", { status: 200 }),
      model: "m",
      inputTokens: 10,
      timeoutMs: 1_000,
      day: "2026-10-30",
      key: "k",
    })).rejects.toThrow("without a model reply or usage evidence")
    await expect(sendKeepalive({
      fetchImpl: async () => json({ code: 14018, msg: "Credits exhausted" }),
      model: "m",
      inputTokens: 10,
      timeoutMs: 1_000,
      day: "2026-10-30",
      key: "k",
    })).rejects.toThrow("rejected the request: Credits exhausted")
    await expect(sendKeepalive({
      fetchImpl: async () => new Response("<html>sign in</html>", {
        status: 200,
        headers: { "content-type": "text/html" },
      }),
      model: "m",
      inputTokens: 10,
      timeoutMs: 1_000,
      day: "2026-10-30",
      key: "k",
    })).rejects.toThrow("returned an HTML page")
    await expect(sendKeepalive({
      fetchImpl: () => new Promise(() => {}),
      model: "m",
      inputTokens: 10,
      timeoutMs: 10,
      day: "2026-10-30",
      key: "k",
    })).rejects.toThrow("timed out after 10ms")
  })

  test("accepts WorkBuddy 2xx probes with usage-only streams but rejects business errors", () => {
    expect(workbuddyProbeResult(
      new Response(""),
      'data: {"usage":{"credit":0.72}}\n\ndata: [DONE]\n\n',
    )).toEqual({
      ok: true,
      content: "",
      activityVerified: false,
      credit: 0.72,
    })
    expect(workbuddyProbeResult(
      new Response(""),
      'data: {"error":{"message":"channel rejected"}}\n\n',
    )).toEqual({
      ok: false,
      content: "",
      error: "WorkBuddy's keepalive chat returned 200 but rejected the request: channel rejected",
    })
    expect(workbuddyProbeResult(
      new Response(""),
      'data: {"success":false,"message":"request paused"}\n\n',
    )).toMatchObject({
      ok: false,
      error: "WorkBuddy's keepalive chat returned 200 but rejected the request: request paused",
    })
    expect(workbuddyProbeResult(
      new Response(""),
      'data: {"error":"plain refusal"}\n\n',
    )).toMatchObject({
      ok: false,
      error: "WorkBuddy's keepalive chat returned 200 but rejected the request: plain refusal",
    })
    expect(workbuddyProbeResult(new Response(""), undefined)).toEqual({
      ok: true,
      content: "",
      activityVerified: false,
    })
    expect(workbuddyProbeResult(
      new Response(""),
      'data: {"usage":{"prompt_tokens":25023,"credit":0.72}}\n\ndata: [DONE]\n\n',
      { probeMode: "activity" },
    )).toMatchObject({
      ok: true,
      activityVerified: true,
      credit: 0.72,
      promptTokens: 25023,
    })
    expect(workbuddyProbeResult(
      new Response(""),
      'data: {"usage":{"credit":"0.72","prompt_tokens":"25023","completion_tokens":"1","total_tokens":"25024"}}\n\ndata: [DONE]\n\n',
      { probeMode: "activity" },
    )).toMatchObject({
      ok: true,
      activityVerified: true,
      credit: 0.72,
      promptTokens: 25023,
      completionTokens: 1,
      totalTokens: 25024,
    })
    expect(workbuddyProbeResult(
      new Response(""),
      JSON.stringify({ choices: [{ message: { content: "OK" } }] }),
      { probeMode: "activity" },
    )).toMatchObject({
      ok: true,
      content: "OK",
      activityVerified: true,
    })
    expect(workbuddyProbeResult(
      new Response(""),
      JSON.stringify({ content: [{ text: "ACTIVE" }] }),
      { probeMode: "activity" },
    )).toMatchObject({
      ok: true,
      content: "ACTIVE",
      activityVerified: true,
    })
    expect(workbuddyProbeResult(
      new Response(""),
      "data: [DONE]\n\n",
      { probeMode: "activity" },
    )).toMatchObject({
      ok: false,
      activityVerified: false,
    })
    expect(workbuddyProbeResult(
      new Response(""),
      'data: {"usage":{"prompt_tokens":null,"credit":""}}\n\ndata: [DONE]\n\n',
      { probeMode: "activity" },
    )).toMatchObject({
      ok: false,
      activityVerified: false,
    })
  })
})

describe("provider chat protocol", () => {
  test("normalizes endpoints and request bodies", () => {
    expect(endpointFor(" https://x.test/v1/// ", "openai")).toBe("https://x.test/v1/chat/completions")
    expect(endpointFor("https://x.test/v1", "anthropic")).toBe("https://x.test/v1/messages")
    expect(() => endpointFor("", "openai")).toThrow("no request address")
    const body = anthropicBody({ model: "m", inputTokens: 20, day: "2026-10-30" })
    expect(body).toMatchObject({ model: "m", max_tokens: 8, stream: true })
    expect(body.system).toContain("connectivity")
    const context = anthropicBody({
      model: "m",
      inputTokens: 20,
      day: "2026-10-30",
      probeMode: "context",
      probeMessage: "custom",
    })
    expect(context.messages[0].content).toContain("quick brown fox")
    expect(context.messages[0].content).toContain("custom Today is 2026-10-30")
  })

  test("extracts replies from common JSON and stream formats", () => {
    expect(messageFromBody(null)).toBe("")
    expect(messageFromBody({ choices: [{ message: { content: "A" } }] })).toBe("A")
    expect(messageFromBody({ choices: [{ delta: { content: [{ text: "B" }] } }] })).toBe("B")
    expect(messageFromBody({ content: [{ text: "C" }] })).toBe("C")
    expect(messageFromBody({ output_text: "D" })).toBe("D")
    expect(messageFromBody({ choices: [{ text: " " }] })).toBe("")
    expect(messageFromStream("data: nope\n", "openai")).toEqual({ ok: false, content: "" })
    expect(messageFromStream('data: {"choices":[{"delta":{"content":"A"}}]}\n', "openai")).toEqual({
      ok: true,
      content: "A",
    })
    expect(messageFromStream('data: {"choices":[{"delta":{"content":[{"text":"B"}]}}]}\n', "openai")).toEqual({
      ok: true,
      content: "B",
    })
    expect(messageFromStream(
      'data: {"delta":{"text":"A"},"completion":"B","content_block":{"content":[{"text":"C"}]}}\n',
      "anthropic",
    )).toEqual({ ok: true, content: "ABC" })
  })

  test("formats provider errors", () => {
    expect(errorMessage("plain", 500)).toBe("plain")
    expect(errorMessage("", 500)).toBe("HTTP 500")
    for (const body of [
      { error: { message: "a" } },
      { error: { code: "b" } },
      { message: "c" },
      { msg: "d" },
      { ResponseMetadata: { Error: { Message: "e" } } },
    ]) {
      expect(errorMessage(JSON.stringify(body), 400)).toMatch(/[a-e]/)
    }
  })

  test("rejects missing loaders and delegates WorkBuddy", async () => {
    expect(new ProviderChatError("x")).toMatchObject({
      retryNext: true,
      retryToday: true,
    })
    await expect(sendProviderChat({ provider: "x" })).rejects.toThrow("no signed request")
    const result = await sendProviderChat({
      provider: "workbuddy",
      model: "m",
      loader: { fetch: async () => openAiStream("m") },
      inputTokens: 10,
      timeoutMs: 1_000,
      day: "2026-10-30",
      key: "k",
    })
    expect(result.reply).toBe("OK")
  })

  test("sends openai and anthropic chat requests", async () => {
    const calls = []
    const loader = {
      baseURL: "https://x.test/v1",
      fetch: async (url, init) => {
        calls.push({ url, body: JSON.parse(init.body) })
        return url.endsWith("/messages")
          ? sse('data: {"delta":{"text":"A"}}\n')
          : openAiStream("m", "B")
      },
    }
    expect((await sendProviderChat({
      provider: "qoder",
      model: "m",
      protocol: "openai",
      loader,
      inputTokens: 10,
      timeoutMs: 1_000,
      day: "2026-10-30",
      key: "k",
    })).reply).toBe("B")
    expect((await sendProviderChat({
      provider: "minimax-code",
      model: "m",
      protocol: "anthropic",
      loader,
      inputTokens: 10,
      timeoutMs: 1_000,
      day: "2026-10-30",
      key: "k",
    })).reply).toBe("A")
    expect(calls.map((call) => call.url)).toEqual([
      "https://x.test/v1/chat/completions",
      "https://x.test/v1/messages",
    ])
  })

  test("returns JSON and classifies provider failures", async () => {
    const jsonReply = await sendProviderChat({
      provider: "qoder",
      model: "m",
      protocol: "openai",
      loader: {
        baseURL: "https://x.test/v1",
        fetch: async () => json({ choices: [{ message: { content: "JSON" } }] }),
      },
      inputTokens: 10,
      timeoutMs: 1_000,
      day: "2026-10-30",
      key: "k",
    })
    expect(jsonReply.reply).toBe("JSON")

    const failures = [
      async () => { throw new Error("network token=secret") },
      async () => json({ error: { message: "bad", code: "E_BAD" } }, 429),
      async () => json({ code: "TOP" }, 500),
      async () => sse("data: [DONE]\n\n"),
    ]
    for (const fetch of failures) {
      let caught
      try {
        await sendProviderChat({
          provider: "qoder",
          model: "m",
          protocol: "openai",
          loader: { baseURL: "https://x.test/v1", fetch },
          inputTokens: 10,
          timeoutMs: 1_000,
          day: "2026-10-30",
          key: "k",
        })
      } catch (error) {
        caught = error
      }
      expect(caught).toBeInstanceOf(Error)
      expect(caught.retryNext).toBe(true)
    }
    await expect(sendProviderChat({
      provider: "qoder",
      model: "m",
      protocol: "openai",
      loader: {
        baseURL: "https://x.test/v1",
        fetch: () => new Promise(() => {}),
      },
      inputTokens: 10,
      timeoutMs: 10,
      day: "2026-10-30",
      key: "k",
    })).rejects.toThrow("qoder chat timed out after 10ms")

    try {
      await sendProviderChat({
        provider: "qoder",
        model: "m",
        protocol: "openai",
        loader: {
          baseURL: "https://x.test/v1",
          fetch: async () => json({
            error: { message: "Credits exhausted. Subscribe or purchase add-on packs." },
          }, 429),
        },
        inputTokens: 10,
        timeoutMs: 1_000,
        day: "2026-10-30",
        key: "k",
      })
    } catch (error) {
      expect(error).toMatchObject({
        retryNext: false,
        retryToday: false,
        status: 429,
      })
    }
  })
})
