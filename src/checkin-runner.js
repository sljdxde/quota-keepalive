import { readFile } from "node:fs/promises"
import { join } from "node:path"
import { beijingDay, errorText, finiteNumber, magpieConfigHome, sanitize, text } from "./common.js"
import {
  accountTag,
  newRunId,
} from "./log.js"
import { providerEnabled, taskControlId, taskEnabled } from "./preferences.js"
import { runDailyOnce } from "./state.js"
import {
  PROVIDER_ADAPTERS,
  accountResult,
  loadProviderFactory,
  saveAuthEntry,
  writeLog,
} from "./workbuddy.js"

function object(value) {
  return !!value && typeof value === "object" && !Array.isArray(value)
}

async function readJson(path, fallback) {
  try {
    return JSON.parse(await readFile(path, "utf8"))
  } catch (error) {
    if (error?.code === "ENOENT") return fallback
    throw error
  }
}

function checkinStatus(outcome) {
  if (["claimed", "done"].includes(outcome)) return "success"
  if (["inactive", "ineligible", "own-app", "captcha"].includes(outcome)) return "skipped"
  return "failed"
}

class ProviderCheckinError extends Error {
  constructor(message, result = {}) {
    super(message)
    this.name = "ProviderCheckinError"
    this.checkin = {
      outcome: sanitize(result?.outcome, 24) || "failed",
      credit: finiteNumber(result?.credit),
      streak: Math.max(0, Math.round(finiteNumber(result?.streak))),
      message: sanitize(result?.message || message, 300),
    }
  }
}

export function checkinResultFields(result, config, fallback = "") {
  if (result?.kind === "skipped") {
    if (result.reason === "done-today") {
      return {
        status: "skipped",
        outcome: "already-done",
        attempts: result.attempts,
        message: "今天已经完成签到。",
      }
    }
    if (result.reason === "quota-exhausted") {
      return {
        status: "failed",
        outcome: "quota-exhausted",
        attempts: result.attempts,
        retry: false,
        error: result.error || "This account has no usable quota for today's check-in.",
      }
    }
    return {
      status: "failed",
      outcome: "exhausted",
      attempts: result.attempts,
      error: result.error || `今日签到已达到 ${config.maxAttempts} 次尝试上限。`,
    }
  }
  if (result?.kind === "busy") {
    return {
      status: "skipped",
      outcome: "busy",
      retry: true,
      error: result.error,
    }
  }
  if (result?.kind === "unavailable") {
    return {
      status: "failed",
      outcome: "unavailable",
      error: result.error,
    }
  }

  const checkin = result?.ok ? result.result : result?.cause?.checkin
  const outcome = sanitize(checkin?.outcome, 24) ||
    (fallback ? "failed" : "unknown")
  const status = checkinStatus(outcome)
  const message = sanitize(checkin?.message, 300)
  const error = status === "failed"
    ? message || safeError(result?.error || fallback || "provider check-in failed")
    : ""
  const reportedOutcome = status === "failed"
    ? Number(result?.attempts) < Number(config.maxAttempts) ? "retry" : "exhausted"
    : outcome
  return {
    status,
    outcome: reportedOutcome,
    attempts: result?.attempts,
    retry: status === "failed" && Number(result?.attempts) < Number(config.maxAttempts),
    credit: finiteNumber(checkin?.credit),
    streak: Math.max(0, Math.round(finiteNumber(checkin?.streak))),
    ...(message ? { message } : {}),
    ...(error ? { error } : {}),
  }
}

function safeError(value) {
  return sanitize(errorText(value), 300)
}

async function runAccountCheckin({
  input,
  config,
  authPath,
  provider,
  providerMeta,
  key,
  stored,
  loadFactory,
}) {
  const client = {
    auth: {
      set: async ({ path, body } = {}) => {
        const id = text(path?.id).trim() || provider
        if (id !== provider) throw new Error("refusing to save another provider's sign-in")
        return saveAuthEntry(authPath, key, body)
      },
    },
  }
  const factory = await loadFactory(provider)
  const base = await factory({ ...input, client })
  const checkin = base?.auth?.checkin
  if (typeof checkin !== "function") {
    throw new ProviderCheckinError(
      `${providerMeta?.name || provider} 的登录插件没有提供签到接口。`,
      { outcome: "unsupported" },
    )
  }
  let answer
  try {
    answer = await checkin(async () => structuredClone(stored))
  } catch (error) {
    throw new ProviderCheckinError(errorText(error))
  }
  if (!object(answer)) {
    throw new ProviderCheckinError("供应商签到接口返回了无法识别的结果。")
  }
  const status = checkinStatus(sanitize(answer.outcome, 24))
  if (status === "failed") {
    throw new ProviderCheckinError(
      sanitize(answer.message, 300) || "供应商签到失败。",
      answer,
    )
  }
  return answer
}

