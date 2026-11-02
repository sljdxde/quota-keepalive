const PROVIDER_ICONS = {
  codearts: { path: "assets/provider-icons/codearts.svg", mode: "color" },
  workbuddy: { path: "assets/provider-icons/workbuddy.svg", mode: "color" },
  "workbuddy-ai": { path: "assets/provider-icons/workbuddy.svg", mode: "color" },
  codebuddy: { path: "assets/provider-icons/codebuddy.svg", mode: "color" },
  "minimax-code": { path: "assets/provider-icons/minimax.svg", mode: "color" },
  "minimax-code-global": { path: "assets/provider-icons/minimax.svg", mode: "color" },
  "opencode-zen-free": { path: "assets/provider-icons/opencode.svg", mode: "mask" },
  opencode: { path: "assets/provider-icons/opencode.svg", mode: "mask" },
  qoder: { path: "assets/provider-icons/qoder.svg", mode: "mask" },
  "qoder-cn": { path: "assets/provider-icons/qoder.svg", mode: "mask" },
  qwenwork: { path: "assets/provider-icons/qwen.svg", mode: "color" },
  "qwenwork-enterprise": { path: "assets/provider-icons/qwen.svg", mode: "color" },
  "trae-cn": { path: "assets/provider-icons/trae.svg", mode: "mask" },
  "trae-global": { path: "assets/provider-icons/trae.svg", mode: "mask" },
  zcode: { path: "assets/provider-icons/zcode.png", mode: "color" },
};

export function providerIconSpec(provider) {
  const id = String((provider && provider.id) || provider || "").trim().toLowerCase();
  return PROVIDER_ICONS[id] || null;
}

export function populateProviderIcon(host, provider) {
  if (!host) return host;
  host.replaceChildren();
  host.classList.remove("is-logo", "is-mask", "is-color");
  const spec = providerIconSpec(provider);
  if (!spec) {
    host.textContent = String((provider && (provider.name || provider.id)) || "?")
      .slice(0, 2).toUpperCase();
    return host;
  }
  host.classList.add("is-logo", spec.mode === "mask" ? "is-mask" : "is-color");
  if (spec.mode === "mask") {
    const glyph = document.createElement("span");
    glyph.className = "provider-glyph";
    const mask = 'url("' + spec.path + '") center / contain no-repeat';
    glyph.style.webkitMask = mask;
    glyph.style.mask = mask;
    host.appendChild(glyph);
    return host;
  }
  const image = document.createElement("img");
  image.src = spec.path;
  image.alt = "";
  image.draggable = false;
  host.appendChild(image);
  return host;
}
