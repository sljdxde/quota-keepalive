import { randomBytes } from "node:crypto"
import {
  chmod,
  mkdir,
  open,
  readFile,
  rename,
  stat,
  unlink,
} from "node:fs/promises"
import { dirname, join } from "node:path"
import { pathToFileURL } from "node:url"
import {
  beijingDay,
  errorText,
  magpieConfigHome,
  sanitize,
  text,
} from "./common.js"
import { rankedModels } from "./inventory.js"
import { providerEnabled, taskControlId, taskEnabled } from "./preferences.js"
import { ProviderChatError, sendProviderChat } from "./provider-chat.js"
import {
  accountTag,
  appendDailyLog,
  newRunId,
  readDailyLogs,
  safeLogText,
} from "./log.js"
import { runDailyOnce } from "./state.js"

const AUTH_LOCK_STALE_MS = 30_000
const FILE_OPS = { chmod, mkdir, open, readFile, rename, stat, unlink }

export const PROVIDER_ADAPTERS = Object.freeze({
  workbuddy: {
    package: "@magpie-community/opencode-workbuddy-auth",
    exports: ["WorkBuddyAuthPlugin"],
    protocol: "openai",
  },
  "workbuddy-ai": {
    package: "@magpie-community/opencode-workbuddy-auth",
    exports: ["WorkBuddyAIAuthPlugin"],
    protocol: "openai",
  },
  codearts: {
    package: "@magpie-community/opencode-codearts-auth",
    exports: ["CodeArtsAuthPlugin"],
    protocol: "openai",
  },
  "minimax-code": {
    package: "@magpie-community/opencode-minimax-auth",
    exports: ["MiniMaxCodeAuthPlugin"],
    protocol: "anthropic",
  },
  "minimax-code-global": {
    package: "@magpie-community/opencode-minimax-auth",
    exports: ["MiniMaxCodeGlobalAuthPlugin"],
    protocol: "anthropic",
  },
  qoder: {
    package: "@magpie-community/opencode-qoder-auth",
    exports: ["QoderAuthPlugin"],
    protocol: "openai",
  },
  "qoder-cn": {
    package: "@magpie-community/opencode-qoder-auth",
    exports: ["QoderCNAuthPlugin"],
    protocol: "openai",
  },
  qwenwork: {
    package: "@magpie-community/opencode-qoder-auth",
    exports: ["QwenWorkAuthPlugin"],
    protocol: "openai",
  },
  "qwenwork-enterprise": {
    package: "@magpie-community/opencode-qoder-auth",
    exports: ["QwenWorkEnterpriseAuthPlugin"],
    protocol: "openai",
  },
  "trae-cn": {
    package: "@magpie-community/opencode-trae-auth",
    exports: ["TraeCNAuthPlugin"],
    protocol: "openai",
  },
  "trae-global": {
    package: "@magpie-community/opencode-trae-auth",
    exports: ["TraeGlobalAuthPlugin"],
    protocol: "openai",
  },
  zcode: {
    package: "@magpie-community/opencode-zcode-auth",
    exports: ["ZCodeAuthPlugin"],
    protocol: "anthropic",
  },
  "opencode-zen-free": {
    package: "@magpie-community/opencode-zen-free-auth",
    exports: ["ZenFreePlugin"],
    protocol: "openai",
  },
})

export async function importMagpiePlugin(packageName, configHome) {
  let lastError
  try {
    return await import(packageName)
  } catch (error) {
    lastError = error
  }
  const path = join(configHome, "plugins", "node_modules", packageName, "index.mjs")
  try {
    return await import(pathToFileURL(path).href)
  } catch (error) {
    lastError = error
  }
  throw new Error(
    `provider login package ${packageName} is unavailable; reinstall or update that provider plugin in Magpie (${
      lastError?.message || lastError
    })`,
  )
}

export async function loadProviderFactory(provider, configHome = magpieConfigHome()) {
  const adapter = PROVIDER_ADAPTERS[provider]
  if (!adapter) throw new Error(`${provider} has no registered keepalive adapter`)
  const module = await importMagpiePlugin(adapter.package, configHome)
  for (const name of adapter.exports) {
    if (typeof module?.[name] === "function") return module[name]
  }
  throw new Error(`${adapter.package} does not export ${adapter.exports.join(", ")}`)
}

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

