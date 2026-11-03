import * as Bound from "../bindings/qka/internal/gui/boundservice.js";
import { Window, Events } from "/wails/runtime.js";
import {
  POKEMON_CATALOG,
  POKEMON_EVOLUTION_THRESHOLDS,
  POKEMON_MAX_DAILY_TOKENS,
  evolutionStageForDailyTokens,
  pokemonById,
} from "./pokemon-art.js";
import { nextGroupExpansion } from "./group-collapse.js";
import { petAimPoint } from "./pet-gaze.js";
import { createPokemon3D } from "./pokemon-3d.js";
import { populateProviderIcon, providerIconSpec } from "./provider-icons.js";
import {
  normalizeTitleTheme,
  titleIconMarkup,
  titleLevelForSpeed,
  titleLevelProgress,
  titleThemeOrder,
  titleThresholds,
} from "./title-system.js";

const panelTabIds = ["checkin", "speed", "pet"];
const panelTabStorageKey = "qka.panel.tab";
const petSelectedStorageKey = "qka.pet.selected";
const opLabels = { checkin: "签到" };
const expanded = {};
const providerInfo = {};
const speedHistory = Array.from({ length: 24 }, function () { return 0; });
const latestPointer = { x: null, y: null };
const agentExpansion = new Map();

let activeTab = "checkin";
let currentAccounts = [];
let currentTargets = [];
let latestSpeedOutput = 0;
let latestSpeedActive = false;
let latestTodayTokens = 0;
let animationFrame = 0;
let animatedOutput = 0;
let speedSeen = false;
let speedHistorySeeded = false;
let selectedPetId = readStoredString(petSelectedStorageKey, "bulbasaur");
let petMessageIndex = 0;
let refreshInFlight = null;
let renderedPetId = "";
let renderedSkillTier = 0;
let petViewer = null;
let petViewerPetId = "";

const petCatalog = POKEMON_CATALOG;

window.addEventListener("keydown", function (event) {
  if (event.key === "Escape") hidePanel();
});

function readStoredString(key, fallback) {
  try {
    return localStorage.getItem(key) || fallback;
  } catch (_) {
    return fallback;
  }
}

function writeStoredValue(key, value) {
  try {
    localStorage.setItem(key, String(value));
  } catch (_) {}
}

function asJSON(value) {
  return typeof value === "string" ? JSON.parse(value) : value;
}

function compactTokenRate(value) {
  const number = Number(value);
  if (!Number.isFinite(number) || number <= 0) return "0";
  if (number < 10) {
    const rounded = Math.round(number * 10) / 10;
    return rounded >= 10 ? "10" : rounded.toFixed(1);
  }
  if (number < 1000) return String(Math.round(number));
  const units = [
    { value: 1e9, suffix: "B" },
    { value: 1e6, suffix: "M" },
    { value: 1e3, suffix: "k" },
  ];
  for (const unit of units) {
    const scaled = number / unit.value;
    if (scaled >= 1) {
      return (scaled >= 100 ? scaled.toFixed(0) : scaled.toFixed(2)) + unit.suffix;
    }
  }
  return String(Math.round(number));
}

function compactTokenCount(value) {
  const number = Math.max(0, Number(value) || 0);
  if (number >= 1e8) {
    const scaled = number / 1e8;
    return (scaled >= 10 ? scaled.toFixed(1) : scaled.toFixed(2))
      .replace(/\.0+$/, "") + " 亿";
  }
  if (number >= 1e4) {
    const scaled = number / 1e4;
    return (scaled >= 100 ? scaled.toFixed(0) : scaled.toFixed(1))
      .replace(/\.0$/, "") + " 万";
  }
  return String(Math.round(number));
}

function renderPanelTitle(view) {
  const themeId = normalizeTitleTheme(view && view.title_theme_id);
  const level = titleLevelForSpeed(
    view && view.output_tokens_per_second,
    !!(view && view.active)
  );
  const progress = titleLevelProgress(
    view && view.output_tokens_per_second,
    !!(view && view.active)
  );
  document.getElementById("panel-speed-icon").innerHTML =
    titleIconMarkup(themeId, level, {
      active: !!(view && view.active),
      compact: true,
      progress: progress,
    });
  document.getElementById("panel-speed").style.setProperty(
    "--title-progress",
    progress.toFixed(1) + "%"
  );
}

function sourceLogoNode(source) {
  const value = String(source || "").toLowerCase();
  const logo = document.createElement("span");
  logo.className = "source-logo";
  logo.setAttribute("aria-hidden", "true");
  if (providerIconSpec(value)) {
    return populateProviderIcon(logo, { id: value });
    } else if (value === "dsh" || value.indexOf("deepseek") >= 0) {
      logo.classList.add("dsh");
      logo.innerHTML = '<svg viewBox="0 0 16 16" fill="none">' +
        '<path d="M2.4 5.6c1.6-1.8 3.1-1.8 4.7 0 1.6 1.8 3.1 1.8 4.7 0 1-.1 1.5.3 1.8.7" ' +
        'stroke="currentColor" stroke-width="1.35" stroke-linecap="round"/>' +
        '<path d="M2.4 9.2c1.6-1.8 3.1-1.8 4.7 0 1.6 1.8 3.1 1.8 4.7 0" ' +
        'stroke="currentColor" stroke-width="1.35" stroke-linecap="round"/>' +
        '<path d="M2.4 12.6c1.6-1.6 3.1-1.6 4.7 0" ' +
        'stroke="currentColor" stroke-width="1.2" stroke-linecap="round" opacity=".55"/></svg>';
  } else if (value.indexOf("claude") >= 0) {
    logo.classList.add("claude");
    logo.innerHTML = '<svg viewBox="0 0 16 16" fill="none">' +
      '<path d="M8 2.2v11.6M2.2 8h11.6M3.9 3.9l8.2 8.2M12.1 3.9 3.9 12.1" ' +
      'stroke="currentColor" stroke-width="1.35" stroke-linecap="round"/></svg>';
  } else if (value.indexOf("gemini") >= 0) {
    logo.classList.add("gemini");
    logo.innerHTML = '<svg viewBox="0 0 16 16" fill="none">' +
      '<path d="M8 1.8c.6 3.2 2.4 5 5.6 5.6-3.2.6-5 2.4-5.6 5.6-.6-3.2-2.4-5-5.6-5.6C5.6 6.8 7.4 5 8 1.8Z" ' +
      'stroke="currentColor" stroke-width="1.25" stroke-linejoin="round"/></svg>';
  } else {
    logo.classList.add("codex");
    logo.innerHTML = '<svg viewBox="0 0 16 16" fill="none">' +
      '<path d="m8 1.9 5.2 3v6.2L8 14.1l-5.2-3V4.9L8 1.9Z" stroke="currentColor" stroke-width="1.25"/>' +
      '<path d="m5.7 8 1.5 1.5L10.4 6" stroke="currentColor" stroke-width="1.35" ' +
      'stroke-linecap="round" stroke-linejoin="round"/></svg>';
  }
  return logo;
}

function providerMarkNode(provider) {
  const mark = document.createElement("span");
  mark.className = "provider-mark";
  mark.setAttribute("aria-hidden", "true");
  return populateProviderIcon(mark, provider);
}

function activateTab(tabId, options) {
  const selected = panelTabIds.indexOf(tabId) >= 0 ? tabId : "checkin";
  const settings = options || {};
  activeTab = selected;
  document.getElementById("refresh").hidden = selected === "speed";
  document.querySelectorAll("[data-panel-tab]").forEach(function (tab) {
    const active = tab.dataset.panelTab === selected;
    tab.classList.toggle("active", active);
    tab.setAttribute("aria-selected", active ? "true" : "false");
    tab.tabIndex = active ? 0 : -1;
  });
  panelTabIds.forEach(function (id) {
    const page = document.getElementById("page-" + id);
    const active = id === selected;
    page.hidden = !active;
    page.classList.toggle("active", active);
  });
  if (petViewer) petViewer.setEnabled(selected === "pet");
  if (selected === "pet") {
    resetPetGaze();
    renderPet();
  }
  if (settings.persist !== false) writeStoredValue(panelTabStorageKey, selected);
  updateFooter();
  if (settings.focus) {
    const tab = document.getElementById("tab-" + selected);
    if (tab) tab.focus();
  }
  if (settings.load === true) {
    if (selected === "speed") refreshSpeed();
    if (selected === "checkin") refresh();
  }
}

