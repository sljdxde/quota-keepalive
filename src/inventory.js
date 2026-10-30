import { readFile } from "node:fs/promises"
import { join } from "node:path"
import { beijingDay, magpieConfigHome, sanitize } from "./common.js"
import { accountTag } from "./log.js"
import { taskControlId } from "./preferences.js"

export const KEEPALIVE_PROVIDERS = new Set([
  "workbuddy",
  "workbuddy-ai",
  "codearts",
  "minimax-code",
  "minimax-code-global",
  "qoder",
  "qoder-cn",
  "qwenwork",
  "qwenwork-enterprise",
  "trae-cn",
  "trae-global",
  "zcode",
  "opencode-zen-free",
])
const BUILTIN_NAMES = Object.freeze({
  claude: "Claude Code",
  codex: "Codex",
  copilot: "Copilot",
  gemini: "Gemini CLI",
  antigravity: "Antigravity",
  zed: "Zed",
  factory: "Factory",
  mimo: "Xiaomi MiMo",
})

function object(value) {
  return !!value && typeof value === "object" && !Array.isArray(value)
}

const ACTIVITY_DAYS = 5
const ACTIVITY_EVENTS = new Set(["checkin", "keepalive", "keepalive-model", "finished"])
const AVAILABILITY_DAYS = 5
const DEFAULT_PROVIDER_MODELS = Object.freeze({
  // dsh's long-running WorkBuddy probe uses this model because the 0x models
  // are frequently throttled by the CN gateway.
  workbuddy: "deepseek-v4.1-flash",
})

async function readJson(path, fallback) {
  try {
    return JSON.parse(await readFile(path, "utf8"))
  } catch {
    return fallback
  }
}

function finite(value) {
  return typeof value === "number" && Number.isFinite(value) ? value : 0
}

function modelCost(model) {
  if (!object(model?.cost)) return 0
  return finite(model.cost.input) + finite(model.cost.output)
}

function lowerScore(a, b) {
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return a[i] < b[i]
  }
  return false
}

function emptyHealth() {
  return { successes: 0, failures: 0, lastSuccess: 0, lastFailure: 0 }
}

function updateHealth(map, model, record) {
  if (!map.has(model)) map.set(model, emptyHealth())
  const health = map.get(model)
  const at = Date.parse(record?.ts)
  if (record?.status === "success") {
    health.successes++
    if (Number.isFinite(at)) health.lastSuccess = Math.max(health.lastSuccess, at)
  } else if (record?.status === "failed") {
    health.failures++
    if (Number.isFinite(at)) health.lastFailure = Math.max(health.lastFailure, at)
  }
}

function availabilityMaps(records, provider, account, sinceDay) {
  const global = new Map()
  const local = new Map()
  const tags = new Set(
    (Array.isArray(account) ? account : [account])
      .filter((value) => typeof value === "string" && value),
  )
  for (const record of Array.isArray(records) ? records : []) {
    if (
      record?.provider !== provider ||
      !["keepalive-model", "keepalive"].includes(record?.event) ||
      typeof record?.model !== "string" ||
      !record.model ||
      typeof record?.day !== "string" ||
      (sinceDay && record.day < sinceDay) ||
      (record.event === "keepalive" && record.status !== "success")
    ) continue
    updateHealth(global, record.model, record)
    if (tags.has(record.account)) updateHealth(local, record.model, record)
  }
  return { global, local }
}

function availabilityScore(modelId, maps) {
  const own = maps.local.get(modelId) || emptyHealth()
  const all = maps.global.get(modelId) || emptyHealth()
  const ownKnown = own.successes + own.failures
  const tier = own.successes
    ? 0
    : ownKnown
      ? 3
      : all.successes
        ? 1
        : all.failures
          ? 3
          : 2
  const successes = own.successes * 2 + all.successes
  const failures = own.failures * 2 + all.failures
  const attempts = successes + failures
  return [
    tier,
    attempts ? -(successes / attempts) : 0,
    -Math.max(own.lastSuccess, all.lastSuccess),
    -successes,
    failures,
  ]
}

export function defaultKeepaliveModel(models, options = {}) {
  return rankedModels(models, [], {
    ...options,
    maxCandidates: 1,
  })[0] || null
}

