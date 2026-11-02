const MAX_TILT = 3.2;
const liquidTabRoots = new WeakSet();
const liquidSurfaceRoots = new WeakSet();
const liquidCommandRoots = new WeakSet();

function tabButtons(root) {
  return Array.from(root.children).filter(function (node) {
    return node.tagName === "BUTTON";
  });
}

function reducedMotion() {
  return window.matchMedia &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

function activeTab(root) {
  return tabButtons(root).find(function (node) {
    return node.tagName === "BUTTON" &&
      (node.classList.contains("active") ||
        node.getAttribute("aria-selected") === "true" ||
        node.getAttribute("aria-current") === "page");
  }) || null;
}

function tabGeometry(root, button) {
  const rootRect = root.getBoundingClientRect();
  const buttonRect = button.getBoundingClientRect();
  return {
    x: buttonRect.left - rootRect.left,
    y: buttonRect.top - rootRect.top,
    width: buttonRect.width,
    height: buttonRect.height,
  };
}

function sameGeometry(left, right) {
  return !!left && !!right &&
    Math.abs(left.x - right.x) < 0.25 &&
    Math.abs(left.y - right.y) < 0.25 &&
    Math.abs(left.width - right.width) < 0.25 &&
    Math.abs(left.height - right.height) < 0.25;
}

export function initLiquidTabs(root) {
  if (!root || liquidTabRoots.has(root)) return;
  liquidTabRoots.add(root);

  const pill = document.createElement("span");
  pill.className = "liquid-tab-pill";
  pill.setAttribute("aria-hidden", "true");
  root.appendChild(pill);
  root.classList.add("liquid-tabs-ready");

  let previous = null;
  let current = null;
  let pendingFrame = 0;

  function place(geometry, animate) {
    if (!geometry || geometry.width <= 0 || geometry.height <= 0) {
      pill.style.opacity = "0";
      current = null;
      return;
    }

    const changed = previous && !sameGeometry(previous, geometry);
    pill.style.opacity = "1";
    pill.style.width = geometry.width + "px";
    pill.style.height = geometry.height + "px";
    pill.style.transform =
      "translate3d(" + geometry.x + "px," + geometry.y + "px,0)";

    if (animate && changed && !reducedMotion() && pill.animate) {
      const distance = Math.abs(geometry.x - previous.x);
      const midpointX = previous.x + (geometry.x - previous.x) * 0.58;
      const midpointY = previous.y + (geometry.y - previous.y) * 0.58;
      const midpointWidth = Math.max(
        previous.width,
        geometry.width
      ) + Math.min(22, distance * 0.24);
      pill.animate([
        {
          transform: "translate3d(" + previous.x + "px," +
            previous.y + "px,0)",
          width: previous.width + "px",
          offset: 0,
        },
        {
          transform: "translate3d(" + midpointX + "px," +
            midpointY + "px,0) scaleY(1.035)",
          width: midpointWidth + "px",
          offset: 0.58,
        },
        {
          transform: "translate3d(" + geometry.x + "px," +
            geometry.y + "px,0)",
          width: geometry.width + "px",
          offset: 1,
        },
      ], {
        duration: 360,
        easing: "cubic-bezier(.2,.9,.25,1.12)",
        fill: "none",
      });
    }
    previous = geometry;
    current = geometry;
  }

  function update(animate) {
    if (pendingFrame) return;
    pendingFrame = requestAnimationFrame(function () {
      pendingFrame = 0;
      const button = activeTab(root);
      if (!button) {
        pill.style.opacity = "0";
        return;
      }
      const geometry = tabGeometry(root, button);
      if (!animate || !current) {
        previous = current;
        place(geometry, false);
        return;
      }
      place(geometry, true);
    });
  }

  root.addEventListener("click", function (event) {
    if (event.target.closest("button")) update(true);
  });
  root.addEventListener("liquid-tab-geometry", function () {
    update(false);
  });

  const observer = new MutationObserver(function () {
    update(true);
  });
  Array.from(root.children).forEach(function (button) {
    if (button.tagName !== "BUTTON") return;
    observer.observe(button, {
      attributes: true,
      attributeFilter: ["class", "aria-selected", "aria-current"],
    });
  });

  if (window.ResizeObserver) {
    const resizeObserver = new ResizeObserver(function () {
      update(false);
    });
    resizeObserver.observe(root);
    Array.from(root.children).forEach(function (button) {
      if (button.tagName === "BUTTON") resizeObserver.observe(button);
    });
  } else {
    window.addEventListener("resize", function () {
      update(false);
    });
  }

  requestAnimationFrame(function () {
    update(false);
  });
}

function resetDock(root) {
  tabButtons(root).forEach(function (button) {
    button.style.removeProperty("--liquid-dock-scale");
    button.style.removeProperty("--liquid-dock-lift");
  });
}

function initLiquidDock(root) {
  if (!root || root.dataset.liquidDockReady === "true") return;
  root.dataset.liquidDockReady = "true";
  root.classList.add("liquid-dock");

  const buttons = tabButtons(root);
  let settleTimer = 0;

  function settle() {
    window.clearTimeout(settleTimer);
    settleTimer = window.setTimeout(function () {
      root.dispatchEvent(new CustomEvent("liquid-tab-geometry"));
    }, 190);
  }

  function setDock(index) {
    buttons.forEach(function (button, buttonIndex) {
      const distance = Math.abs(buttonIndex - index);
      const scale = distance === 0 ? 1.075 : distance === 1 ? 1.025 : 1;
      button.style.setProperty("--liquid-dock-scale", String(scale));
      button.style.setProperty(
        "--liquid-dock-lift",
        distance === 0 ? "-1px" : "0px"
      );
    });
    settle();
  }

  root.addEventListener("pointerover", function (event) {
    if (event.pointerType !== "mouse") return;
    const button = event.target.closest("button");
    if (!button || button.parentElement !== root) return;
    const index = buttons.indexOf(button);
    if (index >= 0) setDock(index);
  });

  root.addEventListener("pointerleave", function () {
    window.clearTimeout(settleTimer);
    resetDock(root);
    settle();
  });
}

function liquidSelector() {
  return [
    "[data-liquid-surface]",
    "section.page > .summary",
    ".speed-hero",
  ].join(",");
}

function clamp(value, min, max) {
  return Math.max(min, Math.min(max, value));
}

export function initLiquidSurfaces(root) {
  const scope = root || document;
  if (liquidSurfaceRoots.has(scope)) return;
  liquidSurfaceRoots.add(scope);

  scope.querySelectorAll(liquidSelector()).forEach(function (surface) {
    if (surface.dataset.noLiquid !== undefined ||
        surface.dataset.liquidReady === "true") {
      return;
    }
    surface.dataset.liquidReady = "true";
    surface.classList.add("liquid-surface");
    if (window.getComputedStyle(surface).position === "static") {
      surface.style.position = "relative";
    }

    const sheen = document.createElement("span");
    sheen.className = "liquid-sheen";
    sheen.setAttribute("aria-hidden", "true");
    surface.appendChild(sheen);

    let pendingEvent = null;
    let frame = 0;

    function applyPointer(event) {
      const rect = surface.getBoundingClientRect();
      if (!rect.width || !rect.height) return;
      const x = clamp(event.clientX - rect.left, 0, rect.width);
      const y = clamp(event.clientY - rect.top, 0, rect.height);
      const nx = x / rect.width * 2 - 1;
      const ny = y / rect.height * 2 - 1;
      surface.style.setProperty("--liquid-x", x + "px");
      surface.style.setProperty("--liquid-y", y + "px");
      if (!reducedMotion() && event.pointerType === "mouse") {
        surface.style.setProperty(
          "--liquid-tilt-x",
          (-ny * MAX_TILT).toFixed(2) + "deg"
        );
        surface.style.setProperty(
          "--liquid-tilt-y",
          (nx * MAX_TILT).toFixed(2) + "deg"
        );
      }
    }

    surface.addEventListener("pointerenter", function (event) {
      if (event.pointerType !== "mouse") return;
      surface.classList.add("liquid-active");
      applyPointer(event);
    });

    surface.addEventListener("pointermove", function (event) {
      if (event.pointerType !== "mouse") return;
      pendingEvent = event;
      if (frame) return;
      frame = requestAnimationFrame(function () {
        frame = 0;
        if (pendingEvent) applyPointer(pendingEvent);
      });
    }, { passive: true });

    surface.addEventListener("pointerleave", function () {
      if (frame) cancelAnimationFrame(frame);
      frame = 0;
      pendingEvent = null;
      surface.classList.remove("liquid-active");
      surface.style.setProperty("--liquid-tilt-x", "0deg");
      surface.style.setProperty("--liquid-tilt-y", "0deg");
    });
  });
}

function initLiquidCommandPalette(root) {
  const scope = root || document;
  if (liquidCommandRoots.has(scope)) return;

  const results = scope.querySelector("#command-results");
  if (!results) return;
  liquidCommandRoots.add(scope);
  results.classList.add("command-liquid-results");

  let highlight = results.querySelector(".command-liquid-highlight");
  let previous = null;
  let pendingFrame = 0;

  function ensureHighlight() {
    if (highlight && highlight.parentElement === results) return highlight;
    highlight = results.querySelector(".command-liquid-highlight");
    if (!highlight) {
      highlight = document.createElement("span");
      highlight.className = "command-liquid-highlight";
      highlight.setAttribute("aria-hidden", "true");
    }
    results.prepend(highlight);
    return highlight;
  }

  function selectedOption() {
    return results.querySelector('[role="option"][aria-selected="true"]');
  }

  function optionGeometry(option) {
    return {
      x: option.offsetLeft,
      y: option.offsetTop,
      width: option.offsetWidth,
      height: option.offsetHeight,
    };
  }

  function place(geometry, animate) {
    const pill = ensureHighlight();
    if (!geometry || geometry.width <= 0 || geometry.height <= 0) {
      pill.style.opacity = "0";
      previous = null;
      return;
    }

    const changed = previous && !sameGeometry(previous, geometry);
    pill.style.opacity = "1";
    pill.style.width = geometry.width + "px";
    pill.style.height = geometry.height + "px";
    pill.style.transform =
      "translate3d(" + geometry.x + "px," + geometry.y + "px,0)";

    if (animate && changed && !reducedMotion() && pill.animate) {
      const midpointX = previous.x + (geometry.x - previous.x) * 0.55;
      const midpointY = previous.y + (geometry.y - previous.y) * 0.55;
      const distance = Math.abs(geometry.y - previous.y);
      const midpointHeight =
        Math.max(previous.height, geometry.height) +
        Math.min(5, distance * 0.08);
      pill.animate([
        {
          transform: "translate3d(" + previous.x + "px," +
            previous.y + "px,0)",
          width: previous.width + "px",
          height: previous.height + "px",
          offset: 0,
        },
        {
          transform: "translate3d(" + midpointX + "px," +
            midpointY + "px,0)",
          width: Math.max(previous.width, geometry.width) + 3 + "px",
          height: midpointHeight + "px",
          offset: 0.55,
        },
        {
          transform: "translate3d(" + geometry.x + "px," +
            geometry.y + "px,0)",
          width: geometry.width + "px",
          height: geometry.height + "px",
          offset: 1,
        },
      ], {
        duration: 230,
        easing: "cubic-bezier(.22,.9,.24,1)",
        fill: "none",
      });
    }
    previous = geometry;
  }

  function update(animate) {
    if (pendingFrame) return;
    pendingFrame = requestAnimationFrame(function () {
      pendingFrame = 0;
      const option = selectedOption();
      place(option ? optionGeometry(option) : null, animate);
    });
  }

  const observer = new MutationObserver(function () {
    update(true);
  });
  observer.observe(results, {
    subtree: true,
    childList: true,
    attributes: true,
    attributeFilter: ["aria-selected"],
  });

  if (window.ResizeObserver) {
    const resizeObserver = new ResizeObserver(function () {
      update(false);
    });
    resizeObserver.observe(results);
  }

  update(false);
}

export function initLiquidUI() {
  document.querySelectorAll("[data-liquid-tabs]").forEach(function (root) {
    initLiquidTabs(root);
  });
  document.querySelectorAll("[data-liquid-dock]").forEach(function (root) {
    initLiquidDock(root);
  });
  initLiquidSurfaces(document);
  initLiquidCommandPalette(document);
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", initLiquidUI, { once: true });
} else {
  initLiquidUI();
}