export function createProviderCheckin(options = {}) {
  const { input, config } = options
  const configHome = options.configHome || config?.configHome || magpieConfigHome()
  const loadFactory = options.loadFactory || loadProviderFactory
  return async function runProviderCheckin({
    force = false,
    providers: onlyProviders = [],
    accounts: onlyAccounts = [],
  } = {}) {
    const [providers, auth] = await Promise.all([
      readJson(join(configHome, "plugin-providers.json"), []),
      readJson(join(configHome, "plugin-auth.json"), {}),
    ])
    const wanted = new Set(
      (Array.isArray(onlyProviders) ? onlyProviders : [])
        .map((id) => text(id).trim())
        .filter(Boolean),
    )
    const wantedAccounts = new Set(
      (Array.isArray(onlyAccounts) ? onlyAccounts : [])
        .map((id) => text(id).trim())
        .filter(Boolean),
    )
    const authPath = join(configHome, "plugin-auth.json")
    const day = beijingDay()
    const results = []
    let checkedProviders = 0

    for (const providerMeta of Array.isArray(providers) ? providers : []) {
      const provider = text(providerMeta?.id).trim()
      const adapter = PROVIDER_ADAPTERS[provider]
      if (!adapter || providerMeta?.checkin !== true || !Array.isArray(providerMeta?.accounts) ||
          !providerMeta.accounts.length) continue
      if (wanted.size && !wanted.has(provider)) continue
      checkedProviders++

      for (const [index, account] of providerMeta.accounts.entries()) {
        const controlId = taskControlId(provider, account, index)
        if (wantedAccounts.size && !wantedAccounts.has(controlId)) continue
        if (!providerEnabled(config, provider)) continue
        if (!wantedAccounts.size && !taskEnabled(config, provider, controlId, "checkin")) continue

        const key = text(account?.key).trim()
        const label = sanitize(account?.accountId || key, 120)
        const stored = object(auth) ? auth[key] : null
        if (!key || !object(stored)) {
          results.push(accountResult(
            provider,
            label,
            "skipped",
            "not-signed-in",
            "This provider sign-in is unavailable.",
          ))
          continue
        }

        const identity = text(stored.uid).trim() || key
        const logAccount = text(account?.accountId).trim() || key
        const run = newRunId()
        const result = await runDailyOnce({
          statePath: config.statePath,
          account: `checkin:${controlId}`,
          day,
          maxAttempts: config.maxAttempts,
          lockWaitMs: config.lockWaitMs,
          timeoutMs: config.timeoutMs,
          force,
          task: () => runAccountCheckin({
            input,
            config,
            authPath,
            provider,
            providerMeta,
            key,
            stored,
            loadFactory: (id) => loadFactory(id, configHome),
          }),
        })
        const fields = checkinResultFields(result, config)
        await writeLog(config, provider, logAccount, {
          event: "checkin",
          run,
          ...fields,
        })
        results.push(accountResult(
          provider,
          label,
          fields.status,
          fields.outcome,
          fields.message || fields.error || "",
        ))
      }
    }

    const failed = results.filter((item) => item.status === "failed").length
    const skipped = results.filter((item) => item.status === "skipped").length
    const success = results.filter((item) => item.status === "success").length
    const status = failed ? "failed" : success ? "success" : "skipped"
    const summary = !checkedProviders
      ? "没有找到支持签到的供应商账号。"
      : failed
        ? `签到完成：${success} 成功，${skipped} 跳过，${failed} 失败。`
        : success
          ? `签到完成：${success} 成功，${skipped} 跳过。`
          : `签到未执行：${skipped} 个账号被跳过。`
    return { status, accounts: results, summary }
  }
}
