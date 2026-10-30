import { homedir } from "node:os"
import { isAbsolute, join } from "node:path"
import { defaultCommandPath, defaultStatePath } from "./common.js"
import {
  DEFAULT_LOG_MAX_BYTES,
  MAX_LOG_MAX_BYTES,
  MIN_LOG_MAX_BYTES,
  defaultLogPath,
} from "./log.js"
import {
  normalizeAccountPreferences,
  normalizeProviderPreferences,
} from "./preferences.js"
import { defaultSnapshotPath } from "./snapshot.js"

export const DEFAULT_KEEPALIVE = Object.freeze({
  model: "",
  modelCandidates: 4,
  inputTokens: 25_000,
  maxAttempts: 3,
  scheduleEnabled: false,
  scheduleAction: "checkin",
  scheduleMinutes: 60,
  timeoutMs: 90_000,
  lockWaitMs: 2_000,
  watchOptions: true,
})

function integerIn(value, fallback, min, max) {
  const n = typeof value === "number" ? value : Number(value)
  return Number.isInteger(n) && n >= min && n <= max ? n : fallback
}

function modelPreferences(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {}
  const out = {}
  for (const [provider, model] of Object.entries(value)) {
    const name = typeof provider === "string" ? provider.trim() : ""
    const id = typeof model === "string" ? model.trim() : ""
    if (!name || !id) continue
    out[name.slice(0, 80)] = id.slice(0, 200)
  }
  return out
}

function absolutePath(value, fallback) {
  if (typeof value !== "string") return fallback
  const candidate = value.trim().startsWith("~/")
    ? join(homedir(), value.trim().slice(2))
    : value.trim()
  return candidate && isAbsolute(candidate) ? candidate : fallback
}

export function resolveKeepaliveOptions(raw) {
  const source = raw && typeof raw === "object" && !Array.isArray(raw) ? raw : {}
  const model = typeof source.model === "string" ? source.model.trim().slice(0, 200) : ""
  const scheduleAction = ["checkin", "keepalive", "all"].includes(source.scheduleAction)
    ? source.scheduleAction
    : DEFAULT_KEEPALIVE.scheduleAction
  return {
    model,
    modelPreferences: modelPreferences(source.modelPreferences),
    providerPreferences: normalizeProviderPreferences(source.providerPreferences),
    accountPreferences: normalizeAccountPreferences(source.accountPreferences),
    modelCandidates: integerIn(source.modelCandidates, DEFAULT_KEEPALIVE.modelCandidates, 1, 12),
    scheduleEnabled: source.scheduleEnabled === true,
    scheduleAction,
    scheduleMinutes: integerIn(source.scheduleMinutes, DEFAULT_KEEPALIVE.scheduleMinutes, 5, 10_080),
    configHome: absolutePath(source.configHome, undefined),
    inputTokens: integerIn(source.inputTokens, DEFAULT_KEEPALIVE.inputTokens, 1_000, 25_000),
    maxAttempts: integerIn(source.maxAttempts, DEFAULT_KEEPALIVE.maxAttempts, 1, 10),
    timeoutMs: integerIn(source.timeoutMs, DEFAULT_KEEPALIVE.timeoutMs, 10_000, 180_000),
    lockWaitMs: integerIn(source.lockWaitMs, DEFAULT_KEEPALIVE.lockWaitMs, 0, 10_000),
    watchOptions: source.watchOptions !== false,
    statePath: absolutePath(source.statePath, defaultStatePath()),
    commandPath: absolutePath(source.commandPath, defaultCommandPath()),
    logPath: absolutePath(source.logPath, defaultLogPath()),
    logMaxBytes: integerIn(
      source.logMaxBytes,
      DEFAULT_LOG_MAX_BYTES,
      MIN_LOG_MAX_BYTES,
      MAX_LOG_MAX_BYTES,
    ),
    snapshotPath: absolutePath(source.snapshotPath, defaultSnapshotPath()),
  }
}