document.querySelectorAll("[data-panel-tab]").forEach(function (tab) {
  tab.addEventListener("click", function () {
    activateTab(tab.dataset.panelTab, { load: true });
  });
  tab.addEventListener("keydown", function (event) {
    const current = panelTabIds.indexOf(tab.dataset.panelTab);
    let next = current;
    if (event.key === "ArrowRight") next = (current + 1) % panelTabIds.length;
    else if (event.key === "ArrowLeft") next = (current - 1 + panelTabIds.length) % panelTabIds.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = panelTabIds.length - 1;
    else return;
    event.preventDefault();
    activateTab(panelTabIds[next], { load: true, focus: true });
  });
});

function stateClass(state) {
  if (!state) return "wait";
  if (state.indexOf("failed") >= 0) return "bad";
  if (state === "signed_confirmed" || state === "already" ||
      state === "completed" || state === "ineligible" || state === "inactive") return "good";
  return "warn";
}

function stateLabel(state) {
  const labels = {
    signed_confirmed: "已到账",
    signed_provisional: "待复核",
    already: "已领取",
    ineligible: "不符合资格",
    inactive: "活动已结束",
    manual_required: "需 App 签到",
    failed_retryable: "可重试",
    failed_final: "失败",
    completed: "已完成",
  };
  return labels[state] || "待执行";
}

function quotaNumber(value) {
  const n = Number(value);
  if (!Number.isFinite(n)) return "—";
  return n.toLocaleString("zh-CN", { maximumFractionDigits: 2 });
}

function displayAccountIdentity(value) {
  const text = String(value || "").trim();
  if (!text) return "账号";
  return text.indexOf("\uFFFD") >= 0 ? "账号信息待刷新" : text;
}

function syncPanelCollapseButton() {
  const button = document.getElementById("checkin-collapse-all");
  if (!button) return;
  const allCollapsed = currentTargets.length > 0 &&
    currentTargets.every(function (target) { return expanded[target] === false; });
  button.textContent = allCollapsed ? "全部展开" : "全部收起";
  button.setAttribute("aria-pressed", allCollapsed ? "true" : "false");
  button.disabled = currentTargets.length === 0;
}

function openCheckin(payload) {
  Events.Emit("open-checkin", payload || {});
  hidePanel();
}

function primaryOperation(account) {
  const operations = account.operations || [];
  return operations.find(function (operation) {
    return operation.operation === "checkin";
  }) || null;
}

function summaryFor(accounts) {
  if (!accounts.length) return { cls: "warn", title: "暂无账号", sub: "打开主窗口添加" };
  let pending = 0;
  let failed = 0;
  let done = 0;
  accounts.forEach(function (account) {
    if (account.source_health === "reauth_required") {
      pending++;
      return;
    }
    const operation = primaryOperation(account);
    if (!operation) return;
    const cls = stateClass(operation.state);
    if (cls === "bad") failed++;
    else if (cls === "warn") pending++;
    else if (cls === "good") done++;
  });
  if (failed) return { cls: "bad", title: failed + " 个失败", sub: "需要处理" };
  if (pending) return { cls: "warn", title: pending + " 个待处理", sub: "需要确认" };
  if (done) return { cls: "good", title: "今日完成", sub: accounts.length + " 个账号" };
  return { cls: "warn", title: "待巡检", sub: accounts.length + " 个账号" };
}

function needsAttention(account) {
  if (account.source_health === "reauth_required") return true;
  const operation = primaryOperation(account);
  return !!operation && (operation.state === "manual_required" ||
    operation.state === "failed_retryable" || operation.state === "failed_final");
}

function accountNode(account) {
  const node = document.createElement("div");
  const operation = primaryOperation(account);
  const state = (operation && operation.state) || "";
  node.className = "account" + (stateClass(state) === "bad" ? " bad" : "");
  node.dataset.accountKey = account.account_id || account.id || "";
  node.tabIndex = 0;
  node.setAttribute("role", "link");
  node.setAttribute("aria-label", "打开主窗口查看账号 " +
    displayAccountIdentity(account.masked_identity || account.display_name));

  const copy = document.createElement("div");
  const title = document.createElement("div");
  title.className = "acct-title";
  const dot = document.createElement("i");
  dot.className = "dot " +
    (account.source_health === "reauth_required" ? "warn" : stateClass(state));
  title.appendChild(dot);
  title.appendChild(document.createTextNode(
    displayAccountIdentity(account.masked_identity || account.display_name)
  ));
  copy.appendChild(title);

  const meta = document.createElement("div");
  meta.className = "acct-meta";
  if (account.source_health === "reauth_required") {
    meta.textContent = "登录已失效，需重新登录";
  } else if (account.source_health === "healthy") {
    meta.textContent = "受管来源 · 健康";
  } else {
    meta.textContent = "受管来源 · 待检查";
  }
  if (operation && operation.balance != null) {
    meta.textContent += " · 额度 " + quotaNumber(operation.balance);
  }
  copy.appendChild(meta);
  const operations = account.operations || [];
  if (operations.length) {
    const strip = document.createElement("div");
    strip.className = "acct-ops";
    operations.forEach(function (entry) {
      const chip = document.createElement("span");
      chip.className = "op-chip " + stateClass(entry.state);
      chip.textContent = (opLabels[entry.operation] || "操作") + " · " +
        stateLabel(entry.state);
      strip.appendChild(chip);
    });
    copy.appendChild(strip);
  }
  node.appendChild(copy);

  const action = document.createElement("button");
  action.className = "mini-action" + (stateClass(state) === "bad" ? " danger" : "");
  action.type = "button";
  action.textContent = account.source_health === "reauth_required" ? "处理" :
    state === "manual_required" ? "复查" : "证据";
  action.addEventListener("click", function (event) {
    event.stopPropagation();
    openCheckin({
      target: account.target,
      account_key: account.account_id || account.id || "",
      scope: needsAttention(account) ? "attention" : "account",
    });
  });
  node.appendChild(action);
  function activateAccount() {
    openCheckin({
      target: account.target,
      account_key: account.account_id || account.id || "",
      scope: needsAttention(account) ? "attention" : "account",
    });
  }
  node.addEventListener("click", function (event) {
    if (event.target.closest("button")) return;
    activateAccount();
  });
  node.addEventListener("keydown", function (event) {
    if (event.target !== node || (event.key !== "Enter" && event.key !== " ")) return;
    event.preventDefault();
    activateAccount();
  });
  return node;
}

function fillEmpty(container, title, detail) {
  container.innerHTML = "";
  const empty = document.createElement("div");
  empty.className = "empty";
  const heading = document.createElement("b");
  heading.textContent = title;
  empty.appendChild(heading);
  empty.appendChild(document.createTextNode(detail));
  container.appendChild(empty);
}

function providerForTarget(target) {
  return providerInfo[target] || { id: target, name: target, icon: "" };
}

