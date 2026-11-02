// Shared title system. The theme sigils come from Lucide 0.468.0 (ISC).
// Keeping one icon family, one stroke weight and one visual grammar makes the
// rank marks readable at both 18px and 56px.

export const titleThemeOrder = [
  "vehicles", "animals", "athletics", "music", "network",
  "sound", "student", "career", "space",
];

export const titleThemeHues = {
  vehicles: 193, animals: 32, athletics: 15, music: 318, network: 218,
  sound: 270, student: 148, career: 44, space: 246,
};

export const titleThresholds = [
  0.5, 8, 12, 16, 24, 32, 48, 68, 96, 136, 200, 280, 400,
];

const titleThemeLabels = {
  vehicles: "交通工具",
  animals: "动物速度",
  athletics: "竞技体育",
  music: "音乐速度",
  network: "网络世代",
  sound: "音速时代",
  student: "学生生涯",
  career: "职场晋升",
  space: "宇宙速度",
};

const titleSigils = {
  vehicles: [
    '<path d="M19 17h2c.6 0 1-.4 1-1v-3c0-.9-.7-1.7-1.5-1.9C18.7 10.6 16 10 16 10s-1.3-1.4-2.2-2.3c-.5-.4-1.1-.7-1.8-.7H5c-.6 0-1.1.4-1.4.9l-1.4 2.9A3.7 3.7 0 0 0 2 12v4c0 .6.4 1 1 1h2"/>',
    '<circle cx="7" cy="17" r="2"/><path d="M9 17h6"/><circle cx="17" cy="17" r="2"/>',
  ],
  animals: [
    '<circle cx="11" cy="4" r="2"/><circle cx="18" cy="8" r="2"/><circle cx="20" cy="16" r="2"/>',
    '<path d="M9 10a5 5 0 0 1 5 5v3.5a3.5 3.5 0 0 1-6.84 1.045Q6.52 17.48 4.46 16.84A3.5 3.5 0 0 1 5.5 10Z"/>',
  ],
  athletics: [
    '<path d="M6 9H4.5a2.5 2.5 0 0 1 0-5H6"/>',
    '<path d="M18 9h1.5a2.5 2.5 0 0 0 0-5H18"/>',
    '<path d="M4 22h16"/><path d="M10 14.66V17c0 .55-.47.98-.97 1.21C7.85 18.75 7 20.24 7 22"/>',
    '<path d="M14 14.66V17c0 .55.47.98.97 1.21C16.15 18.75 17 20.24 17 22"/>',
    '<path d="M18 2H6v7a6 6 0 0 0 12 0V2Z"/>',
  ],
  music: [
    '<circle cx="8" cy="18" r="4"/><path d="M12 18V2l7 4"/>',
  ],
  network: [
    '<rect x="16" y="16" width="6" height="6" rx="1"/>',
    '<rect x="2" y="16" width="6" height="6" rx="1"/>',
    '<rect x="9" y="2" width="6" height="6" rx="1"/>',
    '<path d="M5 16v-3a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v3"/><path d="M12 12V8"/>',
  ],
  sound: [
    '<path d="M2 13a2 2 0 0 0 2-2V7a2 2 0 0 1 4 0v13a2 2 0 0 0 4 0V4a2 2 0 0 1 4 0v13a2 2 0 0 0 4 0v-4a2 2 0 0 1 2-2"/>',
  ],
  student: [
    '<path d="M21.42 10.922a1 1 0 0 0-.019-1.838L12.83 5.18a2 2 0 0 0-1.66 0L2.6 9.08a1 1 0 0 0 0 1.832l8.57 3.908a2 2 0 0 0 1.66 0z"/>',
    '<path d="M22 10v6"/><path d="M6 12.5V16a6 3 0 0 0 12 0v-3.5"/>',
  ],
  career: [
    '<path d="M12 12h.01"/><path d="M16 6V4a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v2"/>',
    '<path d="M22 13a18.15 18.15 0 0 1-20 0"/>',
    '<rect width="20" height="14" x="2" y="6" rx="2"/>',
  ],
  space: [
    '<circle cx="12" cy="12" r="3"/><circle cx="19" cy="5" r="2"/><circle cx="5" cy="19" r="2"/>',
    '<path d="M10.4 21.9a10 10 0 0 0 9.941-15.416"/>',
    '<path d="M13.5 2.1a10 10 0 0 0-9.841 15.416"/>',
  ],
};

export function normalizeTitleTheme(value) {
  return titleThemeOrder.includes(value) ? value : "vehicles";
}

export function titleLevelForSpeed(speed, active) {
  const value = Number(speed) || 0;
  if (!active || value < titleThresholds[0]) return -1;
  let level = 0;
  titleThresholds.forEach((threshold, index) => {
    if (value >= threshold) level = index + 1;
  });
  return Math.min(titleThresholds.length, level);
}

export function titleLevelProgress(speed, active) {
  const level = titleLevelForSpeed(speed, active);
  if (level < 0) return 0;
  if (level >= titleThresholds.length) return 100;
  const lower = titleThresholds[level - 1];
  const upper = titleThresholds[level];
  return Math.max(4, Math.min(100, ((Number(speed) - lower) / Math.max(0.01, upper - lower)) * 100));
}

export function titleThemeSigilMarkup(themeId, className = "") {
  const normalized = normalizeTitleTheme(themeId);
  return '<svg class="title-sigil ' + className + '" viewBox="0 0 24 24" fill="none" ' +
    'stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" ' +
    'aria-hidden="true">' + titleSigils[normalized].join("") + "</svg>";
}

export function titleIconMarkup(themeId, level, options = {}) {
  const normalized = normalizeTitleTheme(themeId);
  const normalizedLevel = level < 1 ? 0 : Math.min(13, Number(level) || 0);
  const explicitProgress = Number(options.progress);
  const progress = normalizedLevel < 1
    ? 0
    : Number.isFinite(explicitProgress)
      ? Math.max(0, Math.min(100, explicitProgress))
      : Math.min(100, normalizedLevel / titleThresholds.length * 100);
  const classes = [
    "title-icon",
    "title-rank-mark",
    options.active ? "title-icon-current" : "",
    options.compact ? "is-compact" : "",
  ].filter(Boolean).join(" ");
  const count = normalizedLevel < 1
    ? '<span class="title-rank-count">·</span>'
    : '<span class="title-rank-count">' + String(normalizedLevel).padStart(2, "0") + "</span>";
  return '<span class="' + classes + '" style="--title-rank:' + progress.toFixed(1) + '%" ' +
    'aria-hidden="true"><span class="title-rank-orb">' +
    titleThemeSigilMarkup(normalized, "title-rank-sigil") + count + "</span></span>";
}

export function titleThemeLabel(themeId) {
  return titleThemeLabels[normalizeTitleTheme(themeId)];
}