export async function withAuthLock(authPath, work, waitMs = 2_000, ops = FILE_OPS) {
  const lockPath = `${authPath}.lock`
  const deadline = Date.now() + waitMs
  await ops.mkdir(dirname(authPath), { recursive: true, mode: 0o700 })
  for (;;) {
    let handle
    try {
      handle = await ops.open(lockPath, "wx", 0o600)
      await handle.writeFile(JSON.stringify({ pid: process.pid, at: new Date().toISOString() }))
      await handle.close()
      break
    } catch (error) {
      if (handle) await handle.close().catch(() => {})
      if (error?.code !== "EEXIST") throw error
      let age = 0
      try {
        age = Date.now() - (await ops.stat(lockPath)).mtimeMs
      } catch {
        continue
      }
      if (age > AUTH_LOCK_STALE_MS) {
        await ops.unlink(lockPath).catch(() => {})
        continue
      }
      if (Date.now() >= deadline) throw new Error("magpie's sign-in file is busy")
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
  }
  try {
    return await work()
  } finally {
    await ops.unlink(lockPath).catch(() => {})
  }
}

export async function saveAuthEntry(authPath, key, next, ops = FILE_OPS) {
  if (!text(key) || !object(next)) throw new Error("refusing to save an invalid provider sign-in")
  return withAuthLock(authPath, async () => {
    for (let attempt = 0; attempt < 4; attempt++) {
      const snapshot = await readAuthSnapshot(authPath, ops)
      const all = snapshot.value
      if (!object(all)) throw new Error("plugin-auth.json isn't an object")
      if (JSON.stringify(all[key]) === JSON.stringify(next)) return true

      all[key] = next
      const serialized = `${JSON.stringify(all, null, 2)}\n`
      const tmp = `${authPath}.tmp-${process.pid}-${randomBytes(6).toString("hex")}`
      let handle
      try {
        handle = await ops.open(tmp, "wx", 0o600)
        await handle.writeFile(serialized, "utf8")
        await handle.sync()
        await handle.close()
        handle = null

        // Magpie and provider plugins do not use this lock. Refuse to replace
        // a file that changed after our read, then merge against the new copy.
        const current = await readAuthStamp(authPath, ops)
        if (current !== snapshot.stamp) {
          await ops.unlink(tmp).catch(() => {})
          await new Promise((resolve) => setTimeout(resolve, 20))
          continue
        }
        await ops.rename(tmp, authPath)
        await ops.chmod(authPath, 0o600)
        return true
      } catch (error) {
        if (handle) await handle.close().catch(() => {})
        await ops.unlink(tmp).catch(() => {})
        throw error
      }
    }
    throw new Error("magpie's sign-in file changed repeatedly; the update was not saved")
  })
}

function authStamp(info) {
  if (!info) return "missing"
  return [
    info.ino ?? "",
    info.size ?? "",
    info.mtimeMs ?? "",
    info.ctimeMs ?? "",
  ].join(":")
}

async function readAuthStamp(authPath, ops) {
  try {
    return authStamp(await ops.stat(authPath))
  } catch (error) {
    if (error?.code === "ENOENT") return "missing"
    throw error
  }
}

async function readAuthSnapshot(authPath, ops) {
  for (let attempt = 0; attempt < 4; attempt++) {
    const before = await readAuthStamp(authPath, ops)
    if (before === "missing") {
      return { value: {}, stamp: before }
    }
    let raw
    try {
      raw = await ops.readFile(authPath, "utf8")
    } catch (error) {
      if (error?.code === "ENOENT") return { value: {}, stamp: "missing" }
      throw error
    }
    const after = await readAuthStamp(authPath, ops)
    if (before === after) {
      return { value: JSON.parse(raw), stamp: after }
    }
    await new Promise((resolve) => setTimeout(resolve, 20))
  }
  throw new Error("magpie's sign-in file kept changing while it was being read")
}

export function authIdentity(auth, key) {
  const uid = text(auth?.uid).trim()
  const extra = text(auth?.enterpriseId || auth?.realUserID || auth?.refresh).trim()
  return uid ? `${uid}|${extra}` : key
}

export function resultFields(result, config, model = "") {
  const longProbe = config?.probeMode === "context" || config?.probeMode === "activity"
  const configuredInputTokens = longProbe ? Number(config.inputTokens) || 0 : 0
  switch (result?.kind) {
    case "skipped":
      if (result.reason === "done-today") {
        return {
          status: "skipped",
          outcome: "already-done",
          attempts: result.attempts,
          message: "Keepalive was already sent today.",
          model,
        }
      }
      if (result.reason === "quota-exhausted") {
        return {
          status: "failed",
          outcome: "quota-exhausted",
          attempts: result.attempts,
          model,
          retry: false,
          error: result.error || "This account has no usable quota for today's keepalive.",
        }
      }
      return {
        status: "failed",
        outcome: "exhausted",
        attempts: result.attempts,
        model,
        error: result.error || `Daily keepalive reached its ${config.maxAttempts}-attempt limit.`,
      }
    case "busy":
      return {
        status: "skipped",
        outcome: "busy",
        model,
        retry: true,
        error: result.error,
      }
    case "unavailable":
      return {
        status: "failed",
        outcome: "unavailable",
        model,
        error: result.error,
      }
    case "sent":
      const terminal = result.cause?.retryToday === false
      const reply = result.result || {}
      const inputTokens = Number(reply.promptTokens) || configuredInputTokens
      return {
        status: result.ok ? "success" : "failed",
        outcome: result.ok
          ? "sent"
          : terminal
            ? "quota-exhausted"
            : result.attempts < config.maxAttempts
              ? "retry"
              : "exhausted",
        attempts: result.attempts,
        model: reply.model || model,
        probeMode: config?.probeMode,
        inputTokens,
        ...(reply.credit === undefined ? {} : { credit: reply.credit }),
        ...(reply.activityVerified === undefined ? {} : { activityVerified: reply.activityVerified }),
        retry: !result.ok && !terminal && result.attempts < config.maxAttempts,
        persisted: result.persisted !== false,
        error: result.error,
      }
    default:
      return {
        status: "failed",
        outcome: "unknown",
        model,
        error: "Keepalive returned no recognized result.",
      }
  }
}

export async function writeLog(config, provider, account, fields) {
  const entry = {
    ts: new Date().toISOString(),
    day: beijingDay(),
    provider,
    run: fields.run || newRunId(),
    ...fields,
  }
  if (fields.message) entry.message = safeLogText(fields.message, account, 480)
  if (fields.error) entry.error = safeLogText(fields.error, account, 480)
  const tag = accountTag(`${provider}|${account}`)
  if (tag) entry.account = tag
  return appendDailyLog({
    logPath: config.logPath,
    logMaxBytes: config.logMaxBytes,
    entry,
  })
}

export function accountResult(provider, account, status, outcome, message) {
  return {
    provider,
    account,
    action: "keepalive",
    status,
    outcome,
    message,
  }
}

export async function sendWithCandidates({
  input,
  config,
  authPath,
  provider,
  providerMeta,
  key,
  stored,
  identity,
  logAccount,
  candidates,
  day,
  run,
  loadFactory,
  sendChat = sendProviderChat,
}) {
  const adapter = PROVIDER_ADAPTERS[provider]
  if (!adapter) throw new Error(`${provider} has no keepalive adapter`)

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
  const loader = await base?.auth?.loader(
    async () => structuredClone(stored),
    { id: provider, name: providerMeta?.name || provider },
  )
  if (!loader || typeof loader.fetch !== "function") {
    throw new Error(`${provider}'s login plugin returned no signed request`)
  }

  let lastError = ""
  let lastFailure = null
  for (const [index, candidate] of candidates.entries()) {
    try {
      const reply = await sendChat({
        provider,
        model: candidate.id,
        protocol: candidate.npm === "@ai-sdk/anthropic" ? "anthropic" : adapter.protocol,
        loader,
        inputTokens: config.inputTokens,
        probeMode: config.probeMode,
        probeMessage: config.probeMessage,
        timeoutMs: config.timeoutMs,
        day,
        key: accountTag(identity),
      })
      await writeLog(config, provider, logAccount, {
        event: "keepalive-model",
        status: "success",
        run,
        outcome: index > 0 ? "fallback" : "selected",
        model: candidate.id,
        message: index > 0
          ? `Switched to ${candidate.name || candidate.id} after ${index} unavailable model(s).`
          : "",
      })
      return { reply, model: reply.model || candidate.id }
    } catch (error) {
      lastFailure = error
      lastError = errorText(error)
      const canFallback = error?.retryNext !== false
      const retryToday = error?.retryToday !== false
      await writeLog(config, provider, logAccount, {
        event: "keepalive-model",
        status: "failed",
        run,
        outcome: canFallback ? "unavailable" : "failed",
        model: candidate.id,
        error: lastError,
      })
      if (!canFallback || !retryToday) break
    }
  }
  throw lastFailure || new Error(lastError || `${provider} had no available keepalive model`)
}

export function createProviderKeepalive(options = {}) {
  const { input, config } = options
  const configHome = options.configHome || config?.configHome || magpieConfigHome()
  const loadFactory = options.loadFactory || loadProviderFactory
  return async function runProviderKeepalive({
    force = false,
    providers: onlyProviders = [],
    accounts: onlyAccounts = [],
  } = {}) {
    const [providers, auth, recentLogs] = await Promise.all([
      readJson(join(configHome, "plugin-providers.json"), []),
      readJson(join(configHome, "plugin-auth.json"), {}),
      readDailyLogs({
        logPath: config.logPath,
        includePrevious: true,
        limit: 5_000,
      }).catch(() => ({ records: [] })),
    ])
    const records = Array.isArray(recentLogs?.records) ? recentLogs.records : []
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
      if (!adapter || !Array.isArray(providerMeta?.accounts) || !providerMeta.accounts.length) continue
      if (wanted.size && !wanted.has(provider)) continue
      checkedProviders++

      for (const [index, account] of providerMeta.accounts.entries()) {
        const controlId = taskControlId(provider, account, index)
        if (wantedAccounts.size && !wantedAccounts.has(controlId)) continue
        if (!providerEnabled(config, provider)) continue
        if (!wantedAccounts.size && !taskEnabled(config, provider, controlId, "keepalive")) continue
        const key = text(account?.key).trim()
        const label = sanitize(account?.accountId || key, 120)
        const stored = object(auth) ? auth[key] : null
        if (!key || !object(stored)) {
          results.push(accountResult(provider, label, "skipped", "not-signed-in", "This provider sign-in is unavailable."))
          continue
        }
        const identity = authIdentity(stored, key)
        const logAccount = text(account?.accountId).trim() || key
        const candidates = rankedModels(providerMeta.models, account.models, {
          preferred: config.modelPreferences?.[provider] || config.model,
          maxCandidates: config.modelCandidates,
          inputTokens: config.inputTokens,
          records,
          provider,
          account: accountTag(`${provider}|${identity}`),
        })
        if (!candidates.length) {
          results.push(accountResult(provider, label, "failed", "no-model", "This account lists no usable model for keepalive."))
          continue
        }

        const run = newRunId()
        await writeLog(config, provider, logAccount, {
          event: "started",
          status: "started",
          run,
          model: candidates[0].id,
          models: candidates.map((candidate) => candidate.id).join(","),
          inputTokens: ["context", "activity"].includes(config.probeMode) ? config.inputTokens : 0,
          probeMode: config.probeMode,
          keepalive: true,
        })

        const activityMode = config.probeMode === "short" ? "short" : "activity"
        const result = await runDailyOnce({
          statePath: config.statePath,
          // A legacy short probe and a real activity conversation are not the
          // same task. Keeping them separate lets a user switch to activity
          // after a short probe has already been recorded today.
          account: accountTag(`${provider}|${identity}|keepalive:${activityMode}`),
          day,
          model: candidates[0].id,
          inputTokens: ["context", "activity"].includes(config.probeMode) ? config.inputTokens : 0,
          probeMode: config.probeMode,
          maxAttempts: config.maxAttempts,
          lockWaitMs: config.lockWaitMs,
          timeoutMs: config.timeoutMs,
          force,
          task: () => sendWithCandidates({
            input,
            config,
            authPath,
            provider,
            providerMeta,
            key,
            stored,
            identity,
            logAccount,
            candidates,
            day,
            run,
            loadFactory: (provider) => loadFactory(provider, configHome),
          }),
        })
        const fields = resultFields(result, config, candidates[0].id)
        await writeLog(config, provider, logAccount, {
          event: "keepalive",
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
    const sent = results.filter((item) => item.status === "success").length
    const status = failed ? "failed" : sent ? "success" : "skipped"
    const summary = !checkedProviders
      ? "没有找到支持真实对话保活的供应商账号。"
      : failed
        ? `保活完成：${sent} 成功，${skipped} 跳过，${failed} 失败。`
        : sent
          ? `保活完成：${sent} 个账号已发送真实对话；${skipped} 个账号跳过。`
          : `保活未发送：${skipped} 个账号被跳过。`
    return { status, accounts: results, summary }
  }
}

// Kept for callers of the first WorkBuddy-only build.
export const createWorkBuddyKeepalive = createProviderKeepalive