function render(view) {
  currentAccounts = view.accounts || [];
  const box = document.getElementById("targets");
  box.innerHTML = "";

  const attention = currentAccounts.filter(needsAttention).length;
  const completed = currentAccounts.filter(function (account) {
    if (account.source_health === "reauth_required") return false;
    const operation = primaryOperation(account);
    return !!operation && stateClass(operation.state) === "good";
  }).length;
  const summaryData = {
    total: currentAccounts.length,
    done: completed,
    attention: attention,
  };
  document.getElementById("summary-total").innerHTML =
    summaryData.total + "<small>个</small>";
  document.getElementById("summary-done").innerHTML =
    summaryData.done + "<small>个</small>";
  document.getElementById("summary-attention").innerHTML =
    summaryData.attention + "<small>个</small>";
  const badge = document.getElementById("checkin-badge");
  badge.hidden = summaryData.attention === 0;
  badge.textContent = String(summaryData.attention);
  document.getElementById("checkin-updated").textContent =
    "更新于 " + new Date().toLocaleTimeString("zh-CN", { hour12: false });

  if (!currentAccounts.length) {
    box.classList.add("empty-list");
    fillEmpty(
      box,
      "还没有已启用账号",
      "打开主窗口，在「签到管理」中添加或启用供应商账号。"
    );
  } else {
    box.classList.remove("empty-list");
  }

  const grouped = {};
  currentAccounts.forEach(function (account) {
    (grouped[account.target] = grouped[account.target] || []).push(account);
  });

  currentTargets = Object.keys(grouped).sort();
  currentTargets.forEach(function (target) {
    const group = grouped[target];
    const provider = providerForTarget(target);
    if (expanded[target] === undefined) expanded[target] = true;
    const app = document.createElement("section");
    app.className = "app" + (expanded[target] ? " open" : "");
    app.dataset.target = target;
    const head = document.createElement("button");
    head.type = "button";
    head.className = "app-head";
    head.setAttribute("aria-expanded", expanded[target] ? "true" : "false");
    head.appendChild(providerMarkNode(provider));
    const who = document.createElement("div");
    who.className = "who";
    const name = document.createElement("div");
    name.className = "name";
    name.textContent = provider.name || provider.id || target;
    const sub = document.createElement("div");
    sub.className = "sub";
    sub.textContent = "签到 · " + group.length + " 个账号";
    who.appendChild(name);
    who.appendChild(sub);
    head.appendChild(who);

    const summary = summaryFor(group);
    const state = document.createElement("div");
    state.className = "summary " + summary.cls;
    const title = document.createElement("b");
    title.textContent = summary.title;
    const detail = document.createElement("small");
    detail.textContent = summary.sub;
    state.appendChild(title);
    state.appendChild(detail);
    head.appendChild(state);

    const chevron = document.createElement("span");
    chevron.className = "chev";
    chevron.innerHTML = '<svg width="12" height="12" viewBox="0 0 16 16" fill="none">' +
      '<path d="m3 6 5 5 5-5" stroke="currentColor" stroke-width="1.5" ' +
      'stroke-linecap="round"/></svg>';
    head.appendChild(chevron);
    head.addEventListener("click", function () {
      expanded[target] = !expanded[target];
      app.classList.toggle("open", expanded[target]);
      head.setAttribute("aria-expanded", expanded[target] ? "true" : "false");
      syncPanelCollapseButton();
    });
    app.appendChild(head);

    const accountList = document.createElement("div");
    accountList.className = "accounts";
    group.forEach(function (account) {
      accountList.appendChild(accountNode(account));
    });
    app.appendChild(accountList);
    box.appendChild(app);
  });
  syncPanelCollapseButton();

  const alert = document.getElementById("alert");
  alert.hidden = attention === 0;
  if (attention) {
    document.getElementById("alert-title").textContent = attention + " 个账号需要处理";
    document.getElementById("alert-detail").textContent =
      "登录失效或签到失败，请打开主窗口查看详情。";
  }
  updateFooter();
}

function showMessage(text, success) {
  const node = document.getElementById("message");
  node.textContent = text;
  node.className = "message" + (success ? " success" : "");
  node.hidden = false;
}

async function loadProviderInfo() {
  Object.keys(providerInfo).forEach(function (key) {
    delete providerInfo[key];
  });
  try {
    const view = asJSON(await Bound.ProvidersList());
    (view.providers || []).forEach(function (provider) {
      providerInfo[provider.id] = provider;
    });
  } catch (_) {}
}

function refresh() {
  if (refreshInFlight) return refreshInFlight;
  const button = document.getElementById("refresh");
  button.disabled = true;
  refreshInFlight = (async function () {
    try {
      const status = asJSON(await Bound.Status());
      await loadProviderInfo();
      render(status);
    } catch (_) {
      fillEmpty(
        document.getElementById("targets"),
        "加载失败",
        "请稍后重试，或打开主窗口检查供应商状态。"
      );
    } finally {
      button.disabled = false;
      refreshInFlight = null;
    }
  })();
  return refreshInFlight;
}

function renderSparkline() {
  const sparkline = document.getElementById("panel-speed-sparkline");
  sparkline.innerHTML = "";
  const max = Math.max.apply(null, speedHistory.concat([1]));
  speedHistory.forEach(function (value, index) {
    const bar = document.createElement("i");
    const ratio = Math.max(0.08, value / max);
    bar.style.height = Math.round(ratio * 100) + "%";
    bar.classList.toggle("hot", index === speedHistory.length - 1 && value > 0);
    sparkline.appendChild(bar);
  });
}

function seedSpeedHistory(output) {
  if (speedHistorySeeded || output <= 0) return;
  const shape = [
    0.14, 0.23, 0.19, 0.38, 0.31, 0.52, 0.42, 0.67,
    0.54, 0.76, 0.62, 0.84, 0.71, 0.93, 0.80, 0.88,
    0.73, 0.96, 0.82, 1, 0.91, 0.97, 0.89, 1,
  ];
  shape.forEach(function (ratio, index) {
    speedHistory[index] = output * ratio;
  });
  speedHistorySeeded = true;
}

