import { describe, expect, test } from "bun:test"
import "../ui/quota-format.js"

const { quotaView } = globalThis.QuotaKeepaliveFormat

describe("quota display", () => {
  test("shows available divided by total instead of used divided by total", () => {
    expect(quotaView({
      quota: { windows: [{ name: "Credits", amount: 100, limit: 100 }] },
    })).toBe("Credits 0 / 100")
    expect(quotaView({
      quota: { windows: [{ name: "Credits", amount: 3.14, limit: 4700, unit: "credits" }] },
    })).toBe("Credits 4696.86 / 4700 credits")
  })

  test("clamps overspent windows at zero and limits the display to two windows", () => {
    expect(quotaView({
      quota: {
        windows: [
          { name: "Daily", amount: 12, limit: 10 },
          { name: "Weekly", amount: 2, limit: 20 },
          { name: "Monthly", amount: 1, limit: 30 },
        ],
      },
    })).toBe("Daily 0 / 10 · Weekly 18 / 20")
  })

  test("falls back to balance, vendor display, then an explicit empty state", () => {
    expect(quotaView({
      quota: { windows: [{ name: "Wallet", balance: 25, unit: "points" }] },
    })).toBe("Wallet 25 points")
    expect(quotaView({
      quota: { windows: [{ name: "Credits", display: "1.2k / 3k" }] },
    })).toBe("Credits 1.2k / 3k")
    expect(quotaView({ quota: { balance: "88 credits" } })).toBe("余额 88 credits")
    expect(quotaView({})).toBe("额度待检测")
  })

  test("converts a vendor used/total display to available/total only when the numbers agree", () => {
    expect(quotaView({
      quota: {
        windows: [{
          name: "Credits",
          used: 100,
          remaining: 0,
          display: "100 / 100",
          unit: "credits",
        }],
      },
    })).toBe("Credits 0 / 100 credits")
    expect(quotaView({
      quota: {
        windows: [{
          name: "Credits",
          used: 20,
          remaining: 80,
          display: "80 / 100",
        }],
      },
    })).toBe("Credits 80 / 100")
    expect(quotaView({
      quota: {
        windows: [{
          name: "Credits",
          used: 20,
          remaining: 80,
          display: "20 / 99",
        }],
      },
    })).toBe("Credits 20 / 99")
  })

  test("does not reinterpret percent-style used or remaining as a count", () => {
    expect(quotaView({
      quota: { windows: [{ name: "Rate", used: 20, remaining: 80, limit: 100 }] },
    })).toBe("额度待检测")
  })
})
