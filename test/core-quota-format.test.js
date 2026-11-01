import { describe, expect, test } from "bun:test"
import "../ui/quota-format.js"

const { quotaItems, quotaView } = globalThis.QuotaKeepaliveFormat

describe("quota display", () => {
  test("labels remaining and used amounts explicitly", () => {
    expect(quotaView({
      quota: { windows: [{ name: "Credits", amount: 100, limit: 100 }] },
    })).toBe("Credits · 剩余 0 / 100 · 已用 100 · 100%")
    expect(quotaView({
      quota: { windows: [{ name: "Credits", amount: 3.14, limit: 4700, unit: "credits" }] },
    })).toBe("Credits · 剩余 4696.86 / 4700 credits · 已用 3.14 credits · 0.07%")
  })

  test("returns structured meter details for the page", () => {
    expect(quotaItems({
      quota: {
        windows: [{ name: "Credits", amount: 425, limit: 500, unit: "credits" }],
      },
    })[0]).toEqual({
      kind: "meter",
      name: "Credits",
      unit: "credits",
      remaining: 75,
      total: 500,
      used: 425,
      percent: 85,
      progress: 85,
      level: "warning",
    })
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
    })).toBe("Daily · 剩余 0 / 10 · 已用 12 · 120% · Weekly · 剩余 18 / 20 · 已用 2 · 10%")
  })

  test("falls back to balance, vendor display, then an explicit empty state", () => {
    expect(quotaView({
      quota: { windows: [{ name: "Wallet", balance: 25, unit: "points" }] },
    })).toBe("Wallet · 余额 25 points")
    expect(quotaView({
      quota: { windows: [{ name: "Credits", display: "1.2k / 3k" }] },
    })).toBe("Credits · 供应商显示 1.2k / 3k")
    expect(quotaView({ quota: { balance: "88 credits" } })).toBe("额度 · 余额 88 credits")
    expect(quotaView({})).toBe("额度 · 额度待检测")
  })

  test("shows a percentage-only reading without guessing its remaining count", () => {
    const account = {
      quota: {
        windows: [{
          name: "Credits",
          used: 100,
          remaining: 0,
          unit: "credits",
        }],
      },
    }
    expect(quotaView(account)).toBe("Credits · 已用 100%")
    expect(quotaItems(account)[0]).toMatchObject({
      kind: "percent",
      percent: 100,
      progress: 100,
      level: "exhausted",
    })
  })

  test("restores Qoder used and total counts from its display text", () => {
    expect(quotaItems({
      quota: {
        windows: [{
          name: "Add-on credits",
          used: 100,
          display: "400 / 400 credits",
        }],
      },
    })[0]).toEqual({
      kind: "meter",
      name: "Add-on credits",
      unit: "credits",
      remaining: 0,
      total: 400,
      used: 400,
      percent: 100,
      progress: 100,
      level: "exhausted",
    })
  })

  test("distinguishes remaining/total display text from Qoder used/total text", () => {
    expect(quotaItems({
      quota: {
        windows: [{
          name: "Credits",
          used: 25,
          display: "300 / 400 credits",
        }],
      },
    })[0]).toMatchObject({
      kind: "meter",
      remaining: 300,
      total: 400,
      used: 100,
      unit: "credits",
      progress: 25,
    })
    expect(quotaItems({
      quota: {
        windows: [{
          name: "Credits",
          used: 100,
          display: "0 credits left",
        }],
      },
    })[0]).toMatchObject({
      kind: "percent",
      progress: 100,
      level: "exhausted",
    })
  })

  test("uses a supplied remaining count instead of treating it as a percentage", () => {
    expect(quotaView({
      quota: { windows: [{ name: "Rate", used: 20, remaining: 80, limit: 100 }] },
    })).toBe("Rate · 剩余 80 / 100 · 已用 20 · 20%")
  })

  test("derives remaining from a percentage and total when no count is supplied", () => {
    expect(quotaView({
      quota: { windows: [{ name: "Credits", used: 96, limit: 500, unit: "points" }] },
    })).toBe("Credits · 剩余 20 / 500 points · 已用 480 points · 96%")
    expect(quotaItems({
      quota: { windows: [{ name: "Credits", used: 100, limit: 500 }] },
    })[0].level).toBe("exhausted")
  })

  test("uses count data when a percentage reading contradicts it", () => {
    expect(quotaItems({
      quota: { windows: [{ name: "Credits", used: 20, amount: 100, limit: 100 }] },
    })[0]).toMatchObject({
      remaining: 0,
      used: 100,
      percent: 100,
      progress: 100,
      level: "exhausted",
    })
  })

  test("recognizes a percentage-style remaining value", () => {
    expect(quotaView({
      quota: { windows: [{ name: "Credits", used: 90, remaining: 10, limit: 500 }] },
    })).toBe("Credits · 剩余 50 / 500 · 已用 450 · 90%")
  })

  test("keeps malformed windows explicit instead of inventing a quota", () => {
    expect(quotaView({
      quota: {
        windows: [
          { name: "Credits", limit: 0 },
          { name: "Credits", amount: null, limit: 100 },
          null,
        ],
      },
    })).toBe("额度 · 额度待检测")
  })
})