export function rankedModels(models, accountModels = [], {
  preferred = "",
  maxCandidates = 4,
  inputTokens = 0,
  records = [],
  provider = "",
  account = "",
  sinceDay = "",
} = {}) {
  const allowed = new Set(
    (Array.isArray(accountModels) ? accountModels : [])
      .filter((id) => typeof id === "string" && id.trim())
      .map((id) => id.trim()),
  )
  const historyStart = sinceDay || recentDays(beijingDay(), AVAILABILITY_DAYS)[0]
  const maps = availabilityMaps(records, provider, account, historyStart)
  const candidates = []
  for (const model of Array.isArray(models) ? models : []) {
    if (!object(model) || typeof model.id !== "string" || !model.id) continue
    if (allowed.size && !allowed.has(model.id)) continue
    const context = finite(model.context || model.limit?.context)
    if (inputTokens > 0 && context > 0 && context <= inputTokens + 16) continue
    candidates.push({
      id: model.id,
      name: sanitize(model.name || model.id, 120),
      npm: sanitize(model.npm || model.api?.npm || "", 80),
      free: model.free === true,
      rate: finite(model.rate),
      cost: modelCost(model),
      score: availabilityScore(model.id, maps),
    })
  }
  candidates.sort((a, b) => lowerScore(a.score, b.score) ? -1 : lowerScore(b.score, a.score) ? 1 : 0)
  if (preferred) {
    const at = candidates.findIndex((candidate) => candidate.id === preferred)
    if (at > 0) candidates.unshift(...candidates.splice(at, 1))
  } else {
    const preferredId = DEFAULT_PROVIDER_MODELS[provider] || ""
    const at = candidates.findIndex((candidate) => candidate.id === preferredId)
    // The provider default is a hint, not a override of local evidence. Keep
    // a model with a proven success or a known failure ahead of the hint.
    if (
      preferredId &&
      at > 0 &&
      candidates[at].score[0] < 3 &&
      candidates[at].score[0] <= candidates[0].score[0]
    ) {
      candidates.unshift(...candidates.splice(at, 1))
    }
  }
  return candidates.slice(0, Math.max(1, Math.min(12, Number(maxCandidates) || 4)))
}

export function maskAccount(account, index = 0) {
  const id = typeof account?.accountId === "string" ? account.accountId.trim() : ""
  if (!id) return `account-${index + 1}`
  if (id.includes("@")) {
    const [name, domain] = id.split("@", 2)
    return `${name.slice(0, 2)}***@${domain}`.slice(0, 120)
  }
  if (id.length <= 28) return sanitize(id, 120)
  return `${sanitize(id.slice(0, 10), 40)}...${sanitize(id.slice(-6), 20)}`
}

function accountLabel(account, index) {
  return maskAccount(account, index)
}

function publicWindow(window) {
  const out = {}
  if (typeof window?.name === "string" && window.name) out.name = sanitize(window.name, 80)
  for (const name of ["used", "remaining", "limit", "amount", "balance", "runsOut"]) {
    if (typeof window?.[name] === "number" && Number.isFinite(window[name])) out[name] = window[name]
  }
  for (const name of ["display", "unit", "resetsAt", "runsOutAt"]) {
    if (typeof window?.[name] === "string" && window[name]) out[name] = sanitize(window[name], 120)
  }
  return out
}

function publicModel(model) {
  if (!object(model) || typeof model.id !== "string" || !model.id) return null
  return {
    id: sanitize(model.id, 160),
    name: sanitize(model.name || model.id, 160),
    free: model.free === true,
    rate: finite(model.rate),
    rateWas: finite(model.rateWas),
    cost: modelCost(model),
    context: finite(model.context || model.limit?.context),
    npm: sanitize(model.npm || model.api?.npm || "", 80),
  }
}

export function cardKey(provider, user) {
  return `${provider}/${String(user || "").toLowerCase()}`
}

function historyByAccount(history) {
  const out = new Map()
  for (const [key, lines] of Object.entries(object(history) ? history : {})) {
    const separator = key.indexOf("|")
    if (separator < 0) continue
    const provider = key.slice(0, separator)
    const user = key.slice(separator + 1)
    const points = []
    for (const [name, values] of Object.entries(object(lines) ? lines : {})) {
      for (const point of Array.isArray(values) ? values : []) {
        if (typeof point?.at !== "string" || typeof point?.left !== "number" || !Number.isFinite(point.left)) continue
        points.push({
          name: sanitize(name, 80),
          at: sanitize(point.at, 40),
          left: point.left,
        })
      }
    }
    points.sort((a, b) => a.at.localeCompare(b.at))
    out.set(cardKey(provider, user), points.slice(-40))
  }
  return out
}

