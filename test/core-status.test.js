import { describe, expect, test } from "bun:test"
import "../ui/status.js"

const status = globalThis.QuotaKeepaliveStatus

describe("page status labels", () => {
  test("labels successful check-ins, credits, and keepalives", () => {
    expect(status.successful(null, "checkin")).toBe(false)
    expect(status.successful({ success: true }, "checkin")).toBe(true)
    expect(status.successful({ outcome: "claimed" }, "checkin")).toBe(true)
    expect(status.successful({ outcome: "sent" }, "keepalive")).toBe(true)
    expect(status.successful({ outcome: "unknown" }, "checkin")).toBe(false)

    expect(status.activity({ status: "success", outcome: "claimed", credit: 100 }, "checkin")).toMatchObject({
      tone: "good",
      label: "签到 已签到 +100",
      short: "已签到 +100",
    })
    expect(status.activity({ status: "success", outcome: "claimed", credit: 12.5 }, "checkin").label).toBe(
      "签到 已签到 +12.5",
    )
    expect(status.activity({ status: "success", outcome: "already-done" }, "keepalive")).toMatchObject({
      tone: "good",
      label: "保活 今日已完成",
    })
    expect(status.activity({ status: "success", outcome: "sent", message: "message" }, "keepalive").title).toContain(
      "message",
    )
    expect(status.activity(null, "checkin")).toMatchObject({
      tone: "muted",
      label: "签到 待执行",
    })
    expect(status.activity(null, "custom").label).toBe("任务 待执行")
  })

  test("separates retryable, terminal, and unavailable failures", () => {
    expect(status.activity({ status: "failed", outcome: "retry" }, "keepalive")).toMatchObject({
      tone: "warn",
      label: "保活 待重试",
    })
    for (const [outcome, label] of [
      ["quota-exhausted", "保活 额度不足"],
      ["unavailable", "保活 当前不可用"],
      ["exhausted", "保活 重试用尽"],
      ["failed", "保活 执行失败"],
    ]) {
      expect(status.activity({ status: "failed", outcome }, "keepalive")).toMatchObject({
        tone: "bad",
        label,
      })
    }
  })

  test("turns provider skip reasons into specific account labels", () => {
    for (const [point, label] of [
      [{ status: "skipped", outcome: "inactive" }, "签到 暂无活动"],
      [{ status: "skipped", outcome: "own-app" }, "签到 请到 App 签到"],
      [{ status: "skipped", outcome: "captcha" }, "签到 需要验证"],
      [{ status: "skipped", outcome: "busy" }, "签到 稍后重试"],
      [{
        status: "skipped",
        outcome: "ineligible",
        message: "当前参与用户太多 (9074) — no Trae CN client profile",
      }, "签到 设备受限"],
      [{ status: "skipped", outcome: "ineligible", message: "not eligible" }, "签到 不符合条件"],
      [{ status: "skipped", outcome: "something-else" }, "签到 已跳过"],
      [{ status: "skipped" }, "签到 已跳过"],
      [{ status: "started" }, "签到 执行中"],
      [{ status: "queued" }, "签到 状态未知"],
    ]) {
      expect(status.activity(point, "checkin")).toMatchObject({
        tone: point.status === "started" || point.status === "queued" ? "muted" : "warn",
        label,
      })
    }
  })

  test("summarizes and colors provider task chips", () => {
    expect(status.summary(null, "checkin")).toEqual({
      total: 0,
      success: 0,
      failed: 0,
      skipped: 0,
      pending: 0,
    })
    expect(status.summary([
      null,
      { success: true },
      { status: "failed" },
      { status: "skipped" },
      { status: "started" },
    ], "checkin")).toEqual({
      total: 5,
      success: 1,
      failed: 1,
      skipped: 1,
      pending: 2,
    })

    expect(status.chip([], "checkin")).toMatchObject({
      tone: "muted",
      label: "签到 已关闭",
      detail: "签到任务未启用",
    })
    expect(status.chip([{ success: true }, { success: true }], "checkin")).toMatchObject({
      tone: "good",
      label: "签到 2/2",
      detail: "2 完成",
    })
    expect(status.chip([{ status: "failed" }, { success: true }], "keepalive")).toMatchObject({
      tone: "bad",
      label: "保活 1/2",
      detail: "1 完成，1 失败",
    })
    expect(status.chip([{ status: "skipped" }], "checkin")).toMatchObject({
      tone: "warn",
      label: "签到 0/1",
      detail: "0 完成，1 跳过",
    })
    expect(status.chip([null], "keepalive")).toMatchObject({
      tone: "muted",
      label: "保活 0/1",
      detail: "0 完成，1 待执行",
    })
    expect(status.chip([], "custom").label).toBe("任务 已关闭")
    expect(status.taskName("keepalive")).toBe("保活")
  })

  test("only exposes task events in the recent-runs list", () => {
    for (const event of ["checkin", "keepalive", "all", "schedule"]) {
      expect(status.visibleLogEvent(event)).toBe(true)
    }
    for (const event of ["detect", "refresh", "started", "finished", "error", ""]) {
      expect(status.visibleLogEvent(event)).toBe(false)
    }
  })

  test("keeps runs with outcomes and hides skipped-only runs", () => {
    expect(status.logHasResult({ status: "success" })).toBe(true)
    expect(status.logHasResult({ status: "failed" })).toBe(true)
    expect(status.logHasResult({ status: "skipped" })).toBe(false)
    expect(status.logHasResult({ status: "skipped", successes: 1 })).toBe(true)
    expect(status.logHasResult({ status: "skipped", failures: "1" })).toBe(true)
    expect(status.logHasResult({ status: "skipped", successes: 0, failures: 0 })).toBe(false)
    expect(status.logHasResult(null)).toBe(false)
  })
})
