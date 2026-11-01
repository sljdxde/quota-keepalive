(function exposeQuotaFormat(global) {
  const number = (value) => {
    if (value === null || value === undefined || value === "") return null
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

  const clampPercent = (value) => Math.max(0, Math.min(100, number(value)))

  function quotaLevel(percent) {
    if (percent >= 100) return "exhausted"
    if (percent >= 85) return "warning"
    return "normal"
  }

  function meterItem(name, unit, used, total, shownPercent) {
    const safeTotal = Math.max(0, total)
    const safeUsed = Math.max(0, used)
    const remaining = Math.max(0, safeTotal - safeUsed)
    const countedPercent = safeTotal > 0 ? (safeUsed / safeTotal) * 100 : 0
    const percent = shownPercent !== null && Math.abs(shownPercent - countedPercent) < 0.5
      ? shownPercent
      : countedPercent
    return {
      kind: "meter",
      name,
      unit,
      remaining,
      total: safeTotal,
      used: safeUsed,
      percent,
      progress: clampPercent(percent),
      level: quotaLevel(percent),
    }
  }

  function windowItem(window) {
    const name = String(window?.name || "额度").trim()
    const unit = String(window?.unit || "").trim()
    const limit = number(window?.limit)
    const amount = number(window?.amount)
    const remaining = number(window?.remaining)
    const usedPercent = number(window?.used)
    const display = typeof window?.display === "string" ? window.display.trim() : ""
    if (limit !== null && limit > 0) {
      if (amount !== null) {
        return meterItem(name, unit, amount, limit, usedPercent)
      }
      if (remaining !== null) {
        const sum = usedPercent !== null ? usedPercent + remaining : null
        const left = sum !== null && Math.abs(sum - 100) < 0.5 && Math.abs(sum - limit) >= 0.5
          ? (limit * remaining) / 100
          : remaining
        const clampedLeft = Math.max(0, Math.min(limit, left))
        return meterItem(name, unit, limit - clampedLeft, limit, usedPercent)
      }
      if (usedPercent !== null) {
        return meterItem(name, unit, (limit * Math.max(0, usedPercent)) / 100, limit, usedPercent)
      }
    }
    const balance = number(window?.balance)
    if (balance !== null) {
      return {
        kind: "balance",
        name,
        text: `余额 ${formatNumber(balance)}${unit ? ` ${unit}` : ""}`,
      }
    }
    if (usedPercent !== null) {
      return {
        kind: "percent",
        name,
        text: `已用 ${formatNumber(usedPercent)}%`,
        detail: display ? `供应商显示 ${display}` : "",
      }
    }
    if (display) {
      return { kind: "display", name, text: display }
    }
    return null
  }

  function itemText(item) {
    if (item.kind === "meter") {
      const unit = item.unit ? ` ${item.unit}` : ""
      return [
        item.name,
        `剩余 ${formatNumber(item.remaining)} / ${formatNumber(item.total)}${unit}`,
        `已用 ${formatNumber(item.used)}${unit} · ${formatNumber(item.percent)}%`,
      ].join(" · ")
    }
    const text = item.kind === "display"
      ? `${item.name} · 供应商显示 ${item.text}`
      : `${item.name} · ${item.text}`
    return item.detail ? `${text} · ${item.detail}` : text
  }

  function quotaItems(account) {
    const windows = Array.isArray(account?.quota?.windows) ? account.quota.windows : []
    const shown = windows
      .slice(0, 2)
      .map(windowItem)
      .filter(Boolean)
    if (shown.length) return shown

    const balance = typeof account?.quota?.balance === "string"
      ? account.quota.balance.trim()
      : ""
    if (balance) return [{ kind: "balance", name: "额度", text: `余额 ${balance}` }]
    return [{ kind: "pending", name: "额度", text: "额度待检测" }]
  }

  function quotaView(account) {
    return quotaItems(account).map(itemText).join(" · ")
  }

  const api = Object.freeze({ number, formatNumber, quotaItems, quotaView })
  global.QuotaKeepaliveFormat = api
})(globalThis)
