(() => {
  const $ = (id) => document.getElementById(id)
  const ACTIONS = {
    detect: { title: "检测账号", detail: "正在读取 Magpie 当前已登录的供应商与账号。" },
    checkin: { title: "全部签到", detail: "正在让已有签到能力的供应商执行签到。" },
    keepalive: { title: "发送保活", detail: "正在为全部已适配供应商账号发送真实对话。" },
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
  let refreshTimer = 0
  let tick = 0
  const providerOpen = new Map()
  const expandedHistories = new Set()
  const expandedLogs = new Set()
  let modelPreferences = {}

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

  const modelRank = (model) => [
    model?.free === true ? 0 : 1,
    number(model?.rate),
    number(model?.cost),
    String(model?.name || model?.id || "").toLowerCase(),
  ]

  const compareModels = (a, b) => {
    const left = modelRank(a)
    const right = modelRank(b)
    for (let index = 0; index < left.length; index++) {
      if (left[index] === right[index]) continue
      return left[index] < right[index] ? -1 : 1
    }
    return 0
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
      .sort(compareModels)
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

  const statusClass = (status) => ({
    success: "good",
    failed: "bad",
    skipped: "warn",
    started: "muted",
  })[status] || "muted"

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

  function activityBadge(point, labels) {
    if (!point) return ""
    if (point.success) {
      const credit = labels === "checkin" ? pointCredit(point) : ""
      return `<span class="pill good">${escapeHtml(labels === "checkin" ? "已签到" : "已保活")}${
        credit ? ` ${escapeHtml(credit)}` : ""
      }</span>`
    }
    if (point.status === "failed") {
      return `<span class="pill bad">${escapeHtml(labels === "checkin" ? "签到失败" : "保活失败")}</span>`
    }
    if (point.status === "skipped") {
      const text = labels === "checkin" ? "今日未签到" : "今日未保活"
      return `<span class="pill warn">${escapeHtml(text)}</span>`
    }
    return ""
  }

  function accountBadges(account, provider) {
    const badges = []
    if (provider?.checkinSupported) {
      badges.push(activityBadge(todayPoint(account, "checkin"), "checkin"))
    }
    if (provider?.keepaliveSupported) {
      badges.push(activityBadge(todayPoint(account, "keepalive"), "keepalive"))
    }
    return badges.filter(Boolean).join("")
  }

  function accountHistory(account, provider, index) {
    const days = activityDays(account)
    if (!days.length) return ""
    if (!days.some((day) => day?.checkin || day?.keepalive)) return ""
    const summary = account?.activity?.summary || {}
    const key = `${provider?.id || ""}|${account?.keyTag || ""}|${index}`
    return `<details class="account-history" data-history-key="${escapeHtml(key)}"${
      expandedHistories.has(key) ? " open" : ""
    }>
      <summary>
        <span>近 5 天</span>
        <small>签到 ${number(summary.checkinDays)}/${number(summary.days) || days.length} · 保活 ${
          number(summary.keepaliveDays)
        }/${number(summary.days) || days.length}</small>
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
          if (keepalive?.success) {
            chips.push(`<span class="history-chip good">保活</span>`)
          } else if (keepalive?.status === "failed") {
            chips.push(`<span class="history-chip bad">保活失败</span>`)
          } else if (keepalive) {
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
    const failed = number(today.failed)
    const skipped = number(today.checkin?.skipped) + number(today.keepalive?.failed)
    const accounts = number(today.accounts)
    const checkinDone = number(today.checkinDone)
    const keepaliveDone = number(today.keepaliveDone)
    $("summary").innerHTML = [
      stat("已登录账号", String(accounts), `${number(data?.providers?.length)} 个供应商`),
      stat("今日签到", String(checkinDone), `${number(today.checkin?.success)} 次成功`),
      stat("今日保活", String(keepaliveDone), keepaliveDone ? "真实对话已发送" : "等待手动触发"),
      stat(
        "需要处理",
        String(failed + skipped),
        failed ? "存在执行失败" : skipped ? `${skipped} 个账号被跳过` : "暂无失败",
        failed ? "bad" : "",
      ),
    ].join("")
  }

  function quotaView(account) {
    const windows = Array.isArray(account?.quota?.windows) ? account.quota.windows : []
    if (windows.length) {
      const shown = windows.slice(0, 2).map((window) => {
        let value = ""
        if (window.display) value = window.display
        else if (window.remaining !== undefined) value = `剩余 ${formatNumber(window.remaining)} ${window.unit || ""}`.trim()
        else if (window.used !== undefined) value = `已用 ${formatNumber(window.used)} ${window.unit || ""}`.trim()
        else if (window.balance !== undefined) value = `${formatNumber(window.balance)} ${window.unit || ""}`.trim()
        if (value) {
          const name = String(window.name || "").trim()
          return name && !value.toLowerCase().startsWith(name.toLowerCase()) ? `${name} ${value}` : value
        }
        return ""
      }).filter(Boolean)
      if (shown.length) return shown.join(" · ")
    }
    return "额度待检测"
  }

  function quotaTone(account) {
    const history = Array.isArray(account?.history) ? account.history : []
    if (history.length < 2) return ""
    const delta = number(history.at(-1)?.left) - number(history.at(-2)?.left)
    return delta < 0 ? "down" : delta > 0 ? "up" : ""
  }

  function accountRow(account, provider, index) {
    const checkinMessage = account?.checkin?.message || ""
    const latest = account?.latest
    const latestText = latest && latest.message ? latest.message : ""
    const error = account?.error || (latest?.status === "failed" ? latestText : "")
    const quota = quotaView(account)
    const quotaToneClass = quotaTone(account)
    const badges = accountBadges(account, provider)
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
        <span class="account-badges">${badges}</span>
      </div>
      <div class="account-meta">
        <span class="quota ${quotaToneClass}">${escapeHtml(quota)}</span>
        <span>${provider.checkinSupported ? "支持签到" : "无签到能力"}</span>
        <span>${provider.keepaliveSupported ? "支持真实保活" : "暂不保活"}</span>
      </div>
      ${checkinMessage ? `<p class="account-note">${escapeHtml(checkinMessage)}</p>` : ""}
      ${accountHistory(account, provider, index)}
      ${error ? `<p class="account-error">${escapeHtml(error)}</p>` : ""}
    </article>`
  }

  function modelPicker(provider, id) {
    if (provider?.keepaliveSupported !== true) return ""
    const models = modelsForProvider(provider)
    if (!models.length) {
      return `<div class="provider-tools"><span class="model-hint">该供应商还没有可用的模型列表。</span></div>`
    }
    const chosen = selectedModel(provider)
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
      <span class="model-hint">${chosen ? "已指定优先模型，失败时仍会自动回退" : "默认按免费、倍率、成本选择，失败时自动回退"}</span>
    </div>`
  }

  function providerCard(provider, index, open) {
    const id = String(provider?.id || provider?.name || `provider-${index}`)
    const accounts = Array.isArray(provider?.accounts) ? provider.accounts : []
    const done = accounts.filter((account) => {
      const checkin = todayPoint(account, "checkin")
      const keepalive = todayPoint(account, "keepalive")
      return (provider?.checkinSupported && checkin?.success) ||
        (provider?.keepaliveSupported && keepalive?.success)
    }).length
    const failed = accounts.filter((account) => {
      const checkin = todayPoint(account, "checkin")
      const keepalive = todayPoint(account, "keepalive")
      return checkin?.status === "failed" || keepalive?.status === "failed"
    }).length
    const model = provider?.cheapestModel
    const chosen = selectedModel(provider)
    const capabilities = [
      provider?.checkinSupported ? "签到" : "",
      provider?.keepaliveSupported ? "保活" : "",
    ].filter(Boolean).join(" + ") || "仅展示"
    const tone = failed ? "bad" : done === accounts.length && accounts.length ? "good" : ""
    const idKey = String(provider?.id || "").toLowerCase()
    const nativeIcon = typeof provider?.icon === "string" && provider.icon.startsWith("data:image/")
      ? provider.icon
      : ""
    const iconFile = PROVIDER_ICON_FILES[idKey] || ""
    const mark = nativeIcon || iconFile
      ? `<img src="${escapeHtml(nativeIcon || iconFile)}" alt="" loading="lazy">`
      : `<span>${escapeHtml((provider?.name || "?").slice(0, 1).toUpperCase())}</span>`
    return `<details class="provider" data-provider-id="${escapeHtml(id)}" ${open ? "open" : ""}>
      <summary>
        <span class="provider-mark">${mark}</span>
        <span class="provider-title">
          <strong>${escapeHtml(provider?.name || provider?.id || "未知供应商")}</strong>
          <small>${accounts.length} 个账号 · ${escapeHtml(capabilities)}${chosen ? ` · 指定模型 ${escapeHtml(chosen.name || chosen.id)}` : model ? ` · 默认最便宜 ${escapeHtml(model.name || model.id)}` : ""}</small>
        </span>
        <span class="provider-state ${tone}">${failed ? `${failed} 个需处理` : done ? `${done}/${accounts.length} 服务端确认` : "待执行"}</span>
        <span class="chevron" aria-hidden="true">›</span>
      </summary>
      ${modelPicker(provider, id)}
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
      .sort((left, right) => right.score - left.score || compareModels(left.model, right.model))
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
      const options = await magpie.options.get()
      const next = { ...(options.modelPreferences || {}) }
      if (modelId) next[providerId] = modelId
      else delete next[providerId]
      await magpie.options.set({ ...options, modelPreferences: next })
      modelPreferences = next
      renderProviders(snapshot)
      const provider = providerFor(providerId)
      const chosen = provider ? selectedModel(provider) : null
      toast(chosen ? `保活模型已设为 ${chosen.name || chosen.id}` : "保活模型已恢复为自动选择")
    } catch (error) {
      toast(`保存模型失败：${error?.message || error}`, "bad")
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
      ["输入", number(record?.inputTokens) ? `${formatNumber(record.inputTokens)} tokens` : ""],
      ["本次积分", number(record?.credit) ? `+${formatNumber(record.credit)}` : ""],
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
    const groups = Array.isArray(data?.runHistory) ? data.runHistory.slice(0, 12) : []
    if (groups.length) {
      $("logs").innerHTML = groups.map(runGroupRecord).join("")
      $("log-count").textContent = `最近 ${groups.length} 次任务`
      return
    }
    const allRecords = Array.isArray(data?.records) ? data.records : []
    const records = allRecords.slice(-12).reverse()
    $("logs").innerHTML = records.length
      ? records.map((record) => logRecord(record, allRecords)).join("")
      : `<div class="empty compact"><b>还没有执行记录</b><span>签到或保活完成后，这里会显示每次结果。</span></div>`
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
    for (const id of ["detect", "checkin", "keepalive"]) $(id).disabled = value
    if (text) toast(text)
  }

  function scheduleDescription(options = {}) {
    if (options.scheduleEnabled !== true) return "自动任务已关闭，可手动执行或在这里开启。"
    const action = {
      checkin: "签到",
      keepalive: "真实保活",
      all: "签到 + 真实保活",
    }[options.scheduleAction] || "签到"
    return `每 ${number(options.scheduleMinutes) || 60} 分钟执行一次${action}。`
  }

  async function loadSchedule(showToast = false) {
    try {
      const options = await magpie.options.get()
      modelPreferences = options.modelPreferences &&
        typeof options.modelPreferences === "object" &&
        !Array.isArray(options.modelPreferences)
        ? { ...options.modelPreferences }
        : {}
      $("schedule-enabled").checked = options.scheduleEnabled === true
      $("schedule-minutes").value = String(options.scheduleMinutes || 60)
      $("schedule-action").value = ["checkin", "keepalive", "all"].includes(options.scheduleAction)
        ? options.scheduleAction
        : "checkin"
      $("schedule-status").textContent = scheduleDescription(options)
      if (showToast) toast("定时计划已保存")
    } catch (error) {
      if (showToast) toast(`读取计划失败：${error?.message || error}`, "bad")
    }
  }

  async function saveSchedule() {
    const minutes = Math.max(5, Math.min(10080, Math.round(number($("schedule-minutes").value) || 60)))
    const options = await magpie.options.get()
    await magpie.options.set({
      ...options,
      scheduleEnabled: $("schedule-enabled").checked,
      scheduleMinutes: minutes,
      scheduleAction: $("schedule-action").value,
    })
    await loadSchedule(true)
  }

  async function queue(action) {
    if (busy) return
    const meta = ACTIONS[action]
    if (!meta) return
    setBusy(true)
    try {
      const options = await magpie.options.get()
      const id = `${action}-${Date.now()}-${crypto.randomUUID?.() || Math.random().toString(16).slice(2)}`
      await magpie.options.set({
        ...options,
        pendingAction: action,
        pendingId: id,
        pendingRequestedAt: new Date().toISOString(),
      })
      $("command-title").textContent = `${meta.title}等待宿主执行`
      $("command-detail").textContent = meta.detail
      $("command-dot").className = "status-dot wait"
      toast(`${meta.title}已提交`)
    } catch (error) {
      toast(`提交失败：${error?.message || error}`, "bad")
    } finally {
      setBusy(false)
    }
  }

  function closeConfirm() {
    activeConfirm = false
    $("confirm-layer").hidden = true
  }

  function openConfirm() {
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
    $("providers").addEventListener("click", async (event) => {
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
    $("keepalive").addEventListener("click", openConfirm)
    $("schedule-save").addEventListener("click", () => {
      saveSchedule().catch((error) => toast(`保存失败：${error?.message || error}`, "bad"))
    })
    $("confirm-cancel").addEventListener("click", closeConfirm)
    $("confirm-send").addEventListener("click", async () => {
      closeConfirm()
      await queue("keepalive")
    })
    $("confirm-layer").addEventListener("click", (event) => {
      if (event.target === $("confirm-layer")) closeConfirm()
    })
    addEventListener("keydown", (event) => {
      if (event.key === "Escape" && activeConfirm) closeConfirm()
    })
    $("refresh").addEventListener("click", () => manualRefresh())
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

  async function manualRefresh() {
    const button = $("refresh")
    if (button.disabled) return
    button.disabled = true
    button.classList.add("spin")
    const started = performance.now()
    try {
      const refreshed = await reloadSnapshot()
      if (refreshed) toast("快照已刷新", "ok")
      else toast("快照刷新失败", "bad")
    } finally {
      const remaining = 650 - (performance.now() - started)
      if (remaining > 0) await new Promise((resolve) => setTimeout(resolve, remaining))
      button.classList.remove("spin")
      button.disabled = false
      button.blur()
    }
  }

  async function start() {
    try {
      await magpie.ready
    } catch {}
    bind()
    await loadSchedule()
    render(snapshot)
    clearInterval(refreshTimer)
    refreshTimer = setInterval(() => {
      if (!document.hidden && !busy) reloadSnapshot()
    }, 5000)
  }

  start()
})()
