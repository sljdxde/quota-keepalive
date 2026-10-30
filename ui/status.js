(function exposeQuotaStatus(global) {
  const TASK_NAMES = Object.freeze({
    checkin: "签到",
    keepalive: "保活",
  })
  const SUCCESS_OUTCOMES = Object.freeze({
    checkin: new Set(["claimed", "done", "already-done"]),
    keepalive: new Set(["sent", "done", "already-done"]),
  })
  const VISIBLE_LOG_EVENTS = new Set(["checkin", "keepalive", "all", "schedule"])

  const taskName = (task) => TASK_NAMES[task] || "任务"
  const visibleLogEvent = (event) => VISIBLE_LOG_EVENTS.has(String(event || ""))
  const messageOf = (point) => String(point?.message || point?.error || "").trim()
  const finite = (value) => {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : 0
  }
  const formatNumber = (value) => {
    const parsed = finite(value)
    return Math.abs(parsed - Math.round(parsed)) < 0.001
      ? String(Math.round(parsed))
      : parsed.toFixed(2).replace(/\.?0+$/, "")
  }

  function successful(point, task) {
    if (!point) return false
    if (point.success === true) return true
    return (SUCCESS_OUTCOMES[task] || new Set()).has(String(point.outcome || ""))
  }

  function result(tone, short, name, message) {
    const label = `${name} ${short}`
    return {
      tone,
      short,
      label,
      message,
      title: message ? `${label} · ${message}` : label,
    }
  }

  function activity(point, task) {
    const name = taskName(task)
    if (!point) return result("muted", "待执行", name, "")
    const message = messageOf(point)

    if (successful(point, task)) {
      const credit = task === "checkin" && finite(point.credit) > 0
        ? ` +${formatNumber(point.credit)}`
        : ""
      const short = String(point.outcome || "") === "already-done"
        ? "今日已完成"
        : task === "checkin" ? "已签到" : "已保活"
      return result("good", `${short}${credit}`, name, message)
    }

    const outcome = String(point.outcome || "")
    if (point.status === "failed") {
      if (outcome === "retry") return result("warn", "待重试", name, message)
      const short = outcome === "quota-exhausted"
        ? "额度不足"
        : outcome === "unavailable"
          ? "当前不可用"
          : outcome === "exhausted" ? "重试用尽" : "执行失败"
      return result("bad", short, name, message)
    }

    if (point.status === "skipped" || outcome) {
      const short = outcome === "inactive"
        ? "暂无活动"
        : outcome === "own-app"
          ? "请到 App 签到"
          : outcome === "captcha"
            ? "需要验证"
            : outcome === "busy"
              ? "稍后重试"
              : outcome === "ineligible" && /9074|client profile|device|设备|客户端/i.test(message)
                ? "设备受限"
                : outcome === "ineligible" ? "不符合条件" : "已跳过"
      return result("warn", short, name, message)
    }

    return result(point.status === "started" ? "muted" : "muted",
      point.status === "started" ? "执行中" : "状态未知", name, message)
  }

  function summary(points, task) {
    const list = Array.isArray(points) ? points : []
    const out = {
      total: list.length,
      success: 0,
      failed: 0,
      skipped: 0,
      pending: 0,
    }
    for (const point of list) {
      if (!point) out.pending++
      else if (successful(point, task)) out.success++
      else if (point.status === "failed") out.failed++
      else if (point.status === "skipped") out.skipped++
      else out.pending++
    }
    return out
  }

  function chip(points, task) {
    const name = taskName(task)
    const counts = summary(points, task)
    if (!counts.total) {
      return {
        tone: "muted",
        label: `${name} 已关闭`,
        detail: `${name}任务未启用`,
        title: `${name}任务未启用`,
        counts,
      }
    }
    const tone = counts.failed
      ? "bad"
      : counts.success === counts.total
        ? "good"
        : counts.skipped ? "warn" : "muted"
    const detail = [
      `${counts.success} 完成`,
      counts.failed ? `${counts.failed} 失败` : "",
      counts.skipped ? `${counts.skipped} 跳过` : "",
      counts.pending ? `${counts.pending} 待执行` : "",
    ].filter(Boolean).join("，")
    return {
      tone,
      label: `${name} ${counts.success}/${counts.total}`,
      detail,
      title: `${name}：${detail}`,
      counts,
    }
  }

  global.QuotaKeepaliveStatus = Object.freeze({
    activity,
    successful,
    summary,
    chip,
    taskName,
    visibleLogEvent,
  })
})(globalThis)
