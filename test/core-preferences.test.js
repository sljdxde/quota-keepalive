import { describe, expect, test } from "bun:test"
import { accountTag } from "../src/log.js"
import {
  TASK_NAMES,
  normalizeAccountPreferences,
  normalizeProviderPreferences,
  providerEnabled,
  taskControlId,
  taskEnabled,
} from "../src/preferences.js"

describe("task preferences", () => {
  test("normalizes only explicit off switches", () => {
    expect(normalizeProviderPreferences(null)).toEqual({})
    expect(normalizeProviderPreferences([])).toEqual({})
    expect(normalizeProviderPreferences({
      " workbuddy ": { enabled: false },
      keep: { enabled: true },
      bad: null,
      "": { enabled: false },
    })).toEqual({ workbuddy: { enabled: false } })
    expect(normalizeAccountPreferences({
      " acct-1 ": { checkin: false, keepalive: true, other: false },
      both: { checkin: false, keepalive: false },
      empty: { checkin: true },
      bad: "off",
    })).toEqual({
      "acct-1": { checkin: false },
      both: { checkin: false, keepalive: false },
    })
  })

  test("builds stable controls and applies provider/account defaults", () => {
    expect(TASK_NAMES).toEqual(["checkin", "keepalive"])
    expect(taskControlId("workbuddy", { key: "k" })).toBe(accountTag("workbuddy|k"))
    expect(taskControlId("workbuddy", { accountId: "a" }, 4)).toBe(accountTag("workbuddy|a"))
    expect(taskControlId("workbuddy", {}, 4)).toBe(accountTag("workbuddy|account-5"))
    expect(providerEnabled({}, "workbuddy")).toBe(true)
    expect(providerEnabled({ providerPreferences: { workbuddy: { enabled: false } } }, "workbuddy")).toBe(false)
    expect(taskEnabled({}, "workbuddy", "acct-1", "checkin")).toBe(true)
    expect(taskEnabled({}, "workbuddy", "acct-1", "other")).toBe(false)
    expect(taskEnabled({
      providerPreferences: { workbuddy: { enabled: false } },
      accountPreferences: { "acct-1": { checkin: false } },
    }, "workbuddy", "acct-1", "checkin")).toBe(false)
    expect(taskEnabled({
      accountPreferences: { "acct-1": { keepalive: false } },
    }, "workbuddy", "acct-1", "keepalive")).toBe(false)
  })
})