function renderSpeedDirectory(sources, sessions, idleCount) {
  const list = document.getElementById("panel-sources");
  list.innerHTML = "";
  const sourceRows = Array.isArray(sources) ? sources : [];
  const sessionRows = Array.isArray(sessions) ? sessions : [];
  document.getElementById("panel-source-count").textContent =
    sourceRows.length + " 个 Agent";
  document.getElementById("panel-session-count").textContent =
    sessionRows.length ? "最近 10 分钟 · " + sessionRows.length + " 个会话" : "暂无近期会话";
  if (!sourceRows.length && !sessionRows.length) {
    const empty = document.createElement("div");
    empty.className = "agent-empty";
    empty.textContent = "未发现本机 Agent 数据源";
    list.appendChild(empty);
    return;
  }

  const sessionsBySource = new Map();
  sessionRows.forEach(function (session) {
    const key = String(session.source || "");
    if (!sessionsBySource.has(key)) sessionsBySource.set(key, []);
    sessionsBySource.get(key).push(session);
  });

  function renderAgentHead(options) {
    const hasSessions = options.related.length > 0;
    const stateKey = options.stateKey;
    const expanded = hasSessions
      ? (agentExpansion.has(stateKey)
        ? agentExpansion.get(stateKey)
        : options.generating || options.related.some(function (session) {
          return !!session.generating;
        }))
      : false;
    const head = document.createElement(hasSessions ? "button" : "div");
    head.className = "agent-head" + (hasSessions ? " agent-toggle" : "");
    if (hasSessions) {
      head.type = "button";
      head.setAttribute("aria-expanded", expanded ? "true" : "false");
      head.setAttribute("aria-controls", options.sessionsId);
      head.setAttribute(
        "aria-label",
        (expanded ? "收起 " : "展开 ") + options.label + " 的 " +
          options.related.length + " 个会话"
      );
    }
    const identity = document.createElement("div");
    identity.className = "agent-identity";
    identity.appendChild(sourceLogoNode(options.logoKey));
    const copy = document.createElement("div");
    copy.className = "agent-copy";
    const name = document.createElement("div");
    name.className = "agent-name";
    if (options.generating) {
      const dot = document.createElement("i");
      dot.className = "source-live-dot active";
      name.appendChild(dot);
    } else if (options.showIdleDot) {
      const dot = document.createElement("i");
      dot.className = "source-live-dot";
      name.appendChild(dot);
    }
    const label = document.createElement("span");
    label.textContent = options.label;
    name.appendChild(label);
    const sub = document.createElement("div");
    sub.className = "agent-sub";
    sub.textContent = options.subText;
    copy.appendChild(name);
    copy.appendChild(sub);
    identity.appendChild(copy);
    head.appendChild(identity);

    const side = document.createElement("div");
    side.className = "agent-head-side";
    const rate = document.createElement("div");
    rate.className = "agent-rate";
    const strong = document.createElement("b");
    strong.textContent = compactTokenRate(options.rate);
    rate.appendChild(strong);
    rate.appendChild(document.createTextNode(" tok/s"));
    if (options.rateSub) {
      const rateSub = document.createElement("small");
      rateSub.textContent = options.rateSub;
      rate.appendChild(rateSub);
    }
    side.appendChild(rate);
    if (hasSessions) {
      const chevron = document.createElement("span");
      chevron.className = "agent-chevron";
      chevron.setAttribute("aria-hidden", "true");
      chevron.innerHTML = '<svg viewBox="0 0 16 16" fill="none">' +
        '<path d="M3.4 5.8 8 10.2l4.6-4.4" stroke="currentColor" ' +
        'stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/></svg>';
      side.appendChild(chevron);
    }
    head.appendChild(side);

    const body = appendSessionRows(options.related, options.sessionsId);
    if (!hasSessions) body.hidden = true;
    if (hasSessions && !expanded) body.hidden = true;
    const toggle = function () {
      const next = head.getAttribute("aria-expanded") !== "true";
      agentExpansion.set(stateKey, next);
      head.setAttribute("aria-expanded", next ? "true" : "false");
      head.setAttribute(
        "aria-label",
        (next ? "收起 " : "展开 ") + options.label + " 的 " +
          options.related.length + " 个会话"
      );
      body.hidden = !next;
      const card = body.closest(".agent-card");
      if (card) card.classList.toggle("is-collapsed", !next);
    };
    return {
      head: head,
      body: body,
      expanded: expanded,
      hasSessions: hasSessions,
      toggle: toggle,
    };
  }

  function appendSessionRows(related, sessionsId) {
    const nested = document.createElement("div");
    nested.className = "agent-sessions";
    nested.id = sessionsId;
    related
      .slice()
      .sort(function (a, b) {
        return Number(!!b.generating) - Number(!!a.generating);
      })
      .forEach(function (session) {
        const item = document.createElement("div");
        item.className = "agent-session";
        const main = document.createElement("div");
        main.className = "agent-session-main";
        const sessionDot = document.createElement("i");
        sessionDot.className = "source-live-dot" +
          (session.generating ? " active" : "");
        main.appendChild(sessionDot);
        const sessionCopy = document.createElement("div");
        sessionCopy.className = "agent-session-copy";
        const sessionName = document.createElement("span");
        sessionName.className = "agent-session-name";
        sessionName.textContent = session.label || "会话";
        const meta = document.createElement("span");
        meta.className = "agent-session-meta";
        meta.textContent = [
          session.model || "模型未知",
          session.generating ? "生成中" : "最近活跃",
        ].join(" · ");
        sessionCopy.appendChild(sessionName);
        sessionCopy.appendChild(meta);
        main.appendChild(sessionCopy);
        item.appendChild(main);
        const sessionRate = document.createElement("span");
        sessionRate.className = "agent-session-rate";
        sessionRate.textContent =
          compactTokenRate(session.output_tokens_per_second) + " tok/s";
        item.appendChild(sessionRate);
        nested.appendChild(item);
      });
    return nested;
  }

  // 整行空白（含卡片内边距）都可折叠；会话明细行本身不触发折叠。
  function bindAgentCardToggle(row, rendered) {
    if (!rendered.hasSessions) return;
    row.addEventListener("click", function (event) {
      const target = event.target;
      if (!target || !target.closest) return;
      if (target.closest(".agent-sessions")) return;
      rendered.toggle();
    });
  }

  sourceRows.forEach(function (source) {
    const sourceKey = String(source.source || "");
    const related = sessionsBySource.get(sourceKey) || [];
    sessionsBySource.delete(sourceKey);
    const row = document.createElement("article");
    row.className = "agent-card" +
      (Number(source.generating_agents) > 0 ? " generating" : "");
    const sessionsId = "agent-sessions-" + sourceKey.replace(/[^a-z0-9_-]/gi, "-");
    const rendered = renderAgentHead({
      stateKey: sourceKey,
      sessionsId: sessionsId,
      logoKey: source.source || source.label,
      label: source.label || source.source || "Agent",
      subText: "今日 " + compactTokenCount(Number(source.today_total_tokens) || 0) +
        (related.length ? " · " + related.length + " 个会话" : ""),
      rate: source.output_tokens_per_second,
      rateSub: Number(source.generating_agents) > 0 ? "正在生成" :
        (Number(source.today_total_tokens) > 0 ? "今日已用" : "待使用"),
      generating: Number(source.generating_agents) > 0,
      showIdleDot: true,
      related: related,
    });
    row.appendChild(rendered.head);
    row.appendChild(rendered.body);
    bindAgentCardToggle(row, rendered);
    if (!rendered.expanded && related.length) row.classList.add("is-collapsed");
    list.appendChild(row);
  });

  sessionsBySource.forEach(function (orphanSessions, sourceKey) {
    const row = document.createElement("article");
    row.className = "agent-card";
    const generating = orphanSessions.some(function (item) { return !!item.generating; });
    const sessionsId = "agent-sessions-orphan-" +
      sourceKey.replace(/[^a-z0-9_-]/gi, "-");
    const rendered = renderAgentHead({
      stateKey: sourceKey,
      sessionsId: sessionsId,
      logoKey: sourceKey,
      label: orphanSessions[0] && orphanSessions[0].source_label || "其他 Agent",
      subText: orphanSessions.length + " 个近期会话",
      rate: orphanSessions.reduce(function (total, item) {
      return total + (Number(item.output_tokens_per_second) || 0);
      }, 0),
      rateSub: generating ? "正在生成" : "近期活跃",
      generating: generating,
      showIdleDot: false,
      related: orphanSessions,
    });
    row.appendChild(rendered.head);
    row.appendChild(rendered.body);
    bindAgentCardToggle(row, rendered);
    if (!rendered.expanded) row.classList.add("is-collapsed");
    list.appendChild(row);
  });

  // 今天没用过的 Agent 不再各占一张空卡片，只在末尾用一行提示数量。
  if (!list.children.length) {
    const empty = document.createElement("p");
    empty.className = "panel-sources-empty";
    empty.textContent = "今天还没有 Agent 使用记录，开始对话后这里会出现";
    list.appendChild(empty);
  } else if (Number(idleCount) > 0) {
    const hint = document.createElement("p");
    hint.className = "panel-sources-empty";
    hint.textContent = "另有 " + Number(idleCount) + " 个已配置的 Agent 今天未使用";
    list.appendChild(hint);
  }
}

