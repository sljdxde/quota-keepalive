import { errorText, finiteNumber, sanitize, text, truthy } from "./common.js"

const ORIGIN = "https://copilot.tencent.com"
const STATUS_PATH = "/v2/billing/meter/checkin-activity-status"
const CLAIM_PATH = "/v2/billing/meter/daily-checkin"
const SUMMARY_PATH = "/billing/meter/get-user-resource-summary"

export const CHECKIN_CODE = Object.freeze({
  already: 1001,
  alreadyToday: 10001,
  ineligible: 1002,
  ended: 1003,
})

export class WorkBuddyReplyError extends Error {
  constructor(message, { status = 0, code = 0, body = "" } = {}) {
    super(message)
    this.name = "WorkBuddyReplyError"
    this.status = status
    this.code = code
    this.body = body
  }
}

function parseJson(raw) {
  try {
    return JSON.parse(raw)
  } catch {
    return null
  }
}

function plainObject(value) {
  return !!value && typeof value === "object" && !Array.isArray(value)
}

function hasOwn(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key)
}

function vendorMessage(value) {
  if (!value || typeof value !== "object") return ""
  const data = value.error && typeof value.error === "object" ? value.error : value
  return text(data.message || data.msg || data.error?.message || value.msg || value.message)
}

async function jsonCall(fetchImpl, path, timeoutMs) {
  const response = await fetchImpl(`${ORIGIN}${path}`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      accept: "application/json",
    },
    body: "{}",
    signal: AbortSignal.timeout(timeoutMs),
  })
  const raw = await response.text()
  const body = parseJson(raw)
  const code = finiteNumber(body?.code, 0)
  if (!response.ok) {
    const said = vendorMessage(body) || raw || response.statusText
    throw new WorkBuddyReplyError(
      `WorkBuddy answered HTTP ${response.status}${said ? `: ${sanitize(said, 260)}` : ""}`,
      { status: response.status, code, body: raw },
    )
  }
  if (!plainObject(body) ||
      !Number.isFinite(Number(body.code ?? 0))) {
    throw new WorkBuddyReplyError("WorkBuddy's check-in answered with unreadable JSON", {
      status: response.status,
      body: raw,
    })
  }
  if (code !== 0) {
    const said = vendorMessage(body)
    throw new WorkBuddyReplyError(
      `WorkBuddy refused the check-in${said ? `: ${sanitize(said, 260)}` : ` (code ${code})`}`,
      { status: response.status, code, body: raw },
    )
  }
  if (!plainObject(body.data)) {
    throw new WorkBuddyReplyError("WorkBuddy's check-in answered without readable data", {
      status: response.status,
      code,
      body: raw,
    })
  }
  return body.data
}

async function accountReadable(fetchImpl, timeoutMs) {
  try {
    const response = await fetchImpl(`${ORIGIN}${SUMMARY_PATH}`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        accept: "application/json",
      },
      body: "{}",
      signal: AbortSignal.timeout(timeoutMs),
    })
    const raw = await response.text()
    if (!response.ok) return false
    const body = parseJson(raw)
    return plainObject(body) && (body.code == null || body.code === 0)
  } catch {
    return false
  }
}

function outcomeForCode(code, fallbackMessage = "") {
  switch (code) {
    case CHECKIN_CODE.already:
    case CHECKIN_CODE.alreadyToday:
      return { outcome: "done", credit: 0, streak: 0, message: fallbackMessage }
    case CHECKIN_CODE.ineligible:
      return { outcome: "ineligible", credit: 0, streak: 0, message: fallbackMessage }
    case CHECKIN_CODE.ended:
      return { outcome: "inactive", credit: 0, streak: 0, message: fallbackMessage }
    default:
      return null
  }
}

async function refused(error, fetchImpl, timeoutMs) {
  if (error instanceof WorkBuddyReplyError && error.status === 401) {
    if (await accountReadable(fetchImpl, timeoutMs)) {
      return {
        outcome: "ineligible",
        credit: 0,
        streak: 0,
        message:
          "WorkBuddy accepts its daily check-in only in its own app (the web API wants a desktop device token); check in from WorkBuddy.",
      }
    }
    return {
      outcome: "failed",
      credit: 0,
      streak: 0,
      message: "WorkBuddy refused this account's sign-in (401); sign in again.",
    }
  }
  if (error instanceof WorkBuddyReplyError) {
    const known = outcomeForCode(error.code, error.message)
    if (known) return known
  }
  return {
    outcome: "failed",
    credit: 0,
    streak: 0,
    message: errorText(error),
  }
}

export async function checkinWorkBuddy({
  fetchImpl,
  timeoutMs = 30_000,
}) {
  if (typeof fetchImpl !== "function") {
    throw new Error("WorkBuddy's signed fetch is unavailable")
  }

  let status
  try {
    status = await jsonCall(fetchImpl, STATUS_PATH, timeoutMs)
  } catch (error) {
    return refused(error, fetchImpl, timeoutMs)
  }

  if (!truthy(status.active)) {
    return { outcome: "inactive", credit: 0, streak: 0, message: "" }
  }
  const statusChecked = truthy(status.today_checked_in ?? status.todayCheckedIn)
  const statusCredit = finiteNumber(status.today_credit ?? status.todayCredit)
  const statusStreak = Math.round(
    finiteNumber(status.streak_days ?? status.streakDays),
  )

  // The activity status can disagree with the app; confirm with the
  // idempotent claim endpoint even when it already says today is checked in.
  try {
    const claim = await jsonCall(fetchImpl, CLAIM_PATH, timeoutMs)
    const confirmed = [
      "credit",
      "streak_days",
      "streakDays",
      "is_streak_day",
      "isStreakDay",
    ].some((key) => hasOwn(claim, key))
    if (!confirmed) {
      throw new WorkBuddyReplyError(
        "WorkBuddy's claim response did not confirm a credit or streak",
      )
    }
    const credit = finiteNumber(claim.credit)
    const streak = Math.round(
      finiteNumber(claim.streak_days ?? claim.streakDays),
    )
    return {
      outcome: statusChecked && credit <= 0 ? "done" : "claimed",
      credit: credit > 0 ? credit : statusChecked ? statusCredit : 0,
      streak: streak > 0 ? streak : statusChecked ? statusStreak : 0,
      message: sanitize(text(claim.message), 300),
    }
  } catch (error) {
    const result = await refused(error, fetchImpl, timeoutMs)
    if (result.outcome === "done") {
      result.credit = statusCredit
      result.streak = statusStreak
    }
    return result
  }
}