export function quotaByAccount(quotas) {
  const out = new Map()
  for (const [key, entry] of Object.entries(object(quotas) ? quotas : {})) {
    if (!object(entry?.quota)) continue
    out.set(key.toLowerCase(), entry)
  }
  return out
}

function checkinByKey(checkins) {
  return object(checkins) ? checkins : {}
}

function accountRowQuota(row) {
  const windows = (Array.isArray(row?.windows) ? row.windows : []).map(publicWindow)
  if (!windows.length && !row?.plan && !row?.balance) return null
  return {
    plan: sanitize(row?.plan, 80),
    balance: sanitize(row?.balance, 80),
    readAt: sanitize(row?.readAt, 40),
    windows,
  }
}

function redactAccount(value, account, label) {
  let out = sanitize(value, 300)
  const id = typeof account?.accountId === "string" ? account.accountId.trim() : ""
  if (id && out) out = out.split(id).join(label || maskAccount(account))
  return out
}

function recentDays(today, count = ACTIVITY_DAYS) {
  const anchor = Date.parse(`${today}T00:00:00Z`)
  if (!Number.isFinite(anchor)) return [today]
  return Array.from({ length: count }, (_, index) =>
    new Date(anchor - (count - 1 - index) * 86_400_000).toISOString().slice(0, 10)
  )
}

function activityIndex(records) {
  const byTag = new Map()
  for (const record of Array.isArray(records) ? records : []) {
    const tag = typeof record?.account === "string" ? record.account : ""
    if (!tag || !record?.provider || !record?.day || !ACTIVITY_EVENTS.has(record?.event)) continue
    if (!byTag.has(tag)) byTag.set(tag, [])
    byTag.get(tag).push(record)
  }
  return byTag
}

function accountTags(provider, key, user, auth) {
  const tags = new Set()
  const id = String(user || "").trim()
  const accountKey = String(key || "").trim()
  if (id) {
    tags.add(accountTag(`${provider}|${id}`))
    tags.add(accountTag(id))
  }
  if (accountKey) tags.add(accountTag(accountKey))
  if (object(auth)) {
    const uid = typeof auth.uid === "string" ? auth.uid.trim() : ""
    const extra = typeof (auth.enterpriseId || auth.realUserID || auth.refresh) === "string"
      ? String(auth.enterpriseId || auth.realUserID || auth.refresh).trim()
      : ""
    const identity = uid ? `${uid}|${extra}` : accountKey
    if (identity) {
      tags.add(accountTag(identity))
      tags.add(accountTag(`${provider}|${identity}`))
    }
  }
  tags.delete("")
  return [...tags]
}

function recordsFor(index, tags, provider) {
  const byIdentity = new Map()
  for (const tag of tags) {
    for (const record of index.get(tag) || []) {
      if (record?.provider !== provider) continue
      byIdentity.set(`${record.ts}|${record.event}|${record.status}|${record.outcome || ""}`, record)
    }
  }
  return [...byIdentity.values()]
}

function latestFor(index, tags, provider) {
  let latest = null
  for (const record of recordsFor(index, tags, provider)) {
    if (!["checkin", "keepalive", "error", "finished"].includes(record?.event)) continue
    if (!latest || String(record.ts || "") > String(latest.at || "")) {
      latest = {
        at: sanitize(record.ts, 40),
        event: sanitize(record.event, 20),
        status: sanitize(record.status, 12),
        outcome: sanitize(record.outcome, 24),
        model: sanitize(record.model, 160),
        message: sanitize(record.message || record.error, 300),
      }
    }
  }
  return latest
}

function latestKeepaliveModel(index, tags, provider) {
  const records = recordsFor(index, tags, provider)
    .filter((record) =>
      ["keepalive", "keepalive-model"].includes(record?.event) &&
      typeof record?.model === "string" &&
      record.model.trim()
    )
    .sort((left, right) => String(left.ts || "").localeCompare(String(right.ts || "")))
  return sanitize(records.at(-1)?.model || "", 160)
}

export function successfulActivity(record, event) {
  if (!record) return false
  if (event === "checkin") {
    return record.status === "success" ||
      ["claimed", "done", "already-done"].includes(record.outcome)
  }
  if (event === "keepalive") {
    return record.status === "success" ||
      ["sent", "already-done", "done"].includes(record.outcome)
  }
  return false
}

function chooseActivity(records) {
  const finals = records
    .filter((record) => record?.status !== "started")
    .sort((a, b) => String(a.ts || "").localeCompare(String(b.ts || "")))
  if (!finals.length) return null
  return finals.findLast((record) => record.status === "success") || finals.at(-1)
}