function formatUpdated(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

function animateOutput(target) {
  if (animationFrame) cancelAnimationFrame(animationFrame);
  const reduced = window.matchMedia &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (!speedSeen || reduced) {
    animatedOutput = target;
    document.getElementById("panel-speed-output").textContent = compactTokenRate(target);
    speedSeen = true;
    return;
  }
  const startValue = animatedOutput;
  const started = performance.now();
  function step(now) {
    const progress = Math.min(1, (now - started) / 360);
    const eased = 1 - Math.pow(1 - progress, 3);
    animatedOutput = startValue + (target - startValue) * eased;
    document.getElementById("panel-speed-output").textContent =
      compactTokenRate(animatedOutput);
    if (progress < 1) animationFrame = requestAnimationFrame(step);
    else animationFrame = 0;
  }
  animationFrame = requestAnimationFrame(step);
}

function renderSpeed(raw) {
  const view = raw ? asJSON(raw) : null;
  const card = document.getElementById("panel-speed");
  const output = Number(view && view.output_tokens_per_second) || 0;
  const previousOutput = latestSpeedOutput;
  latestSpeedOutput = output;
  latestSpeedActive = !!(view && view.active);
  latestTodayTokens = Math.max(0, Number(view && view.today_total_tokens) || 0);
  card.classList.toggle("active", latestSpeedActive);

  if (!view) {
    document.getElementById("panel-speed-state").textContent = "速度读取失败";
    document.getElementById("panel-speed-title").textContent = "待机";
    document.getElementById("panel-speed-theme").textContent = "交通工具";
    document.getElementById("panel-speed-updated").textContent = "—";
    document.getElementById("panel-speed-agents").textContent = "0";
    document.getElementById("panel-speed-today").textContent = "0";
    document.getElementById("panel-source-count").textContent = "0 个 Agent";
    document.getElementById("panel-session-count").textContent = "暂无近期会话";
    renderPanelTitle(null);
    renderSpeedDirectory([], []);
    const badge = document.getElementById("speed-badge");
    badge.hidden = true;
    speedHistory.shift();
    speedHistory.push(0);
    renderSparkline();
    animateOutput(0);
    updatePetForDailyTokens(0);
    return;
  }

  const generating = Number(view.generating_agents) || 0;
  document.getElementById("panel-speed-state").textContent =
    generating > 0 ? "生成中 " + generating :
      (view.active ? "最近一次响应已完成解码" : "当前空闲");
  document.getElementById("panel-speed-updated").textContent =
    formatUpdated(view.updated_at);
  document.getElementById("panel-speed-title").textContent = view.title || "待机";
  document.getElementById("panel-speed-theme").textContent =
    view.title_theme || "交通工具";
  document.getElementById("panel-speed-agents").textContent = String(generating);
  document.getElementById("panel-speed-today").textContent =
    compactTokenCount(Number(view.today_total_tokens) || 0);
  renderPanelTitle(view);
  renderSpeedDirectory(view.sources || [], view.sessions || [],
    Number(view.idle_source_count) || 0);
  const badge = document.getElementById("speed-badge");
  badge.hidden = output <= 0 && generating === 0;
  badge.textContent = generating > 0 ? String(generating) : compactTokenRate(output);
  if (output > previousOutput + 0.5 && latestSpeedActive) {
    card.classList.remove("rate-bump");
    void card.offsetWidth;
    card.classList.add("rate-bump");
  }
  card.addEventListener("animationend", function () {
    card.classList.remove("rate-bump");
  }, { once: true });
  seedSpeedHistory(output);
  speedHistory.shift();
  speedHistory.push(output);
  renderSparkline();
  animateOutput(output);
  updatePetForDailyTokens(latestTodayTokens);
  updateFooter();
}

async function refreshSpeed() {
  try {
    renderSpeed(await Bound.TokenSpeedGet());
  } catch (_) {
    renderSpeed(null);
  }
}

function updateFooter() {
  const count = document.getElementById("count");
  const run = document.getElementById("run");
  if (activeTab === "speed") {
    count.textContent = "自动刷新";
    run.hidden = true;
    return;
  }
  run.hidden = false;
  if (activeTab === "pet") {
    const tier = currentSkillTier();
    count.innerHTML = "<b>" + romanStage(tier) + "</b> / III 技能";
    run.textContent = "放技能";
    run.setAttribute("aria-label", "让当前宝可梦伙伴释放技能");
    return;
  }
  count.innerHTML = "<b>" + currentAccounts.length + "</b> 个账号";
  run.textContent = "巡检";
  run.setAttribute("aria-label", "立即执行今日巡检");
}

function currentPet() {
  return pokemonById(selectedPetId) || POKEMON_CATALOG[0];
}

function currentSkillTier() {
  return evolutionStageForDailyTokens(latestTodayTokens);
}

function romanStage(stage) {
  return ["I", "II", "III"][Math.max(0, Math.min(2, stage - 1))];
}

function evolutionThreshold(stage) {
  return POKEMON_EVOLUTION_THRESHOLDS[Math.max(0, Math.min(2, stage - 1))];
}

function petEffectIntensity() {
  const tier = currentSkillTier();
  if (tier < 3) return tier;
  const strengthened = Math.max(
    0,
    Math.min(
      1,
      (latestTodayTokens - POKEMON_EVOLUTION_THRESHOLDS[2]) /
        Math.max(1, POKEMON_MAX_DAILY_TOKENS - POKEMON_EVOLUTION_THRESHOLDS[2])
    )
  );
  return 3 + Math.round(strengthened * 2);
}

function ensurePetViewer(pet) {
  const avatar = document.getElementById("pet-avatar");
  if (petViewer && petViewerPetId === pet.id) {
    petViewer.setEffectIntensity(petEffectIntensity());
    return;
  }

  if (petViewer) petViewer.dispose();
  petViewer = null;
  petViewerPetId = pet.id;
  avatar.innerHTML =
    '<canvas class="pokemon-3d-canvas" aria-hidden="true"></canvas>' + pet.art;
  avatar.className = "pet-avatar pet-" + pet.id + " is-loading";
  const canvas = avatar.querySelector(".pokemon-3d-canvas");
  petViewer = createPokemon3D(canvas, {
    models: pet.stages.map(function (entry) { return entry.model; }),
    boneAdjust: pet.stages[0].pose || null,
    modelRotations: pet.stages.map(function (entry) { return entry.rotation || null; }),
    stage: 1,
    enabled: activeTab === "pet",
    effectIntensity: petEffectIntensity(),
    onLoading: function (loading) {
      if (petViewerPetId === pet.id) {
        avatar.classList.toggle("is-loading", loading !== false);
      }
    },
    onReady: function () {
      if (petViewerPetId !== pet.id) return;
      avatar.classList.remove("is-loading", "model-fallback");
      avatar.classList.add("is-3d");
      if (latestPointer.x !== null && latestPointer.y !== null) {
        updatePetGaze(latestPointer.x, latestPointer.y);
      }
    },
    onUnavailable: function () {
      if (petViewerPetId !== pet.id) return;
      avatar.classList.remove("is-3d", "is-loading");
      avatar.classList.add("model-fallback");
    },
    onError: function () {
      if (petViewerPetId !== pet.id) return;
      avatar.classList.remove("is-3d", "is-loading");
      avatar.classList.add("model-fallback");
    },
  });
}

function renderPetAvatar(pet) {
  const avatar = document.getElementById("pet-avatar");
  ensurePetViewer(pet);
  avatar.dataset.renderedPet = pet.id;
  avatar.dataset.evolution = "1";
  if (latestPointer.x !== null && latestPointer.y !== null) {
    requestAnimationFrame(function () {
      updatePetGaze(latestPointer.x, latestPointer.y);
    });
  }
}

function renderPet() {
  const pet = currentPet();
  const tier = currentSkillTier();
  const previousId = renderedPetId;
  const previousTier = renderedSkillTier;
  const changedTier = previousId === pet.id && previousTier > 0 &&
    tier !== previousTier;

  selectedPetId = pet.id;
  const stageNode = document.getElementById("pet-stage");
  stageNode.dataset.pet = pet.id;
  stageNode.dataset.evolution = "1";
  stageNode.style.setProperty("--pet-accent", pet.accent);
  renderPetAvatar(pet);
  renderedPetId = pet.id;
  renderedSkillTier = tier;

  document.getElementById("pet-name").textContent = pet.name;
  document.getElementById("pet-subtitle").textContent = pet.subtitle;
  document.getElementById("pet-current-speed").innerHTML =
    compactTokenRate(latestSpeedOutput) + " <small>tok/s</small>";
  document.getElementById("pet-today-tokens").textContent =
    compactTokenCount(latestTodayTokens);
  document.getElementById("pet-level").innerHTML =
    romanStage(tier) + "<small>技能等级</small>";
  document.getElementById("pet-stage-label").innerHTML =
    romanStage(tier) + " <small>/ III 技能</small>";

  const nextTier = tier < 3 ? tier + 1 : 0;
  if (nextTier) {
    const from = evolutionThreshold(tier);
    const to = evolutionThreshold(nextTier);
    const progress = Math.max(
      3,
      Math.min(100, (latestTodayTokens - from) / Math.max(1, to - from) * 100)
    );
    document.getElementById("pet-progress-title").textContent =
      "下一技能等级 · " + romanStage(nextTier);
    document.getElementById("pet-progress-copy").textContent =
      compactTokenCount(latestTodayTokens) + " / " + compactTokenCount(to) + " Token";
    document.getElementById("pet-progress").style.width = progress + "%";
  } else {
    const capped = latestTodayTokens >= POKEMON_MAX_DAILY_TOKENS;
    const progress = capped
      ? 100
      : Math.max(
        5,
        Math.min(
          100,
          (latestTodayTokens - POKEMON_EVOLUTION_THRESHOLDS[2]) /
            Math.max(1, POKEMON_MAX_DAILY_TOKENS - POKEMON_EVOLUTION_THRESHOLDS[2]) *
            100
        )
      );
    document.getElementById("pet-progress-title").textContent =
      capped ? "技能特效已达顶峰" : "技能特效强化中";
    document.getElementById("pet-progress-copy").textContent =
      compactTokenCount(latestTodayTokens) + " / " +
      compactTokenCount(POKEMON_MAX_DAILY_TOKENS) + " Token";
    document.getElementById("pet-progress").style.width = progress + "%";
  }

  document.querySelectorAll("button[data-pet-id]").forEach(function (button) {
    const candidate = petCatalog.find(function (entry) {
      return entry.id === button.dataset.petId;
    });
    const current = !!candidate && candidate.id === pet.id;
    button.disabled = false;
    button.classList.toggle("unlocked", !!candidate);
    button.classList.toggle("current", current);
    button.setAttribute("aria-pressed", current ? "true" : "false");
    const badge = document.createElement("span");
    badge.className = "pet-unlock-lock";
    badge.textContent = current ? "使用中" : "切换";
    button.replaceChildren(
      document.createTextNode((candidate && candidate.name) || "伙伴"),
      badge
    );
  });
  updateFooter();
  return { pet, tier, previousTier, changedTier };
}

function updatePetGaze(x, y) {
  const stage = document.getElementById("pet-stage");
  const avatar = document.getElementById("pet-avatar");
  if (!stage || !avatar) return;
  const stageRect = stage.getBoundingClientRect();
  const avatarRect = avatar.getBoundingClientRect();
  const aim = petAimPoint(
    x + stageRect.left,
    y + stageRect.top,
    stageRect,
    avatarRect
  );
  if (!aim) {
    resetPetGaze();
    return;
  }
  const nx = aim.x;
  const ny = aim.y;
  stage.style.setProperty("--pet-look-x", (nx * 5.2).toFixed(1) + "px");
  stage.style.setProperty("--pet-look-y", (ny * 3.2).toFixed(1) + "px");
  stage.style.setProperty("--pet-look-roll", (nx * 1.8).toFixed(1) + "deg");
  if (petViewer) petViewer.setPointer(nx, ny);
}

function resetPetGaze() {
  const stage = document.getElementById("pet-stage");
  if (!stage) return;
  stage.style.setProperty("--pet-look-x", "0px");
  stage.style.setProperty("--pet-look-y", "0px");
  stage.style.setProperty("--pet-look-roll", "0deg");
  if (petViewer) petViewer.resetPointer();
}

function updatePetForDailyTokens(total) {
  const previousTier = renderedSkillTier;
  latestTodayTokens = Math.max(0, Number(total) || 0);
  const rendered = renderPet();
  if (rendered.changedTier && previousTier > 0) {
    const pet = rendered.pet;
    document.getElementById("pet-bubble").textContent = rendered.tier > previousTier
      ? "今日 Token 达到 " + compactTokenCount(evolutionThreshold(rendered.tier)) +
        "，「" + pet.name + "」技能提升到 " + romanStage(rendered.tier) + " 级。"
      : "今日 Token 重置，「" + pet.name + "」技能回到 " +
        romanStage(rendered.tier) + " 级。";
  }
  return rendered;
}

function selectPet(petId) {
  const pet = pokemonById(petId);
  if (!pet) return;
  selectedPetId = pet.id;
  writeStoredValue(petSelectedStorageKey, selectedPetId);
  latestPointer.x = null;
  latestPointer.y = null;
  resetPetGaze();
  clearPetSkill();
  renderPet();
  document.getElementById("pet-bubble").textContent =
    "轮到「" + pet.name + "」啦，技能等级 " + romanStage(currentSkillTier());
}

document.querySelectorAll("button[data-pet-id]").forEach(function (button) {
  button.addEventListener("click", function () {
    selectPet(button.dataset.petId);
  });
});

function clampSkillPoint(value, max) {
  return Math.max(8, Math.min(max - 8, Number(value) || 0));
}

function skillZigzagPath(x1, y1, x2, y2, power) {
  const dx = x2 - x1;
  const dy = y2 - y1;
  const length = Math.max(1, Math.hypot(dx, dy));
  const px = -dy / length;
  const py = dx / length;
  const segments = 7 + power;
  const points = [];
  for (let index = 0; index <= segments; index += 1) {
    const t = index / segments;
    const taper = Math.sin(Math.PI * t);
    const jitter = index === 0 || index === segments
      ? 0
      : (index % 2 ? 1 : -1) * (2.8 + power * .75) * taper;
    points.push([
      x1 + dx * t + px * jitter,
      y1 + dy * t + py * jitter,
    ]);
  }
  return "M" + points.map(function (point) {
    return point[0].toFixed(1) + " " + point[1].toFixed(1);
  }).join(" L");
}

function petSkillSvg(pet, targetX, targetY, rect, power) {
  const width = Math.max(1, rect.width);
  const height = Math.max(1, rect.height);
  const centerX = width * .5;
  const effects = [];
  const leafCount = power;
  const sparkCount = 2 + power * 2;

  if (pet.effect === "leaf") {
    const sourceY = height * .72;
    for (let index = 0; index < leafCount; index += 1) {
      const bend = (index - (leafCount - 1) / 2) * (12 + index * 2);
      const c1x = centerX + bend * .45;
      const c1y = sourceY - height * (.2 + index * .025);
      const c2x = targetX - bend * .65;
      const c2y = targetY + height * .18;
      effects.push(
        '<path class="skill-line skill-vine' + (index === 0 ? " core" : "") + '" ' +
        'style="animation-delay:' + (index * 44) + 'ms" ' +
        'd="M' + centerX.toFixed(1) + " " + sourceY.toFixed(1) +
        " C" + c1x.toFixed(1) + " " + c1y.toFixed(1) +
        " " + c2x.toFixed(1) + " " + c2y.toFixed(1) +
        " " + targetX.toFixed(1) + " " + targetY.toFixed(1) + '"></path>'
      );
    }
    for (let index = 0; index < 3 + power * 2; index += 1) {
      const angle = index * 2.214 + .35;
      const radius = 8 + (index % 4) * 5 + power * 1.7;
      const lx = targetX + Math.cos(angle) * radius;
      const ly = targetY + Math.sin(angle) * radius * .68;
      const rotation = index * 47 - 12;
      effects.push(
        '<ellipse class="skill-node skill-leaf" ' +
        'style="animation-delay:' + (72 + index * 24) + 'ms" ' +
        'cx="' + lx.toFixed(1) + '" cy="' + ly.toFixed(1) + '" ' +
        'rx="' + (4.2 + power * .45).toFixed(1) + '" ry="2.2" ' +
        'transform="rotate(' + rotation + " " + lx.toFixed(1) + " " + ly.toFixed(1) + ')"></ellipse>'
      );
    }
  }

  if (pet.effect === "ember") {
    const sourceX = centerX;
    const sourceY = height * .47;
    const distance = Math.max(34, Math.hypot(targetX - sourceX, targetY - sourceY));
    const count = 2 + power;
    for (let index = 0; index < count; index += 1) {
      const spread = (index - (count - 1) / 2) * 7.5;
      const c1x = sourceX + (targetX - sourceX) * .36 + spread;
      const c1y = sourceY + (targetY - sourceY) * .28 - distance * .13;
      const c2x = sourceX + (targetX - sourceX) * .72 - spread * .45;
      const c2y = sourceY + (targetY - sourceY) * .7 + spread * .35;
      const classes = index === 0 ? " warm core" : (index % 2 ? " warm" : "");
      effects.push(
        '<path class="skill-line skill-flame-stream' + classes + '" ' +
        'style="--skill-origin-x:' + sourceX.toFixed(1) + 'px;' +
        '--skill-origin-y:' + sourceY.toFixed(1) + 'px;animation-delay:' + (index * 30) + 'ms" ' +
        'd="M' + sourceX.toFixed(1) + " " + sourceY.toFixed(1) +
        " Q" + c1x.toFixed(1) + " " + c1y.toFixed(1) +
        " " + targetX.toFixed(1) + " " + targetY.toFixed(1) + '"></path>'
      );
    }
    for (let index = 0; index < sparkCount; index += 1) {
      const sx = targetX + ((index * 37) % 28) - 14;
      const sy = targetY + ((index * 23) % 20) - 10;
      effects.push(
        '<circle class="skill-spark" style="' +
        '--sx:' + (((index * 19) % 22) - 11) + 'px;' +
        '--sy:' + (-10 - (index % 4) * 5) + 'px;' +
        'animation-delay:' + (60 + index * 22) + 'ms" ' +
        'cx="' + sx.toFixed(1) + '" cy="' + sy.toFixed(1) + '" ' +
        'r="' + (1.4 + power * .12).toFixed(1) + '"></circle>'
      );
    }
  }

  if (pet.effect === "water") {
    const sources = [
      [centerX - Math.min(56, width * .16), height * .63],
      [centerX + Math.min(56, width * .16), height * .63],
    ];
    const jetCount = 2 + Math.floor(power / 2);
    for (let index = 0; index < jetCount; index += 1) {
      const source = sources[index % sources.length];
      const lift = (index - (jetCount - 1) / 2) * 8;
      const c1x = source[0] + (targetX - source[0]) * .32;
      const c1y = source[1] - height * (.28 + index * .015);
      const c2x = targetX + lift;
      const c2y = targetY + height * (.12 + index * .012);
      effects.push(
        '<path class="skill-line skill-water-stream' + (index === 0 ? " core" : "") + '" ' +
        'style="animation-delay:' + (index * 34) + 'ms" ' +
        'd="M' + source[0].toFixed(1) + " " + source[1].toFixed(1) +
        " C" + c1x.toFixed(1) + " " + c1y.toFixed(1) +
        " " + c2x.toFixed(1) + " " + c2y.toFixed(1) +
        " " + targetX.toFixed(1) + " " + targetY.toFixed(1) + '"></path>'
      );
    }
    for (let index = 0; index < 4 + power; index += 1) {
      const bx = targetX + ((index * 31) % 36) - 18;
      const by = targetY + ((index * 17) % 20) - 10;
      effects.push(
        '<circle class="skill-node skill-water-node" style="' +
        '--wx:' + (((index * 13) % 18) - 9) + 'px;' +
        '--wy:' + (-6 - (index % 3) * 5) + 'px;' +
        'animation-delay:' + (48 + index * 28) + 'ms" ' +
        'cx="' + bx.toFixed(1) + '" cy="' + by.toFixed(1) + '" ' +
        'r="' + (1.7 + (index % 3) * .75 + power * .08).toFixed(1) + '"></circle>'
      );
    }
  }

  if (pet.effect === "electric") {
    const sourceX = centerX;
    const sourceY = height * .48;
    effects.push(
      '<path class="skill-line skill-bolt core" ' +
      'd="' + skillZigzagPath(sourceX, sourceY, targetX, targetY, power) + '"></path>'
    );
    const branches = Math.max(2, power + 1);
    for (let index = 0; index < branches; index += 1) {
      const t = .28 + (index / branches) * .45;
      const bx = sourceX + (targetX - sourceX) * t;
      const by = sourceY + (targetY - sourceY) * t;
      const angle = -1.1 + index * 1.73;
      const ex = bx + Math.cos(angle) * (16 + power * 4);
      const ey = by + Math.sin(angle) * (11 + power * 3);
      effects.push(
        '<path class="skill-line skill-bolt" ' +
        'style="animation-delay:' + (index * 25) + 'ms" ' +
        'd="' + skillZigzagPath(bx, by, ex, ey, Math.max(1, power - 1)) + '"></path>'
      );
    }
  }

  return '<svg class="pet-skill-vector is-' + pet.effect + '" viewBox="0 0 ' +
    width.toFixed(1) + " " + height.toFixed(1) +
    '" preserveAspectRatio="none" aria-hidden="true">' + effects.join("") + "</svg>";
}

function clearPetSkill() {
  const stage = document.getElementById("pet-stage");
  const layer = document.getElementById("pet-skill-layer");
  const particles = document.getElementById("pet-particles");
  if (layer) layer.replaceChildren();
  if (particles) particles.replaceChildren();
  if (!stage) return;
  stage.classList.remove(
    "skill-leaf", "skill-ember", "skill-water", "skill-electric", "playing"
  );
}

function spawnPetEffect(pet, x, y, rect, options) {
  const ultimate = !!(options && options.ultimate);
  const stage = document.getElementById("pet-stage");
  const layer = document.getElementById("pet-skill-layer");
  const particles = document.getElementById("pet-particles");
  particles.replaceChildren();
  layer.innerHTML = "";
  stage.classList.remove("skill-leaf", "skill-ember", "skill-water", "skill-electric");
  void stage.offsetWidth;
  stage.classList.add("skill-" + pet.effect);

  const counts = { leaf: 12, ember: 16, water: 14, electric: 12 };
  const power = ultimate ? 5 : petEffectIntensity();
  stage.style.setProperty("--fx-power", String(power));
  const targetX = clampSkillPoint(x, rect.width);
  const targetY = clampSkillPoint(y, rect.height);
  layer.innerHTML = petSkillSvg(pet, targetX, targetY, rect, power);

  const origins = {
    leaf: [rect.width * .5, rect.height * .72],
    ember: [rect.width * .5, rect.height * .47],
    water: [rect.width * .5, rect.height * .61],
    electric: [rect.width * .5, rect.height * .48],
  };
  const origin = origins[pet.effect] || [rect.width * .5, rect.height * .55];
  const baseCount = (counts[pet.effect] || 12) * (0.72 + power * 0.28);
  const count = Math.round(baseCount * (ultimate ? 1.9 : 1));
  for (let index = 0; index < count; index++) {
    const particle = document.createElement("i");
    particle.className = "pet-particle fx-" + pet.effect;
    const travel = .48 + (index % 5) * .07;
    particle.style.left =
      (origin[0] + (targetX - origin[0]) * travel).toFixed(1) + "px";
    particle.style.top =
      (origin[1] + (targetY - origin[1]) * travel).toFixed(1) + "px";
    particle.style.setProperty("--particle-accent", pet.accent);
    const angle = index / count * Math.PI * 2 + (index % 3) * 0.16;
    const distance = (18 + power * 4 + (index % 5) * 8) * (ultimate ? 1.45 : 1);
    particle.style.setProperty("--dx", Math.cos(angle) * distance + "px");
    particle.style.setProperty("--dy", Math.sin(angle) * distance + "px");
    particle.style.setProperty("--spin", (index % 2 ? -1 : 1) * (45 + index * 9) + "deg");
    particle.addEventListener("animationend", function () {
      particle.remove();
    }, { once: true });
    particles.appendChild(particle);
  }
  window.setTimeout(function () {
    stage.classList.remove("skill-leaf", "skill-ember", "skill-water", "skill-electric");
    layer.replaceChildren();
  }, 760 + power * 110);
}

// 长按放大招：短按仍走原来的小技能路径。
const PANEL_LONG_PRESS_MS = 520;
const ULTIMATE_MESSAGES = {
  leaf: ["飞叶风暴！", "花瓣全部甩了出去。"],
  ember: ["大字爆炎！", "尾巴的火一下窜高了。"],
  water: ["水炮齐射！", "龟壳里的水全打了出去。"],
  electric: ["十万伏特！", "整只都亮起来了。"],
};
let ultimateMessageIndex = 0;

function spawnPanelShockwave(count) {
  const stage = document.getElementById("pet-stage");
  if (!stage) return;
  const total = Math.max(1, Math.min(3, Number(count) || 1));
  for (let index = 0; index < total; index += 1) {
    const ring = document.createElement("span");
    ring.className = "pet-shockwave" + (index > 0 ? " second" : "");
    stage.appendChild(ring);
    ring.addEventListener("animationend", function () { ring.remove(); });
  }
}

function triggerPetPlay(x, y, options) {
  const ultimate = !!(options && options.ultimate);
  const stage = document.getElementById("pet-stage");
  const avatar = document.getElementById("pet-avatar");
  const rect = stage.getBoundingClientRect();
  const targetX = Number.isFinite(x) ? x : (latestPointer.x ?? rect.width * 0.65);
  const targetY = Number.isFinite(y) ? y : (latestPointer.y ?? rect.height * 0.42);
  const centerX = rect.width / 2;
  const centerY = rect.height * 0.53;
  const dx = Math.max(-42, Math.min(42, (targetX - centerX) * 0.3));
  const dy = Math.max(-26, Math.min(20, (targetY - centerY) * 0.28));
  stage.style.setProperty("--pet-dx", dx.toFixed(1) + "px");
  stage.style.setProperty("--pet-dy", dy.toFixed(1) + "px");
  avatar.classList.remove("pounce", "ultimate");
  void avatar.offsetWidth;
  avatar.classList.add(ultimate ? "ultimate" : "pounce");

  const pet = currentPet();
  if (petViewer) {
    petViewer.playSkill(pet.effect, ultimate ? 5 : petEffectIntensity(),
      ultimate ? { ultimate: true } : undefined);
  }
  const bubble = document.getElementById("pet-bubble");
  if (bubble) {
    bubble.classList.toggle("is-ultimate", ultimate);
    if (ultimate) {
      const lines = ULTIMATE_MESSAGES[pet.effect] || [pet.name + "放出了大招！"];
      bubble.textContent = lines[ultimateMessageIndex++ % lines.length];
    } else {
      bubble.textContent = pet.messages[petMessageIndex++ % pet.messages.length];
    }
  }
  spawnPetEffect(pet, targetX, targetY, rect, ultimate ? { ultimate: true } : undefined);
  if (ultimate) spawnPanelShockwave(2);
  stage.classList.remove("playing");
  void stage.offsetWidth;
  stage.classList.add("playing");
}

const petStage = document.getElementById("pet-stage");
petStage.addEventListener("pointermove", function (event) {
  if (activeTab !== "pet") return;
  const rect = petStage.getBoundingClientRect();
  latestPointer.x = event.clientX - rect.left;
  latestPointer.y = event.clientY - rect.top;
  updatePetGaze(latestPointer.x, latestPointer.y);
}, { passive: true });
petStage.addEventListener("pointerleave", function () {
  latestPointer.x = null;
  latestPointer.y = null;
  resetPetGaze();
});
document.documentElement.addEventListener("pointerleave", function () {
  latestPointer.x = null;
  latestPointer.y = null;
  resetPetGaze();
});
document.addEventListener("visibilitychange", function () {
  if (petViewer) {
    petViewer.setEnabled(document.visibilityState !== "hidden" && activeTab === "pet");
  }
});
window.addEventListener("blur", resetPetGaze);
let petPressStart = 0;
let petPressTimer = 0;
let petPressCharging = false;
let petPressPoint = null;

function clearPanelPetPress() {
  window.clearTimeout(petPressTimer);
  petPressTimer = 0;
  petPressCharging = false;
  const avatar = document.getElementById("pet-avatar");
  if (avatar) avatar.classList.remove("charge");
  if (petViewer) petViewer.setCharging(false);
}

function beginPanelPetPress(event) {
  clearPanelPetPress();
  const rect = petStage.getBoundingClientRect();
  petPressPoint = event
    ? { x: event.clientX - rect.left, y: event.clientY - rect.top }
    : null;
  petPressStart = performance.now();
  petPressTimer = window.setTimeout(function () {
    petPressCharging = true;
    const avatar = document.getElementById("pet-avatar");
    if (avatar) avatar.classList.add("charge");
    if (petViewer) petViewer.setCharging(true);
    const bubble = document.getElementById("pet-bubble");
    if (bubble) {
      bubble.classList.remove("is-ultimate");
      bubble.textContent = "蓄力中…松手放大招";
    }
  }, PANEL_LONG_PRESS_MS);
}

function endPanelPetPress(cancelled) {
  const held = performance.now() - petPressStart;
  const wasCharging = petPressCharging;
  const point = petPressPoint;
  clearPanelPetPress();
  petPressPoint = null;
  if (cancelled) return;
  const ultimate = wasCharging || held >= PANEL_LONG_PRESS_MS;
  triggerPetPlay(point ? point.x : undefined, point ? point.y : undefined,
    ultimate ? { ultimate: true } : undefined);
}

petStage.addEventListener("pointerdown", function (event) {
  if (event.button !== 0) return;
  beginPanelPetPress(event);
});
petStage.addEventListener("pointerup", function (event) {
  if (event.button !== 0) return;
  endPanelPetPress(false);
});
petStage.addEventListener("pointercancel", function () { endPanelPetPress(true); });
petStage.addEventListener("pointerleave", function () { endPanelPetPress(true); });
petStage.addEventListener("keydown", function (event) {
  if (event.key !== "Enter" && event.key !== " ") return;
  event.preventDefault();
  if (event.repeat) return;
  beginPanelPetPress(null);
});
petStage.addEventListener("keyup", function (event) {
  if (event.key !== "Enter" && event.key !== " ") return;
  event.preventDefault();
  endPanelPetPress(false);
});
document.getElementById("pet-avatar").addEventListener("animationend", function () {
  this.classList.remove("pounce", "ultimate");
});
document.getElementById("pet-stage").addEventListener("animationend", function () {
  this.classList.remove("playing");
});

function applyTheme(theme) {
  if (theme === "light" || theme === "dark") {
    document.documentElement.dataset.theme = theme;
  } else {
    delete document.documentElement.dataset.theme;
  }
}

async function syncTheme() {
  try {
    applyTheme(asJSON(await Bound.SettingsGet()).theme || "system");
  } catch (_) {}
}

function hidePanel() {
  Events.Emit("collapse-panel");
  Window.Hide().catch(function () {});
}

function openMain() {
  Events.Emit("open-main");
  hidePanel();
}

document.getElementById("refresh").addEventListener("click", function () {
  if (activeTab === "speed") {
    refreshSpeed();
    return;
  }
  if (activeTab === "pet") {
    triggerPetPlay();
    return;
  }
  refresh();
});
document.getElementById("open-main-top").addEventListener("click", openMain);
document.querySelectorAll("[data-checkin-scope]").forEach(function (tile) {
  tile.addEventListener("click", function () {
    openCheckin({ scope: tile.dataset.checkinScope || "all" });
  });
});
document.getElementById("checkin-collapse-all").addEventListener("click", function () {
  if (!currentTargets.length) return;
  const next = nextGroupExpansion(currentTargets, expanded);
  currentTargets.forEach(function (target) {
    expanded[target] = next[target];
  });
  document.querySelectorAll("#targets .app").forEach(function (app) {
    const open = expanded[app.dataset.target] !== false;
    app.classList.toggle("open", open);
    const head = app.querySelector(".app-head");
    if (head) head.setAttribute("aria-expanded", open ? "true" : "false");
  });
  syncPanelCollapseButton();
});
document.getElementById("settings").addEventListener("click", function () {
  Events.Emit("open-settings");
  hidePanel();
});
document.getElementById("collapse").addEventListener("click", hidePanel);
document.getElementById("run").addEventListener("click", async function () {
  const button = this;
  if (activeTab === "speed") {
    await refreshSpeed();
    return;
  }
  if (activeTab === "pet") {
    triggerPetPlay();
    return;
  }
  button.disabled = true;
  showMessage("正在执行今日巡检…", true);
  try {
    await Bound.Run();
    await refresh();
    showMessage("巡检完成", true);
  } catch (_) {
    showMessage("巡检失败；本次未宣称成功。");
  } finally {
    button.disabled = false;
  }
});
document.getElementById("open-main").addEventListener("click", openMain);
document.getElementById("quit").addEventListener("click", function () {
  Events.Emit("quit");
});

Events.On("accounts-updated", function () {
  if (activeTab === "checkin") refresh();
});
Events.On("common:WindowShow", function (event) {
  if (event.sender !== "panel") return;
  if (activeTab === "checkin") refresh();
  if (activeTab === "speed") refreshSpeed();
  if (activeTab === "pet") renderPet();
});
Events.On("tokenspeed-changed", function (event) {
  if (event && event.data) renderSpeed(event.data);
});
Events.On("theme-changed", function (event) {
  applyTheme(event && event.data);
});
renderSparkline();
renderPet();
activateTab(readStoredString(panelTabStorageKey, "checkin"), { persist: false });
refreshSpeed();
refresh();
syncTheme();
