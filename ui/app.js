(() => {
  const $ = (id) => document.getElementById(id)
  const KEEPALIVE_VISIBLE = false
  const ACTIONS = {
    detect: { title: "检测账号", detail: "正在读取 Magpie 当前已登录的供应商与账号。" },
    checkin: { title: "全部签到", detail: "正在让已有签到能力的供应商执行签到。" },
    keepalive: { title: "发送保活", detail: "正在为全部已适配供应商账号发送真实活跃对话；会消耗额度。" },
    refresh: { title: "刷新额度", detail: "正在刷新全部启用供应商的实时额度。" },
  }
  const EVENT_NAMES = {
    started: "开始",
    keepalive: "保活",
    "keepalive-model": "切换模型",
    checkin: "签到",
    schedule: "定时任务",
    finished: "完成",
    error: "异常",
    detect: "检测",
    all: "签到并保活",
    refresh: "刷新额度",
  }
  const OUTCOME_NAMES = {
    claimed: "已签到",
    done: "今日已完成",
    inactive: "暂无活动",
    ineligible: "不符合条件",
    sent: "保活已发送",
    "already-done": "今日已完成",
    retry: "将在稍后重试",
    exhausted: "已达重试上限",
    "quota-exhausted": "额度不足，今日已停止",
    busy: "另一任务正在执行",
    unavailable: "当前不可用",
    unsupported: "当前登录方式不支持",
    unidentified: "账号标识不可用",
    disabled: "保活未开启",
    failed: "执行失败",
  }
  const PROVIDER_ICON_FILES = {
    "workbuddy-ai": "provider-icons/workbuddy.svg",
    workbuddy: "provider-icons/workbuddy.svg",
    zcode: "provider-icons/zcode.png",
    qoder: "provider-icons/qoder.svg",
    "qoder-cn": "provider-icons/qoder.svg",
    qwenwork: "provider-icons/qoder.svg",
    "qwenwork-enterprise": "provider-icons/qoder.svg",
    "opencode-zen-free": "provider-icons/opencode.svg",
    "trae-cn": "provider-icons/trae.svg",
    "trae-global": "provider-icons/trae.svg",
    "minimax-code": "provider-icons/minimax.svg",
    "minimax-code-global": "provider-icons/minimax.svg",
    codearts: "provider-icons/codearts.svg",
  }
  let snapshot = window.DailyKeepaliveSnapshot || null
  let busy = false
  let activeConfirm = false
  let confirmRequest = null
  let refreshTimer = 0
  let refreshBusy = false
  let tick = 0
  const providerOpen = new Map()
  const expandedHistories = new Set()
  const expandedLogs = new Set()
  let modelPreferences = {}
  let providerPreferences = {}
  let accountPreferences = {}
  let pageOptions = null
  let optionsWriter = null

  const escapeHtml = (value) => String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;")

  const number = (value) => {
    const n = Number(value)
    return Number.isFinite(n) ? n : 0
  }

  const formatNumber = (value) => {
    const n = number(value)
    return Math.abs(n - Math.round(n)) < 0.001
      ? String(Math.round(n))
      : n.toFixed(2).replace(/\.?0+$/, "")
  }

  const formatTime = (value) => {
    const at = Date.parse(value)
    if (!Number.isFinite(at)) return ""
    return new Intl.DateTimeFormat("zh-CN", {
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
    }).format(at)
  }

  const relation = (value) => {
    const at = Date.parse(value)
    if (!Number.isFinite(at)) return ""
    const seconds = Math.max(0, Math.round((Date.now() - at) / 1000))
    if (seconds < 60) return "刚刚"
    if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`
    if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`
    return `${Math.floor(seconds / 86400)} 天前`
  }

  const modelRate = (model) => {
    if (model?.free === true) return "免费"
    const rate = Number(model?.rate)
    if (!Number.isFinite(rate) || rate <= 0) return ""
    return `倍率 ×${formatNumber(rate)}`
  }

  const modelTitle = (model) => {
    const name = String(model?.name || model?.id || "").trim()
    const rate = modelRate(model)
    return rate ? `${name} · ${rate}` : name
  }

  // Fuzzy matching: an exact substring wins; otherwise every query character
  // must appear in order, with adjacent matches scored higher.
  function fuzzyScore(query, value) {
    const needle = String(query || "").trim().toLocaleLowerCase()
    const haystack = String(value || "").toLocaleLowerCase()
    if (!needle) return 1
    const exact = haystack.indexOf(needle)
    if (exact >= 0) return 1_000 - exact
    let cursor = 0
    let score = 0
    let previous = -2
    for (const character of needle) {
      while (cursor < haystack.length && haystack[cursor] !== character) cursor++
      if (cursor >= haystack.length) return -1
      score += previous === cursor - 1 ? 8 : 2
      previous = cursor
      cursor++
    }
    return score
  }

  function modelsForProvider(provider) {
    return (Array.isArray(provider?.models) ? provider.models : [])
      .filter((model) => model && typeof model.id === "string" && model.id)
      .slice()
  }

  function providerFor(id) {
    return (Array.isArray(snapshot?.providers) ? snapshot.providers : [])
      .find((provider) => String(provider?.id || "") === String(id || ""))
  }

  function selectedModel(provider) {
    const wanted = modelPreferences[String(provider?.id || "")]
    if (!wanted) return null
    return modelsForProvider(provider).find((model) => model.id === wanted) || null
  }

  function modelName(provider, modelId) {
    const id = String(modelId || "").trim()
    if (!id) return ""
    const model = modelsForProvider(provider).find((item) => item.id === id)
    return String(model?.name || id).trim()
  }

  function providerOn(provider) {
    return providerPreferences[String(provider?.id || "")]?.enabled !== false
  }

  function taskOn(provider, account, task) {
    if (!providerOn(provider)) return false
    return accountPreferences[String(account?.controlId || "")]?.[task] !== false
  }

  const statusClass = (status) => ({
    success: "good",
    failed: "bad",
    skipped: "warn",
    started: "muted",
  })[status] || "muted"

  const statusApi = window.QuotaKeepaliveStatus || {}
  const visibleLogEvent = statusApi.visibleLogEvent ||
    ((event) => new Set(["checkin", "keepalive", "all", "schedule"]).has(String(event || "")))
  const activityState = statusApi.activity || ((point, task) => {
    const name = task === "checkin" ? "签到" : "保活"
    if (!point) return { tone: "muted", label: `${name}待执行`, short: "待执行", message: "", title: "" }
    if (point.success) return {
      tone: "good",
      label: `${name}${task === "checkin" ? "已签到" : "已保活"}`,
      short: task === "checkin" ? "已签到" : "已保活",
      message: point.message || "",
      title: point.message || "",
    }
    return {
      tone: statusClass(point.status),
      label: `${name}${point.status === "failed" ? "失败" : point.status === "skipped" ? "已跳过" : "待执行"}`,
      short: point.status === "failed" ? "失败" : point.status === "skipped" ? "已跳过" : "待执行",
      message: point.message || "",
      title: point.message || "",
    }
  })
  const activitySummary = statusApi.summary || ((points) => {
    const list = Array.isArray(points) ? points : []
    return {
      total: list.length,
      success: list.filter((point) => point?.success).length,
      failed: list.filter((point) => point?.status === "failed").length,
      skipped: list.filter((point) => point?.status === "skipped").length,
      pending: list.filter((point) => !point).length,
    }
  })
  const taskChip = statusApi.chip || ((points, task) => {
    const counts = activitySummary(points, task)
    return {
      tone: counts.failed ? "bad" : counts.success === counts.total && counts.total ? "good" : "muted",
      label: `${task === "checkin" ? "签到" : "保活"} ${counts.success}/${counts.total}`,
      title: "",
      counts,
    }
  })

  const formatDay = (value) => {
    const match = /^\d{4}-(\d{2})-(\d{2})$/.exec(String(value || ""))
    return match ? `${match[1]}/${match[2]}` : String(value || "")
  }

  const pointCredit = (point) => point?.credit > 0 ? `+${formatNumber(point.credit)}` : ""

  function todayPoint(account, event) {
    const current = account?.activity?.today
    if (current?.[event]) return current[event]
    if (event === "checkin" && account?.checkin?.day === snapshot?.today) {
      return {
        status: ["claimed", "done"].includes(account.checkin.outcome) ? "success" : account.checkin.status,
        outcome: account.checkin.outcome,
        success: ["claimed", "done"].includes(account.checkin.outcome),
        credit: number(account.checkin.credit),
        streak: number(account.checkin.streak),
        message: account.checkin.message || "",
      }
    }
    return null
  }

  function activityDays(account) {
    const days = Array.isArray(account?.activity?.days)
      ? account.activity.days
      : []
    if (days.length) return days.slice(-5)
    const checkin = account?.checkin?.day ? {
      day: account.checkin.day,
      checkin: todayPoint(account, "checkin"),
      keepalive: null,
    } : null
    return checkin ? [checkin] : []
  }

  function shortMessage(value, limit = 220) {
    const line = String(value || "").replace(/\s+/g, " ").trim()
    return line.length > limit ? `${line.slice(0, limit - 1)}…` : line
  }

  function accountNotes(account, provider) {
    const notes = []
    for (const task of KEEPALIVE_VISIBLE ? ["checkin", "keepalive"] : ["checkin"]) {
      if (!provider?.[`${task}Supported`]) continue
      const point = todayPoint(account, task)
      if (!point || point.success) continue
      const state = activityState(point, task)
      notes.push({
        tone: state.tone,
        label: state.label,
        message: shortMessage(state.message || point.message || ""),
        title: state.title || point.message || "",
      })
    }
    if (account?.error) {
      const message = shortMessage(account.error)
      notes.push({
        tone: "bad",
        label: "额度读取失败",
        message,
        title: account.error,
      })
    }
    return notes.map((note) => `<p class="account-note ${escapeHtml(note.tone)}" title="${escapeHtml(note.title)}">
      <b>${escapeHtml(note.label)}</b>
      ${note.message ? `<span>${escapeHtml(note.message)}</span>` : ""}
    </p>`).join("")
  }

  function accountHistory(account, provider, index) {
    const days = activityDays(account)
    if (!days.length) return ""
    if (!days.some((day) => day?.checkin || (KEEPALIVE_VISIBLE && day?.keepalive))) return ""
    const summary = account?.activity?.summary || {}
    const key = `${provider?.id || ""}|${account?.keyTag || ""}|${index}`
    return `<details class="account-history" data-history-key="${escapeHtml(key)}"${
      expandedHistories.has(key) ? " open" : ""
    }>
      <summary>
        <span>近 5 天</span>
        <small>签到 ${number(summary.checkinDays)}/${number(summary.days) || days.length}${
          KEEPALIVE_VISIBLE ? ` · 保活 ${number(summary.keepaliveDays)}/${
            number(summary.days) || days.length
          }` : ""
        }</small>
        <span class="history-chevron" aria-hidden="true">›</span>
      </summary>
      <div class="history-days">
        ${days.slice().reverse().map((day) => {
          const checkin = day?.checkin
          const keepalive = day?.keepalive
          const chips = []
          if (checkin?.success) {
            chips.push(`<span class="history-chip good">签到${pointCredit(checkin) ? ` ${pointCredit(checkin)}` : ""}</span>`)
          } else if (checkin?.status === "failed") {
            chips.push(`<span class="history-chip bad">签到失败</span>`)
          } else if (checkin) {
            chips.push(`<span class="history-chip muted">未签到</span>`)
          }
          if (KEEPALIVE_VISIBLE && keepalive?.success) {
            chips.push(`<span class="history-chip good">保活</span>`)
          } else if (KEEPALIVE_VISIBLE && keepalive?.status === "failed") {
            chips.push(`<span class="history-chip bad">保活失败</span>`)
          } else if (KEEPALIVE_VISIBLE && keepalive) {
            chips.push(`<span class="history-chip muted">未保活</span>`)
          }
          return `<div class="history-day">
            <time>${escapeHtml(formatDay(day?.day))}</time>
            <span class="history-results">${chips.join("") || `<span class="history-chip muted">无记录</span>`}</span>
          </div>`
        }).join("")}
      </div>
    </details>`
  }

  function stat(label, value, detail, tone = "") {
    return `<article class="stat ${tone}">
      <span>${escapeHtml(label)}</span>
      <strong>${escapeHtml(value)}</strong>
      <small>${escapeHtml(detail)}</small>
    </article>`
  }

  function renderSummary(data) {
    const today = data?.summary?.today || {}
    const accounts = number(today.accounts)
    const checkinDone = number(today.checkinDone)
    const checkinSuccess = number(today.checkin?.success)
    const checkinSkipped = number(today.checkin?.skipped)
    const checkinFailed = number(today.checkin?.failed)
    const failed = checkinFailed
    const skipped = checkinSkipped
    const taskDetail = (success, skippedCount, failedCount) => [
      `${success} 成功`,
      skippedCount ? `${skippedCount} 跳过` : "",
      failedCount ? `${failedCount} 失败` : "",
    ].filter(Boolean).join(" · ")
    $("summary").innerHTML = [
      stat("已登录账号", String(accounts), `${number(data?.providers?.length)} 个供应商`),
      stat(
        "今日签到",
        String(checkinDone),
        checkinDone || checkinSkipped || checkinFailed
          ? taskDetail(checkinSuccess, checkinSkipped, checkinFailed)
          : "暂无签到记录",
        checkinFailed ? "bad" : checkinSkipped ? "warn" : "",
      ),
      stat(
        "需要处理",
        String(failed),
        failed ? `签到 ${checkinFailed}` : skipped ? `暂无失败 · ${skipped} 项跳过` : "暂无失败",
        failed ? "bad" : "",
      ),
    ].join("")
  }

  const quotaItems = window.QuotaKeepaliveFormat?.quotaItems || (() => [{
    kind: "pending",
    name: "额度",
    text: "额度待检测",
  }])

  function quotaTone(account) {
    const history = Array.isArray(account?.history) ? account.history : []
    if (history.length < 2) return ""
    const delta = number(history.at(-1)?.left) - number(history.at(-2)?.left)
    return delta < 0 ? "down" : delta > 0 ? "up" : ""
  }

  function quotaSparkline(account) {
    const history = (Array.isArray(account?.history) ? account.history : [])
      .filter((point) => Number.isFinite(Number(point?.left)))
      .slice(-20)
    if (history.length < 2) return ""
    const values = history.map((point) => number(point.left))
    const min = Math.min(...values)
    const max = Math.max(...values)
    const range = max - min || 1
    const points = values.map((value, index) => {
      const x = (index / (values.length - 1)) * 72
      const y = 18 - ((value - min) / range) * 14
      return `${x.toFixed(1)},${y.toFixed(1)}`
    }).join(" ")
    return `<svg class="quota-spark ${quotaTone(account)}" viewBox="0 0 72 20"
      preserveAspectRatio="none" role="img" aria-label="最近额度变化"><polyline points="${points}"></polyline></svg>`
  }

  function quotaItemHtml(item) {
    if (item.kind === "meter") {
      const unit = item.unit ? ` ${item.unit}` : ""
      const progress = Math.max(0, Math.min(100, number(item.progress)))
      const label = `${item.name}已用 ${formatNumber(item.percent)}%`
      return `<div class="quota-window">
        <div class="quota-window-head">
          <span class="quota-window-name">${escapeHtml(item.name)}</span>
          <span class="quota-window-value"><b>剩余 ${escapeHtml(formatNumber(item.remaining))}</b> / ${
            escapeHtml(formatNumber(item.total))
          }${escapeHtml(unit)}</span>
        </div>
        <div class="quota-meter ${escapeHtml(item.level)}" role="progressbar"
          aria-label="${escapeHtml(label)}" aria-valuemin="0" aria-valuemax="100"
          aria-valuenow="${escapeHtml(formatNumber(progress))}">
          <i style="width:${progress}%"></i>
        </div>
        <div class="quota-window-foot">
          <span>已用 ${escapeHtml(formatNumber(item.used))}${escapeHtml(unit)}</span>
          <span>${escapeHtml(formatNumber(item.percent))}%</span>
        </div>
      </div>`
    }
    const detail = item.kind === "display"
      ? "供应商原样显示，不推断已用或剩余"
      : item.detail || ""
    return `<div class="quota-window quota-window-plain">
      <div class="quota-window-head">
        <span class="quota-window-name">${escapeHtml(item.name)}</span>
        <span class="quota-window-value">${escapeHtml(item.text)}</span>
      </div>
      ${detail ? `<div class="quota-window-foot">${escapeHtml(detail)}</div>` : ""}
    </div>`
  }

  function accountQuota(account) {
    const items = quotaItems(account)
    return `<div class="account-quota ${quotaTone(account)}" aria-label="额度信息">
      <div class="account-quota-head">
        <span class="account-quota-title"><b>额度</b><span>进度条 = 已用比例</span></span>
        ${quotaSparkline(account)}
      </div>
      <div class="account-quota-windows">${items.map(quotaItemHtml).join("")}</div>
    </div>`
  }

  function accountRow(account, provider, index) {
    const latest = account?.latest
    const actualModel = String(
      account?.lastModel || account?.activity?.today?.keepalive?.model || "",
    ).trim()
    const chosen = selectedModel(provider)
    const automatic = provider?.defaultModel || provider?.cheapestModel
    const modelLabel = KEEPALIVE_VISIBLE && provider?.keepaliveSupported
      ? actualModel
        ? `实际模型 ${modelName(provider, actualModel)}`
        : chosen
          ? `所选模型 ${chosen.name || chosen.id}`
          : automatic
            ? `自动首选 ${automatic.name || automatic.id}`
            : "模型待选择"
      : ""
    const taskRows = [
      provider.checkinSupported ? taskRow(provider, account, "checkin") : "",
      KEEPALIVE_VISIBLE && provider.keepaliveSupported ? taskRow(provider, account, "keepalive") : "",
    ].filter(Boolean).join("")
    const detail = latest
      ? `${EVENT_NAMES[latest.event] || latest.event || "任务"} · ${relation(latest.at)}`
      : "尚无本插件执行记录"
    return `<article class="account">
      <div class="account-main">
        <div class="account-name">
          <span class="avatar" aria-hidden="true">${escapeHtml((account?.label || "?").slice(0, 1).toUpperCase())}</span>
          <div>
            <strong>${escapeHtml(account?.label || "未命名账号")}</strong>
            <small>${escapeHtml(detail)}</small>
          </div>
        </div>
      </div>
      ${accountQuota(account)}
      <div class="account-meta">
        ${modelLabel ? `<span class="model" title="${escapeHtml(actualModel || chosen?.id || automatic?.id || "")}">${
          escapeHtml(modelLabel)
        }</span>` : ""}
        <span>${provider.checkinSupported ? "支持签到" : "无签到能力"}</span>
        ${KEEPALIVE_VISIBLE
          ? `<span>${provider.keepaliveSupported ? "支持真实保活" : "暂不保活"}</span>`
          : ""}
      </div>
      ${taskRows ? `<div class="account-tasks">${taskRows}</div>` : ""}
      ${accountHistory(account, provider, index)}
      ${accountNotes(account, provider)}
    </article>`
  }

  function taskRow(provider, account, task) {
    const label = task === "checkin" ? "签到" : "保活"
    const point = todayPoint(account, task)
    const state = point
      ? activityState(point, task)
      : { tone: "muted", short: "待执行", title: `${label}尚未执行` }
    return `<div class="account-task-row">
      <span class="account-task-name">${label}</span>
      <span class="account-task-status">
        <span class="pill ${escapeHtml(state.tone)}" title="${escapeHtml(state.title)}">${
          escapeHtml(state.short)
        }</span>
      </span>
      <span class="account-task-actions">${taskControl(provider, account, task)}</span>
    </div>`
  }

  function taskControl(provider, account, task) {
    const on = taskOn(provider, account, task)
    const label = task === "checkin" ? "签到" : "保活"
    const disabled = !providerOn(provider)
    return `<span class="account-task-control">
      <label class="task-switch ${on ? "is-on" : "is-off"}"
        title="${disabled ? "请先启用此供应商" : `自动${label}${on ? "已开启" : "已关闭"}`}">
        <input
          class="task-enabled"
          type="checkbox"
          data-account-id="${escapeHtml(account?.controlId || "")}"
          data-task="${task}"
          aria-label="自动${label}"
          ${on ? "checked" : ""}
          ${disabled ? "disabled" : ""}
        >
        <span class="task-switch-track" aria-hidden="true"><i></i></span>
        <span class="task-switch-copy">自动</span>
      </label>
      <button
        class="task-run"
        type="button"
        data-task-run="${task}"
        data-account-id="${escapeHtml(account?.controlId || "")}"
        title="${disabled ? "请先启用此供应商" : `只对当前账号执行一次${label}`}"
        ${disabled ? "disabled" : ""}
      >
        <svg viewBox="0 0 12 12" aria-hidden="true"><path d="M3.2 1.8v8.4L9.8 6z"></path></svg>
        <span>执行${label}</span>
      </button>
    </span>`
  }

  function providerToggle(provider, id) {
    const on = providerOn(provider)
    return `<button
      class="provider-toggle ${on ? "is-on" : "is-off"}"
      type="button"
      data-provider-toggle="${escapeHtml(id)}"
      aria-pressed="${on ? "true" : "false"}"
    >${on ? "停用" : "启用"}</button>`
  }

  function providerRefresh(provider, id) {
    const disabled = !providerOn(provider) || busy || refreshBusy
    return `<button
      class="provider-refresh"
      type="button"
      data-provider-refresh="${escapeHtml(id)}"
      title="${disabled && !providerOn(provider) ? "供应商已停用，不刷新额度" : "立即刷新此供应商额度"}"
      aria-label="立即刷新 ${escapeHtml(provider?.name || provider?.id || "供应商")} 额度"
      ${disabled ? "disabled" : ""}
    >
      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"
           stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9M13.5 2v3h-3"></path>
      </svg>
    </button>`
  }

  function modelPicker(provider, id) {
    if (provider?.keepaliveSupported !== true) {
      return `<div class="provider-tools"><span class="model-hint">该供应商只参与签到和额度展示。</span></div>`
    }
    const models = modelsForProvider(provider)
    if (!models.length) {
      return `<div class="provider-tools"><span class="model-hint">该供应商还没有可用的模型列表。</span></div>`
    }
    const chosen = selectedModel(provider)
    const actualModels = [...new Set(
      (Array.isArray(provider?.accounts) ? provider.accounts : [])
        .map((account) => modelName(provider, account?.lastModel))
        .filter(Boolean),
    )]
    return `<div class="provider-tools">
      <span class="model-field-label">保活模型</span>
      <div class="model-picker" data-model-provider="${escapeHtml(id)}">
        <input
          class="model-search"
          type="search"
          autocomplete="off"
          spellcheck="false"
          placeholder="自动选择：全部可用模型"
          value="${escapeHtml(chosen ? modelTitle(chosen) : "")}"
          data-selected-model="${escapeHtml(chosen?.id || "")}"
          role="combobox"
          aria-autocomplete="list"
          aria-expanded="false"
        >
        <button class="model-clear" type="button" ${chosen ? "" : "hidden"}>自动</button>
        <div class="model-menu" role="listbox" hidden></div>
      </div>
      <span class="model-hint">${
        chosen
          ? `已指定优先模型${actualModels.length ? `；最近实际使用 ${escapeHtml(actualModels.join("、"))}` : ""}，失败时仍会自动回退`
          : actualModels.length
            ? `自动选择；最近实际使用 ${escapeHtml(actualModels.join("、"))}`
            : "默认优先近期成功可用的模型；无记录时按供应商推荐顺序，失败自动回退"
      }</span>
    </div>`
  }

  function providerCard(provider, index, open) {
    const id = String(provider?.id || provider?.name || `provider-${index}`)
    const accounts = Array.isArray(provider?.accounts) ? provider.accounts : []
    const model = provider?.defaultModel || provider?.cheapestModel
    const enabled = providerOn(provider)
    const chosen = selectedModel(provider)
    const capabilities = [
      provider?.checkinSupported ? "签到" : "",
      KEEPALIVE_VISIBLE && provider?.keepaliveSupported ? "保活" : "",
    ].filter(Boolean).join(" + ") || "仅展示"
    const taskChips = []
    for (const task of KEEPALIVE_VISIBLE ? ["checkin", "keepalive"] : ["checkin"]) {
      if (!provider?.[`${task}Supported`]) continue
      const activeAccounts = accounts.filter((account) => taskOn(provider, account, task))
      taskChips.push(taskChip(activeAccounts.map((account) => todayPoint(account, task)), task))
    }
    const failed = taskChips.reduce((sum, item) => sum + number(item?.counts?.failed), 0)
    const states = taskChips.length
      ? taskChips.map((item) => `<span class="status-chip ${escapeHtml(item.tone)}" title="${escapeHtml(item.title)}">${
        escapeHtml(item.label)
      }</span>`).join("")
      : `<span class="status-chip muted">仅展示</span>`
    const failureClass = failed ? " has-failure" : ""
    const idKey = String(provider?.id || "").toLowerCase()
    const nativeIcon = typeof provider?.icon === "string" && provider.icon.startsWith("data:image/")
      ? provider.icon
      : ""
    const iconFile = PROVIDER_ICON_FILES[idKey] || ""
    const mark = nativeIcon || iconFile
      ? `<img src="${escapeHtml(nativeIcon || iconFile)}" alt="" loading="lazy">`
      : `<span>${escapeHtml((provider?.name || "?").slice(0, 1).toUpperCase())}</span>`
    return `<details class="provider${enabled ? "" : " is-off"}${failureClass}" data-provider-id="${escapeHtml(id)}" ${open ? "open" : ""}>
      <summary>
        <span class="provider-mark">${mark}</span>
        <span class="provider-title">
          <strong>${escapeHtml(provider?.name || provider?.id || "未知供应商")}</strong>
          <small>${accounts.length} 个账号 · ${escapeHtml(capabilities)}${
            KEEPALIVE_VISIBLE
              ? chosen
                ? ` · 指定模型 ${escapeHtml(chosen.name || chosen.id)}`
                : model
                  ? ` · 默认最可用 ${escapeHtml(model.name || model.id)}`
                  : ""
              : ""
          }</small>
        </span>
        <span class="provider-states">${enabled ? states : `<span class="status-chip muted">已停用</span>`}</span>
        ${providerRefresh(provider, id)}
        ${providerToggle(provider, id)}
        <span class="chevron" aria-hidden="true">›</span>
      </summary>
      ${KEEPALIVE_VISIBLE ? modelPicker(provider, id) : ""}
      <div class="accounts">${accounts.map((account, accountIndex) =>
        accountRow(account, provider, accountIndex)).join("")}</div>
    </details>`
  }

  function updateProviderToggle() {
    const details = [...$("providers").querySelectorAll("details.provider[data-provider-id]")]
    const button = $("providers-toggle")
    const open = details.some((item) => item.open)
    button.hidden = details.length === 0
    button.disabled = details.length === 0
    button.textContent = open ? "全部折叠" : "全部展开"
  }

  function renderProviders(data) {
    const providers = Array.isArray(data?.providers) ? data.providers : []
    for (const details of $("providers").querySelectorAll("details.provider[data-provider-id]")) {
      providerOpen.set(details.dataset.providerId, details.open)
    }
    for (const [index, provider] of providers.entries()) {
      const id = String(provider?.id || provider?.name || `provider-${index}`)
      if (!providerOpen.has(id)) providerOpen.set(id, false)
    }
    $("providers").innerHTML = providers
      .map((provider, index) => {
        const id = String(provider?.id || provider?.name || `provider-${index}`)
        return providerCard(provider, index, providerOpen.get(id))
      })
      .join("")
    $("provider-count").textContent = `${providers.length} 个供应商 · ${number(data?.summary?.today?.accounts)} 个账号`
    $("providers-empty").hidden = providers.length > 0
    updateProviderToggle()
  }

  function modelOptionHtml(model, selectedId) {
    const selected = model.id === selectedId
    const rate = modelRate(model)
    return `<button
      class="model-option${selected ? " is-selected" : ""}"
      type="button"
      role="option"
      aria-selected="${selected ? "true" : "false"}"
      data-model-id="${escapeHtml(model.id)}"
    >
      <span class="model-option-copy">
        <b>${escapeHtml(model.name || model.id)}</b>
        <small>${escapeHtml(model.id)}</small>
      </span>
      ${rate ? `<span class="model-option-rate ${model.free ? "free" : ""}">${escapeHtml(rate)}</span>` : ""}
    </button>`
  }

  function renderModelMenu(input, query = "") {
    const picker = input.closest(".model-picker")
    if (!picker) return
    const menu = picker.querySelector(".model-menu")
    const provider = providerFor(picker.dataset.modelProvider)
    const selectedId = input.dataset.selectedModel || ""
    const needle = String(query || "")
    const scored = modelsForProvider(provider)
      .map((model) => {
        const searchable = `${model.name || ""} ${model.id || ""} ${modelRate(model)}`
        return { model, score: Math.max(fuzzyScore(needle, searchable), fuzzyScore(needle, model.name), fuzzyScore(needle, model.id)) }
      })
      .filter((item) => item.score >= 0)
      .sort((left, right) => right.score - left.score)
    menu.innerHTML = scored.length
      ? scored.map((item) => modelOptionHtml(item.model, selectedId)).join("")
      : `<div class="model-empty">没有匹配的模型</div>`
    menu.hidden = false
    picker.classList.add("is-open")
    input.setAttribute("aria-expanded", "true")
  }

  function closeModelMenus(except = null) {
    for (const picker of $("providers").querySelectorAll(".model-picker.is-open")) {
      if (picker === except) continue
      picker.classList.remove("is-open")
      const input = picker.querySelector(".model-search")
      const menu = picker.querySelector(".model-menu")
      if (menu) menu.hidden = true
      if (input) input.setAttribute("aria-expanded", "false")
    }
  }

  function resetModelInput(input) {
    const picker = input.closest(".model-picker")
    const provider = picker ? providerFor(picker.dataset.modelProvider) : null
    const chosen = provider ? selectedModel(provider) : null
    input.value = chosen ? modelTitle(chosen) : ""
    input.dataset.selectedModel = chosen?.id || ""
    const clear = picker?.querySelector(".model-clear")
    if (clear) clear.hidden = !chosen
  }

  async function saveModelPreference(providerId, modelId) {
    try {
      const options = await readPageOptions()
      const next = { ...(options.modelPreferences || {}) }
      if (modelId) next[providerId] = modelId
      else delete next[providerId]
      modelPreferences = next
      renderProviders(snapshot)
      await persistPageOptions(stagePageOptions((latest) => ({
        ...latest,
        modelPreferences: next,
      })))
      const provider = providerFor(providerId)
      const chosen = provider ? selectedModel(provider) : null
      toast(chosen ? `保活模型已设为 ${chosen.name || chosen.id}` : "保活模型已恢复为自动选择")
    } catch (error) {
      toast(`保存模型失败：${error?.message || error}`, "bad")
    }
  }

  function normalizePreferences(value) {
    return value && typeof value === "object" && !Array.isArray(value)
      ? { ...value }
      : {}
  }

  async function readPageOptions(refresh = false) {
    if (refresh || !pageOptions || typeof pageOptions !== "object" || Array.isArray(pageOptions)) {
      pageOptions = await magpie.options.get()
    }
    return pageOptions
  }

  function stagePageOptions(mutator) {
    const next = mutator({ ...(pageOptions || {}) })
    pageOptions = next
    return next
  }

  function writePageOptions(options) {
    if (!optionsWriter) {
      const create = window.QuotaKeepaliveOptionsWriter?.create
      optionsWriter = typeof create === "function"
        ? create({ set: (next) => magpie.options.set(next) })
        : { write: (next) => magpie.options.set(next) }
    }
    return optionsWriter.write(options)
  }

  async function persistPageOptions(options) {
    if (!options) return null
    return writePageOptions(options)
  }

  async function saveProviderEnabled(providerId, enabled) {
    try {
      const options = await readPageOptions()
      const next = normalizePreferences(options.providerPreferences)
      if (enabled) delete next[providerId]
      else next[providerId] = { enabled: false }
      providerPreferences = next
      renderProviders(snapshot)
      await persistPageOptions(stagePageOptions((latest) => ({
        ...latest,
        providerPreferences: next,
      })))
      toast(enabled ? "供应商任务已启用" : "供应商任务已停用")
    } catch (error) {
      toast(`保存供应商状态失败：${error?.message || error}`, "bad")
    }
  }

  async function saveAccountTask(providerId, controlId, task, enabled) {
    try {
      const options = await readPageOptions()
      const all = normalizePreferences(options.accountPreferences)
      const nextTask = normalizePreferences(all[controlId])
      if (enabled) delete nextTask[task]
      else nextTask[task] = false
      if (Object.keys(nextTask).length) all[controlId] = nextTask
      else delete all[controlId]
      accountPreferences = all
      renderProviders(snapshot)
      await persistPageOptions(stagePageOptions((latest) => ({
        ...latest,
        accountPreferences: all,
      })))
      const provider = providerFor(providerId)
      const account = provider?.accounts?.find((item) => item.controlId === controlId)
      toast(`${account?.label || "账号"}的${task === "checkin" ? "签到" : "保活"}已${
        enabled ? "启用" : "停用"
      }`)
    } catch (error) {
      toast(`保存账号任务失败：${error?.message || error}`, "bad")
    }
  }

  function moveModelOption(input, direction) {
    const menu = input.closest(".model-picker")?.querySelector(".model-menu")
    if (!menu || menu.hidden) return
    const options = [...menu.querySelectorAll(".model-option")]
    if (!options.length) return
    const current = options.findIndex((option) => option.classList.contains("is-active"))
    const next = current < 0
      ? direction > 0 ? 0 : options.length - 1
      : (current + direction + options.length) % options.length
    for (const option of options) option.classList.remove("is-active")
    options[next].classList.add("is-active")
    options[next].scrollIntoView({ block: "nearest" })
  }

  const STATUS_NAMES = {
    started: "开始",
    success: "成功",
    failed: "失败",
    skipped: "跳过",
    unknown: "未知",
  }

  const logKey = (record) => [
    record?.ts,
    record?.event,
    record?.provider,
    record?.account,
    record?.run,
  ].map((part) => String(part || "")).join("|")

  function formatDetailTime(value) {
    const at = Date.parse(value)
    if (!Number.isFinite(at)) return ""
    return new Intl.DateTimeFormat("zh-CN", {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false,
    }).format(at)
  }

  function formatRunTime(value) {
    const at = Date.parse(value)
    if (!Number.isFinite(at)) return ""
    return new Intl.DateTimeFormat("zh-CN", {
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false,
    }).format(at)
  }

  function durationText(value) {
    const ms = number(value)
    if (ms <= 0) return ""
    return ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(ms < 10000 ? 1 : 0)} s`
  }

  function runGroupFacts(group) {
    const providers = (Array.isArray(group?.providers) ? group.providers : [])
      .map((id) => providerFor(id)?.name || id)
      .filter(Boolean)
    return [
      ["执行时间", formatDetailTime(group?.at)],
      ["任务", EVENT_NAMES[group?.event] || group?.event || "任务"],
      ["结果", STATUS_NAMES[group?.status] || group?.status || ""],
      ["供应商", providers.slice(0, 4).join("、") + (providers.length > 4 ? ` 等 ${providers.length} 个` : "")],
      ["账号", number(group?.accounts) ? `${number(group.accounts)} 个` : ""],
      ["明细", group?.children?.length ? `${number(group.successes)} 成功 · ${number(group.failures)} 失败` : ""],
      ["耗时", durationText(group?.durationMs)],
      ["执行 ID", group?.run],
    ].filter(([, value]) => value !== undefined && value !== null && value !== "")
  }

  function runChildRow(record) {
    const provider = providerFor(record?.provider)?.name || record?.provider || "任务"
    const outcome = OUTCOME_NAMES[record?.outcome] || record?.outcome ||
      STATUS_NAMES[record?.status] || record?.status || ""
    const credit = number(record?.credit) > 0 ? `+${formatNumber(record.credit)} 积分` : ""
    const message = record?.error || record?.message || ""
    return `<li class="${statusClass(record?.status)}">
      <i aria-hidden="true"></i>
      <div class="run-child">
        <div class="run-child-head">
          <b>${escapeHtml(provider)}</b>
          <span>${escapeHtml(record?.account || "")}</span>
        </div>
        <div class="run-child-copy">
          <span>${escapeHtml([outcome, credit].filter(Boolean).join(" · "))}</span>
          ${message ? `<small>${escapeHtml(message)}</small>` : ""}
        </div>
      </div>
      <time>${escapeHtml(formatRunTime(record?.ts))}</time>
    </li>`
  }

  function runGroupDetail(group) {
    const facts = runGroupFacts(group)
    const children = Array.isArray(group?.children) ? group.children : []
    return `<div class="log-detail">
      <dl class="log-facts">
        ${facts.map(([label, value]) => `<div>
          <dt>${escapeHtml(label)}</dt>
          <dd${label === "执行 ID" ? ' class="is-code"' : ""}>${escapeHtml(value)}</dd>
        </div>`).join("")}
      </dl>
      ${group?.message ? `<div class="log-message">
        <span>说明</span>
        <p>${escapeHtml(group.message)}</p>
      </div>` : ""}
      ${children.length ? `<section class="run-children" aria-label="本次任务账号明细">
        <div class="log-timeline-head">
          <b>账号执行明细</b>
          <span>${children.length} 条</span>
        </div>
        <ol>${children.map(runChildRow).join("")}</ol>
      </section>` : ""}
    </div>`
  }

  function runGroupRecord(group) {
    const key = `run:${group?.id || `${group?.at}|${group?.event}`}`
    const providers = (Array.isArray(group?.providers) ? group.providers : [])
      .map((id) => providerFor(id)?.name || id)
      .filter(Boolean)
    const scope = [
      providers.slice(0, 2).join("、"),
      number(group?.accounts) ? `${number(group.accounts)} 个账号` : "",
    ].filter(Boolean).join(" · ")
    const summary = group?.message ||
      `${number(group?.successes)} 成功${number(group?.failures) ? ` · ${number(group.failures)} 失败` : ""}`
    return `<details class="log run ${statusClass(group?.status)}" data-log-key="${escapeHtml(key)}"${
      expandedLogs.has(key) ? " open" : ""
    }>
      <summary class="log-summary">
        <span class="log-mark" aria-hidden="true"></span>
        <span class="log-copy">
          <span class="log-head">
            <strong>${escapeHtml(EVENT_NAMES[group?.event] || group?.event || "任务")}</strong>
            <span class="log-provider">${escapeHtml(scope)}</span>
          </span>
          <span class="log-summary-text">${escapeHtml(summary)}</span>
        </span>
        <span class="log-when">
          <time>${escapeHtml(formatRunTime(group?.at))}</time>
          <span class="log-chevron" aria-hidden="true">
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"
                 stroke-linecap="round" stroke-linejoin="round"><path d="m4 6 4 4 4-4"></path></svg>
          </span>
        </span>
      </summary>
      ${runGroupDetail(group)}
    </details>`
  }

  function logDetailFacts(record) {
    const result = [
      OUTCOME_NAMES[record?.outcome] || record?.outcome || "",
      STATUS_NAMES[record?.status] || record?.status || "",
    ].filter(Boolean).join(" · ")
    return [
      ["执行时间", formatDetailTime(record?.ts)],
      ["任务", EVENT_NAMES[record?.event] || record?.event || ""],
      ["结果", result],
      ["供应商", record?.provider],
      ["账号", record?.account],
      ["模型", record?.model],
      ["保活方式", record?.probeMode === "activity" || record?.probeMode === "context"
        ? "真实活跃对话"
        : record?.probeMode === "short" ? "短探活" : ""],
      ["输入", number(record?.inputTokens) ? `${formatNumber(record.inputTokens)} tokens` : ""],
      ["本次积分", number(record?.credit) ? `+${formatNumber(record.credit)}` : ""],
      ["活跃确认", record?.activityVerified === true ? "已验证" : ""],
      ["连续签到", number(record?.streak) ? `${formatNumber(record.streak)} 天` : ""],
      ["耗时", durationText(record?.durationMs)],
      ["日期", record?.day],
      ["执行 ID", record?.run],
    ].filter(([, value]) => value !== undefined && value !== null && value !== "")
  }

  function logTimeline(record, records) {
    const run = String(record?.run || "")
    if (!run) return []
    return records
      .filter((item) => String(item?.run || "") === run)
      .slice()
      .sort((a, b) => Date.parse(a?.ts || 0) - Date.parse(b?.ts || 0))
  }

  function logDetail(record, records) {
    const facts = logDetailFacts(record)
    const message = record?.error || record?.message || ""
    const timeline = logTimeline(record, records)
    return `<div class="log-detail">
      <dl class="log-facts">
        ${facts.map(([label, value]) => `<div>
          <dt>${escapeHtml(label)}</dt>
          <dd${label === "执行 ID" ? ' class="is-code"' : ""}>${escapeHtml(value)}</dd>
        </div>`).join("")}
      </dl>
      ${message ? `<div class="log-message">
        <span>说明</span>
        <p>${escapeHtml(message)}</p>
      </div>` : ""}
      ${timeline.length > 1 ? `<section class="log-timeline" aria-label="同一轮执行步骤">
        <div class="log-timeline-head">
          <b>同一轮执行</b>
          <span>${timeline.length} 个步骤</span>
        </div>
        <ol>
          ${timeline.map((item) => {
            const key = logKey(item) === logKey(record) ? " current" : ""
            const outcome = OUTCOME_NAMES[item?.outcome] || item?.outcome || STATUS_NAMES[item?.status] || item?.status || ""
            const duration = durationText(item?.durationMs)
            return `<li class="${statusClass(item?.status)}${key}">
              <i aria-hidden="true"></i>
              <div>
                <b>${escapeHtml(EVENT_NAMES[item?.event] || item?.event || "任务")}</b>
                <span>${escapeHtml([outcome, duration].filter(Boolean).join(" · "))}</span>
              </div>
              <time>${escapeHtml(formatTime(item?.ts))}</time>
            </li>`
          }).join("")}
        </ol>
      </section>` : ""}
    </div>`
  }

  function logRecord(record, records) {
    const event = EVENT_NAMES[record?.event] || record?.event || "任务"
    const status = record?.status || "unknown"
    const detail = record?.message || record?.error ||
      OUTCOME_NAMES[record?.outcome] || record?.outcome || ""
    const account = record?.account ? ` · ${record.account}` : ""
    const key = logKey(record)
    return `<details class="log ${statusClass(status)}" data-log-key="${escapeHtml(key)}"${expandedLogs.has(key) ? " open" : ""}>
      <summary class="log-summary">
        <span class="log-mark" aria-hidden="true"></span>
        <span class="log-copy">
          <span class="log-head">
            <strong>${escapeHtml(event)}</strong>
            <span class="log-provider">${escapeHtml(record?.provider || "")}${escapeHtml(account)}</span>
          </span>
          <span class="log-summary-text">${escapeHtml(detail)}</span>
        </span>
        <span class="log-when">
          <time>${escapeHtml(formatTime(record?.ts))}</time>
          <span class="log-chevron" aria-hidden="true">
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"
                 stroke-linecap="round" stroke-linejoin="round"><path d="m4 6 4 4 4-4"></path></svg>
          </span>
        </span>
      </summary>
      ${logDetail(record, records)}
    </details>`
  }

  function renderLogs(data) {
    const groups = (Array.isArray(data?.runHistory) ? data.runHistory : [])
      .filter((group) => visibleLogEvent(group?.event))
      .slice()
      .sort((left, right) => {
        const leftAt = Date.parse(left?.at)
        const rightAt = Date.parse(right?.at)
        if (Number.isFinite(leftAt) && Number.isFinite(rightAt)) return rightAt - leftAt
        return String(right?.at || "").localeCompare(String(left?.at || ""))
      })
      .slice(0, 12)
    if (groups.length) {
      $("logs").innerHTML = groups.map(runGroupRecord).join("")
      $("log-count").textContent = `最近 ${groups.length} 次任务`
      return
    }
    const allRecords = Array.isArray(data?.records) ? data.records : []
    const records = allRecords
      .filter((record) => visibleLogEvent(record?.event))
      .slice(-12)
      .reverse()
    $("logs").innerHTML = records.length
      ? records.map((record) => logRecord(record, allRecords)).join("")
      : `<div class="empty compact"><b>还没有执行记录</b><span>签到完成后，这里会显示每次结果。</span></div>`
    $("log-count").textContent = records.length ? `最近 ${records.length} 条` : "暂无记录"
  }

  function renderCommand(data) {
    const command = data?.command
    const dot = $("command-dot")
    dot.className = "status-dot"
    if (!command) {
      $("command-title").textContent = "准备就绪"
      $("command-detail").textContent = "页面会每 5 秒同步一次本地快照。"
      $("command-bar").className = "command-bar"
      return
    }
    const action = ACTIONS[command.action]?.title || command.action || "任务"
    const running = command.status === "queued" || command.status === "running"
    const failed = command.status === "failed"
    dot.classList.add(running ? "wait" : failed ? "bad" : command.status === "success" ? "good" : "warn")
    $("command-bar").className = `command-bar ${running ? "is-running" : failed ? "is-failed" : "is-done"}`
    $("command-title").textContent = running ? `${action}执行中` : failed ? `${action}失败` : `${action}已完成`
    $("command-detail").textContent = command.summary || command.error || (running ? "正在等待宿主完成本次任务。" : "结果已同步。")
  }

  function render(data) {
    snapshot = data || snapshot
    if (!snapshot) {
      $("providers-empty").hidden = false
      return
    }
    renderSummary(snapshot)
    // Re-rendering the provider list while its combobox is open would steal
    // focus and close the search menu during the five-second refresh.
    const editingModel = document.activeElement?.closest?.(".model-picker")
    if (!editingModel) renderProviders(snapshot)
    renderLogs(snapshot)
    renderCommand(snapshot)
    $("snapshot-time").textContent = snapshot.generatedAt
      ? `同步于 ${formatTime(snapshot.generatedAt)}`
      : ""
    document.documentElement.dataset.snapshotDay = snapshot.today || ""
  }

  function toast(message, tone = "") {
    const box = $("toast")
    box.textContent = message
    box.className = `toast ${tone}`
    box.hidden = false
    clearTimeout(toast.timer)
    toast.timer = setTimeout(() => {
      box.hidden = true
    }, tone === "bad" ? 8000 : 3500)
  }

  function setBusy(value, text = "") {
    busy = value
    for (const id of ["detect", "checkin", "keepalive"]) {
      const button = $(id)
      if (button) button.disabled = value
    }
    updateRefreshControls()
    if (text) toast(text)
  }

  function updateRefreshControls() {
    const all = $("refresh")
    if (all) {
      all.disabled = busy || refreshBusy
      all.classList.toggle("spin", refreshBusy)
    }
    for (const button of $("providers").querySelectorAll("[data-provider-refresh]")) {
      const provider = providerFor(button.dataset.providerRefresh)
      button.disabled = busy || refreshBusy || !providerOn(provider)
    }
    for (const button of $("providers").querySelectorAll("[data-task-run]")) {
      const provider = providerFor(button.closest("details.provider")?.dataset.providerId)
      button.disabled = busy || !providerOn(provider)
    }
    for (const input of $("providers").querySelectorAll(".task-enabled")) {
      const provider = providerFor(input.closest("details.provider")?.dataset.providerId)
      input.disabled = busy || !providerOn(provider)
    }
  }

  function scheduleDescription(options = {}) {
    if (options.scheduleEnabled !== true) return "自动任务已关闭，可手动执行或在这里开启。"
    return `每 ${number(options.scheduleMinutes) || 60} 分钟为启用中的供应商执行一次签到。`
  }

  async function loadSchedule(showToast = false, refresh = false) {
    try {
      const options = await readPageOptions(refresh)
      modelPreferences = options.modelPreferences &&
        typeof options.modelPreferences === "object" &&
        !Array.isArray(options.modelPreferences)
        ? { ...options.modelPreferences }
        : {}
      providerPreferences = normalizePreferences(options.providerPreferences)
      accountPreferences = normalizePreferences(options.accountPreferences)
      $("schedule-enabled").checked = options.scheduleEnabled === true
      $("schedule-minutes").value = String(options.scheduleMinutes || 60)
      $("schedule-action").value = "checkin"
      $("schedule-status").textContent = scheduleDescription(options)
      if (showToast) toast("定时计划已保存")
    } catch (error) {
      if (showToast) toast(`读取计划失败：${error?.message || error}`, "bad")
    }
  }

  async function saveSchedule() {
    const minutes = Math.max(5, Math.min(10080, Math.round(number($("schedule-minutes").value) || 60)))
    await readPageOptions()
    await persistPageOptions(stagePageOptions((latest) => ({
      ...latest,
      scheduleEnabled: $("schedule-enabled").checked,
      scheduleMinutes: minutes,
      scheduleAction: "checkin",
    })))
    await loadSchedule(true)
  }

  async function queue(action, extra = {}) {
    if (busy || (refreshBusy && action !== "refresh")) return false
    const meta = ACTIONS[action]
    if (!meta) return false
    setBusy(true)
    try {
      await readPageOptions()
      const id = `${action}-${Date.now()}-${crypto.randomUUID?.() || Math.random().toString(16).slice(2)}`
      await persistPageOptions(stagePageOptions((latest) => ({
        ...latest,
        pendingAction: action,
        pendingId: id,
        pendingProvider: extra.provider || "",
        pendingAccount: extra.account || "",
        pendingRequestedAt: new Date().toISOString(),
      })))
      $("command-title").textContent = `${meta.title}等待宿主执行`
      $("command-detail").textContent = meta.detail
      $("command-dot").className = "status-dot wait"
      toast(`${meta.title}已提交`)
      return true
    } catch (error) {
      toast(`提交失败：${error?.message || error}`, "bad")
      return false
    } finally {
      setBusy(false)
    }
  }

  function closeConfirm() {
    activeConfirm = false
    confirmRequest = null
    $("confirm-layer").hidden = true
  }

  function openConfirm(request = {}) {
    confirmRequest = request
    const label = String(request.label || "").trim()
    $("confirm-title").textContent = label
      ? `为“${label}”发送真实保活消息？`
      : "发送真实保活消息？"
    $("confirm-detail").textContent = label
      ? "只会为这个账号发送一次真实对话，可能消耗积分。"
      : "这会为全部已适配供应商的账号发送真实对话，优先尝试近期验证可用且能成功回复的模型；仍可能消耗积分。"
    activeConfirm = true
    $("confirm-layer").hidden = false
    $("confirm-send").focus()
  }

  function bind() {
    $("providers").addEventListener("input", (event) => {
      const input = event.target
      if (!(input instanceof HTMLInputElement) || !input.classList.contains("model-search")) return
      input.dataset.selectedModel = ""
      const clear = input.closest(".model-picker")?.querySelector(".model-clear")
      if (clear) clear.hidden = false
      renderModelMenu(input, input.value)
    })
    $("providers").addEventListener("focusin", (event) => {
      const input = event.target
      if (!(input instanceof HTMLInputElement) || !input.classList.contains("model-search")) return
      closeModelMenus(input.closest(".model-picker"))
      renderModelMenu(input, "")
    })
    $("providers").addEventListener("focusout", (event) => {
      const input = event.target
      if (!(input instanceof HTMLInputElement) || !input.classList.contains("model-search")) return
      const picker = input.closest(".model-picker")
      setTimeout(() => {
        if (picker && picker.contains(document.activeElement)) return
        if (!input.dataset.selectedModel) resetModelInput(input)
      }, 120)
    })
    $("providers").addEventListener("keydown", (event) => {
      const input = event.target
      if (!(input instanceof HTMLInputElement) || !input.classList.contains("model-search")) return
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault()
        if (input.getAttribute("aria-expanded") !== "true") renderModelMenu(input, "")
        moveModelOption(input, event.key === "ArrowDown" ? 1 : -1)
      } else if (event.key === "Enter") {
        const active = input.closest(".model-picker")?.querySelector(".model-option.is-active")
        if (active) {
          event.preventDefault()
          active.click()
        }
      } else if (event.key === "Escape") {
        event.preventDefault()
        closeModelMenus()
        input.blur()
      }
    })
    $("providers").addEventListener("change", async (event) => {
      const input = event.target
      if (!(input instanceof HTMLInputElement) || !input.classList.contains("task-enabled")) return
      const provider = providerFor(input.closest("details.provider")?.dataset.providerId)
      if (!providerOn(provider)) return
      const account = provider?.accounts?.find((item) => item.controlId === input.dataset.accountId)
      if (provider && account) {
        await saveAccountTask(provider.id, account.controlId, input.dataset.task, input.checked)
      }
    })
    $("providers").addEventListener("click", async (event) => {
      const providerRefreshButton = event.target.closest("[data-provider-refresh]")
      if (providerRefreshButton) {
        event.preventDefault()
        event.stopPropagation()
        await requestRefresh(providerRefreshButton.dataset.providerRefresh || "")
        return
      }
      const providerToggle = event.target.closest("[data-provider-toggle]")
      if (providerToggle) {
        event.preventDefault()
        const provider = providerFor(providerToggle.dataset.providerToggle)
        await saveProviderEnabled(providerToggle.dataset.providerToggle, !providerOn(provider))
        return
      }
      const taskRun = event.target.closest("[data-task-run]")
      if (taskRun) {
        event.preventDefault()
        event.stopPropagation()
        const provider = providerFor(taskRun.closest("details.provider")?.dataset.providerId)
        if (!providerOn(provider)) return
        const account = provider?.accounts?.find((item) => item.controlId === taskRun.dataset.accountId)
        if (!provider || !account) return
        if (taskRun.dataset.taskRun === "keepalive") {
          openConfirm({
            provider: provider.id,
            account: account.controlId,
            label: account.label,
          })
        } else {
          await queue("checkin", {
            provider: provider.id,
            account: account.controlId,
          })
        }
        return
      }
      const clear = event.target.closest(".model-clear")
      if (clear) {
        const picker = clear.closest(".model-picker")
        const input = picker?.querySelector(".model-search")
        if (input) {
          input.value = ""
          input.dataset.selectedModel = ""
        }
        if (picker) await saveModelPreference(picker.dataset.modelProvider, "")
        return
      }
      const option = event.target.closest(".model-option")
      if (option) {
        const picker = option.closest(".model-picker")
        const input = picker?.querySelector(".model-search")
        const modelId = option.dataset.modelId || ""
        const provider = providerFor(picker?.dataset.modelProvider)
        const model = modelsForProvider(provider).find((item) => item.id === modelId)
        if (input && model) {
          input.value = modelTitle(model)
          input.dataset.selectedModel = model.id
        }
        closeModelMenus()
        if (picker) await saveModelPreference(picker.dataset.modelProvider, modelId)
      }
    })
    document.addEventListener("click", (event) => {
      if (!event.target.closest?.(".model-picker")) closeModelMenus()
    })
    $("providers").addEventListener("toggle", (event) => {
      const details = event.target
      if (!(details instanceof HTMLDetailsElement)) return
      const historyKey = details.dataset.historyKey
      if (historyKey) {
        if (details.open) expandedHistories.add(historyKey)
        else expandedHistories.delete(historyKey)
        return
      }
      const id = details.dataset.providerId
      if (id) providerOpen.set(id, details.open)
      updateProviderToggle()
    }, true)
    $("providers-toggle").addEventListener("click", () => {
      const details = [...$("providers").querySelectorAll("details.provider[data-provider-id]")]
      if (!details.length) return
      const open = !details.some((item) => item.open)
      for (const item of details) {
        item.open = open
        providerOpen.set(item.dataset.providerId, open)
      }
      updateProviderToggle()
    })
    $("logs").addEventListener("toggle", (event) => {
      const details = event.target
      if (!(details instanceof HTMLDetailsElement) || !details.classList.contains("log")) return
      const key = details.dataset.logKey
      if (!key) return
      if (details.open) expandedLogs.add(key)
      else expandedLogs.delete(key)
    }, true)
    $("detect").addEventListener("click", () => queue("detect"))
    $("checkin").addEventListener("click", () => queue("checkin"))
    $("keepalive")?.addEventListener("click", () => openConfirm())
    $("schedule-save").addEventListener("click", () => {
      saveSchedule().catch((error) => toast(`保存失败：${error?.message || error}`, "bad"))
    })
    $("confirm-cancel").addEventListener("click", closeConfirm)
    $("confirm-send").addEventListener("click", async () => {
      const request = confirmRequest
      closeConfirm()
      await queue("keepalive", request || {})
    })
    $("confirm-layer").addEventListener("click", (event) => {
      if (event.target === $("confirm-layer")) closeConfirm()
    })
    addEventListener("keydown", (event) => {
      if (event.key === "Escape" && activeConfirm) closeConfirm()
    })
    $("refresh").addEventListener("click", () => requestRefresh(""))
  }

  // Plugin pages have connect-src 'none', so a fetch of data.js is blocked.
  // A second same-folder script tag is allowed by script-src and refreshes
  // window.DailyKeepaliveSnapshot without reloading the whole iframe.
  function reloadSnapshot() {
    return new Promise((resolve) => {
      const script = document.createElement("script")
      script.src = `data.js?t=${Date.now()}-${tick++}`
      script.onload = () => {
        script.remove()
        render(window.DailyKeepaliveSnapshot)
        resolve(true)
      }
      script.onerror = () => {
        script.remove()
        resolve(false)
      }
      document.head.append(script)
    })
  }

  async function requestRefresh(provider = "") {
    if (busy || refreshBusy) return
    const wanted = String(provider || "")
    const target = wanted ? providerFor(wanted) : null
    if (wanted && !target) return
    if (wanted && !providerOn(target)) return
    refreshBusy = true
    updateRefreshControls()
    const started = performance.now()
    try {
      const submitted = await queue("refresh", { provider: wanted })
      if (!submitted) return
      await new Promise((resolve) => setTimeout(resolve, 1800))
      const refreshed = await reloadSnapshot()
      if (refreshed) {
        toast(wanted ? `已请求刷新 ${target?.name || wanted} 额度` : "已请求刷新全部启用供应商额度", "ok")
      } else {
        toast("额度刷新已提交，页面快照稍后同步", "ok")
      }
    } finally {
      const remaining = 650 - (performance.now() - started)
      if (remaining > 0) await new Promise((resolve) => setTimeout(resolve, remaining))
      refreshBusy = false
      updateRefreshControls()
    }
  }

  async function start() {
    try {
      await magpie.ready
    } catch {}
    bind()
    await loadSchedule(false, true)
    const loaded = await reloadSnapshot()
    if (!loaded) render(null)
    clearInterval(refreshTimer)
    refreshTimer = setInterval(() => {
      if (!document.hidden && !busy) reloadSnapshot()
    }, 5000)
  }

  start()
})()