function activityPoint(record, event) {
  if (!record) return null
  const success = successfulActivity(record, event)
  return {
    at: sanitize(record.ts, 40),
    status: sanitize(record.status, 12),
    outcome: sanitize(record.outcome, 24),
    success,
    credit: event === "checkin" && typeof record.credit === "number" && Number.isFinite(record.credit)
      ? record.credit
      : 0,
    streak: event === "checkin" && typeof record.streak === "number" && Number.isFinite(record.streak)
      ? record.streak
      : 0,
    model: event === "keepalive" ? sanitize(record.model, 160) : "",
    message: sanitize(record.message || record.error, 240),
  }
}

function activitySummary(days) {
  const checkinDays = days.filter((day) => day?.checkin?.success).length
  const keepaliveDays = days.filter((day) => day?.keepalive?.success).length
  const failedDays = days.filter((day) =>
    day?.checkin?.status === "failed" || day?.keepalive?.status === "failed"
  ).length
  return { checkinDays, keepaliveDays, failedDays, days: days.length }
}

function activityFor(index, tags, provider, checkin, today = beijingDay()) {
  const source = recordsFor(index, tags, provider)
  const days = recentDays(today).map((day) => {
    const sameDay = source.filter((record) => record?.day === day)
    return {
      day,
      checkin: activityPoint(chooseActivity(sameDay.filter((record) => record?.event === "checkin")), "checkin"),
      keepalive: activityPoint(chooseActivity(sameDay.filter((record) => record?.event === "keepalive")), "keepalive"),
    }
  })
  const current = days.find((day) => day.day === today)
  if (current && checkin?.day === today) {
    if (!current.checkin) {
      current.checkin = {
        at: sanitize(checkin.at, 40),
        status: ["claimed", "done", "already-done"].includes(checkin.outcome) ? "success" : "skipped",
        outcome: sanitize(checkin.outcome, 24),
        success: ["claimed", "done", "already-done"].includes(checkin.outcome),
        credit: finite(checkin.credit),
        streak: finite(checkin.streak),
        message: sanitize(checkin.message, 240),
      }
    } else {
      if (!current.checkin.credit) current.checkin.credit = finite(checkin.credit)
      if (!current.checkin.streak) current.checkin.streak = finite(checkin.streak)
      if (!current.checkin.message) current.checkin.message = sanitize(checkin.message, 240)
    }
  }
  return { today: current || null, days, summary: activitySummary(days) }
}

function providerRow(provider, checkins, quotas, histories, accountRows = [], activity, records, auth = {}) {
  const models = Array.isArray(provider?.models) ? provider.models : []
  const sourceAccounts = Array.isArray(provider?.accounts) ? provider.accounts : []
  const keepaliveSupported = KEEPALIVE_PROVIDERS.has(provider?.id)
  return {
    id: sanitize(provider?.id, 80),
    name: sanitize(provider?.name || provider?.id, 120),
    icon: typeof provider?.icon === "string" ? sanitize(provider.icon, 24_000) : "",
    signedIn: sourceAccounts.length > 0 || provider?.signedIn === true,
    accountCount: sourceAccounts.length,
    checkinSupported: provider?.checkin === true && keepaliveSupported,
    keepaliveSupported,
    keepaliveNote: keepaliveSupported ? "" : "该供应商暂未提供可用的真实对话保活通道",
    defaultModel: defaultKeepaliveModel(models, {
      records,
      provider: provider?.id,
    }),
    models: models.map(publicModel).filter(Boolean),
    accounts: sourceAccounts.map((account, index) => {
      const key = typeof account?.key === "string" ? account.key : ""
      const user = account?.accountId || ""
      const qkey = cardKey(provider?.id, user).toLowerCase()
      const row = accountRows.find((item) =>
        item?.agent === provider?.id &&
        String(item?.user || "").toLowerCase() === String(user).toLowerCase()
      )
      const quota = accountRowQuota(row) || quotas.get(qkey)?.quota || null
      const checkin = checkins[`${provider?.id}|${key}`] || null
      const label = accountLabel(account, index)
      const tags = accountTags(provider?.id, key, user, auth[key])
      return {
        index,
        controlId: taskControlId(provider?.id, account, index),
        label,
        keyTag: key ? sanitize(key.split("#").pop() || "primary", 24) : "",
        type: account?.type === "api" ? "api" : "oauth",
        active: row?.active,
        on: row?.on,
        checkin: checkin
          ? {
              day: sanitize(checkin.day, 10),
              at: sanitize(checkin.at, 40),
              outcome: sanitize(checkin.outcome, 24),
              credit: finite(checkin.credit),
              streak: finite(checkin.streak),
              message: sanitize(checkin.msg, 300),
            }
          : null,
        quota: quota
          ? {
              plan: sanitize(quota.plan, 80),
              balance: sanitize(quota.balance, 80),
              readAt: sanitize(quotas.get(qkey)?.at, 40),
              windows: (Array.isArray(quota.windows) ? quota.windows : []).map(publicWindow),
            }
          : null,
        history: histories.get(qkey) || [],
        latest: latestFor(activity, tags, provider?.id),
        lastModel: latestKeepaliveModel(activity, tags, provider?.id),
        activity: activityFor(activity, tags, provider?.id, checkin),
        error: redactAccount(quota?.error || account?.listError || "", account, label),
      }
    }),
  }
}

