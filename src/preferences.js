import { text } from "./common.js"
import { accountTag } from "./log.js"

export const TASK_NAMES = Object.freeze(["checkin", "keepalive"])

function object(value) {
  return !!value && typeof value === "object" && !Array.isArray(value)
}

function key(value, limit = 80) {
  return text(value).trim().slice(0, limit)
}

export function normalizeProviderPreferences(value) {
  if (!object(value)) return {}
  const out = {}
  for (const [provider, preference] of Object.entries(value)) {
    const id = key(provider)
    if (!id || !object(preference) || preference.enabled !== false) continue
    out[id] = { enabled: false }
  }
  return out
}

export function normalizeAccountPreferences(value) {
  if (!object(value)) return {}
  const out = {}
  for (const [controlId, preference] of Object.entries(value)) {
    const id = key(controlId, 80)
    if (!id || !object(preference)) continue
    const next = {}
    for (const task of TASK_NAMES) {
      if (preference[task] === false) next[task] = false
    }
    if (Object.keys(next).length) out[id] = next
  }
  return out
}

export function taskControlId(provider, account, index = 0) {
  const providerId = key(provider)
  const accountId = key(account?.key) || key(account?.accountId) || `account-${index + 1}`
  return accountTag(`${providerId}|${accountId}`)
}

export function providerEnabled(preferences, provider) {
  return preferences?.providerPreferences?.[key(provider)]?.enabled !== false
}

export function taskEnabled(preferences, provider, controlId, task) {
  if (!providerEnabled(preferences, provider)) return false
  if (!TASK_NAMES.includes(task)) return false
  return preferences?.accountPreferences?.[key(controlId)]?.[task] !== false
}
