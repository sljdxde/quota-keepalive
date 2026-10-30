(function exposeQuotaFormat(global) {
  const number = (value) => {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : null
  }

  const formatNumber = (value) => {
    const parsed = number(value)
    if (parsed === null) return ""
    return Math.abs(parsed - Math.round(parsed)) < 0.001
      ? String(Math.round(parsed))
      : parsed.toFixed(2).replace(/\.?0+$/, "")
  }

  function windowValue(window) {
    const limit = number(window?.limit)
    const amount = number(window?.amount)
    if (limit !== null && limit > 0 && amount !== null) {
      return `${formatNumber(Math.max(0, limit - amount))} / ${formatNumber(limit)}`
    }
    const balance = number(window?.balance)
    if (balance !== null) return formatNumber(balance)
    const display = typeof window?.display === "string" ? window.display.trim() : ""
    if (display) {
      const used = number(window?.used)
      const remaining = number(window?.remaining)
      const shown = /^(-?\d+(?:\.\d+)?)\s*\/\s*(-?\d+(?:\.\d+)?)/.exec(display)
      if (shown && used !== null && remaining !== null) {
        const shownUsed = Number(shown[1])
        const shownTotal = Number(shown[2])
        const total = limit !== null && limit > 0 ? limit : used + remaining
        if (
          Math.abs(shownUsed - used) < 0.001 &&
          Math.abs(shownTotal - total) < 0.001 &&
          total > 0 &&
          remaining >= 0
        ) {
          return `${formatNumber(remaining)} / ${formatNumber(total)}`
        }
      }
      return display
    }
    return ""
  }

  function quotaView(account) {
    const windows = Array.isArray(account?.quota?.windows) ? account.quota.windows : []
    const shown = windows.slice(0, 2).map((window) => {
      const value = windowValue(window)
      if (!value) return ""
      const name = String(window?.name || "").trim()
      const unit = String(window?.unit || "").trim()
      return [name, value, unit].filter(Boolean).join(" ")
    }).filter(Boolean)
    if (shown.length) return shown.join(" · ")

    const balance = typeof account?.quota?.balance === "string"
      ? account.quota.balance.trim()
      : ""
    if (balance) return `余额 ${balance}`
    return "额度待检测"
  }

  const api = Object.freeze({ number, formatNumber, quotaView })
  global.QuotaKeepaliveFormat = api
})(globalThis)