function builtinProviderRows(accountRows, providers, checkins, quotas, histories, activity) {
  const known = new Set(providers.map((provider) => provider.id))
  const groups = new Map()
  for (const row of accountRows) {
    const id = typeof row?.agent === "string" ? row.agent.trim() : ""
    if (!id || known.has(id)) continue
    if (!groups.has(id)) groups.set(id, [])
    groups.get(id).push(row)
  }
  return [...groups.entries()].map(([id, rows]) => ({
    id: sanitize(id, 80),
    name: sanitize(BUILTIN_NAMES[id] || id, 120),
    signedIn: true,
    accountCount: rows.length,
    checkinSupported: false,
    keepaliveSupported: false,
    keepaliveNote: "内置供应商暂不提供真实对话保活",
    defaultModel: null,
    models: [],
    accounts: rows.map((row, index) => {
      const user = row?.user || ""
      const qkey = cardKey(id, user).toLowerCase()
      const quota = accountRowQuota(row) || quotas.get(qkey)?.quota || null
      const checkin = checkins[`${id}|${user}`] || null
      const label = maskAccount({ accountId: row?.alias || user }, index)
      const tags = accountTags(id, "", user, null)
      return {
        index,
        controlId: taskControlId(id, row, index),
        label,
        keyTag: "",
        type: "builtin",
        active: row?.active,
        on: row?.on,
        checkin: checkin
          ? {
              day: sanitize(checkin.day, 10),
              at: sanitize(checkin.at, 40),
              outcome: sanitize(checkin.outcome, 24),
              credit: finite(checkin.credit),
              streak: finite(checkin.streak),
              message: sanitize(checkin.msg, 300),
            }
          : null,
        quota,
        history: histories.get(qkey) || [],
        latest: latestFor(activity, tags, id),
        lastModel: latestKeepaliveModel(activity, tags, id),
        activity: activityFor(activity, tags, id, checkin),
        error: redactAccount(row?.error || row?.lapsed || "", { accountId: user }, label),
      }
    }),
  }))
}

export async function readInventory({
  configHome = magpieConfigHome(),
  logPath = "",
  records = [],
  accountsData = null,
} = {}) {
  const [providers, checkins, quotas, histories, auth] = await Promise.all([
    readJson(join(configHome, "plugin-providers.json"), []),
    readJson(join(configHome, "plugin-checkin.json"), {}),
    readJson(join(configHome, "quotas.json"), {}),
    readJson(join(configHome, "quota-history.json"), {}),
    readJson(join(configHome, "plugin-auth.json"), {}),
  ])
  const cards = quotaByAccount(quotas)
  const history = historyByAccount(histories)
  const activity = activityIndex(records)
  const sourceProviders = Array.isArray(providers) ? providers : []
  const accountRows = Array.isArray(accountsData) ? accountsData : []
  const list = sourceProviders
    .filter((provider) => object(provider) && Array.isArray(provider.accounts) && provider.accounts.length > 0)
    .map((provider) => providerRow(provider, checkinByKey(checkins), cards, history, accountRows, activity, records, auth))
  list.push(...builtinProviderRows(accountRows, list, checkinByKey(checkins), cards, history, activity))
  return {
    generatedAt: new Date().toISOString(),
    source: {
      providers: "plugin-providers.json",
      checkins: "plugin-checkin.json",
      quotas: "quotas.json",
      history: "quota-history.json",
      log: logPath ? "daily-keepalive.log.jsonl" : "",
    },
    providers: list,
    accounts: list.flatMap((provider) =>
      provider.accounts.map((account) => ({
        provider: provider.id,
        providerName: provider.name,
        ...account,
      })),
    ),
    records,
  }
}
