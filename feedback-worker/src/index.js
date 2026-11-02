const MAX_BODY_BYTES = 9 * 1024 * 1024;
const MAX_IMAGES = 6;
const MAX_IMAGE_BYTES = 1400 * 1024;
const MAX_TOTAL_IMAGE_BYTES = 7 * 1024 * 1024;
const MAX_DESCRIPTION = 12000;
const MAX_LOGS = 100 * 1024;
const RATE_WINDOW_MS = 60 * 1000;
const RATE_LIMIT = 3;

const CORS_HEADERS = {
  "access-control-allow-origin": "*",
  "access-control-allow-methods": "POST, OPTIONS",
  "access-control-allow-headers": "content-type, x-qka-key",
  "access-control-max-age": "600",
};

export class HttpError extends Error {
  constructor(status, message, retryAfter = 0) {
    super(message);
    this.status = status;
    this.retryAfter = retryAfter;
  }
}

// createHandler keeps GitHub and clock dependencies injectable for unit tests.
export function createHandler(deps = {}) {
  const githubFetch = deps.githubFetch || fetch;
  const now = deps.now || (() => Date.now());
  const random = deps.random || (() => crypto.randomUUID());
  const buckets = new Map();
  let commitQueue = Promise.resolve();

  function rateLimit(request) {
    const ip = request.headers.get("cf-connecting-ip") || "local";
    const current = now();
    let bucket = buckets.get(ip);
    if (!bucket || current - bucket.startedAt >= RATE_WINDOW_MS) {
      bucket = { startedAt: current, count: 0 };
      buckets.set(ip, bucket);
    }
    bucket.count += 1;
    if (bucket.count > RATE_LIMIT) {
      const retryAfter = Math.max(1, Math.ceil((RATE_WINDOW_MS - (current - bucket.startedAt)) / 1000));
      throw new HttpError(429, "请求过于频繁，请稍后重试", retryAfter);
    }
    if (buckets.size > 2000) {
      for (const [key, value] of buckets) {
        if (current - value.startedAt >= RATE_WINDOW_MS) buckets.delete(key);
      }
    }
  }

  async function github(env, path, options = {}) {
    const response = await githubFetch(`https://api.github.com${path}`, {
      ...options,
      headers: {
        accept: "application/vnd.github+json",
        authorization: `Bearer ${env.GITHUB_TOKEN}`,
        "content-type": "application/json",
        "user-agent": "Quota-Keepalive-Feedback",
        "x-github-api-version": "2022-11-28",
        ...(options.headers || {}),
      },
    });
    const text = await response.text();
    let data = null;
    try {
      data = text ? JSON.parse(text) : null;
    } catch {
      data = null;
    }
    if (!response.ok) {
      const code = response.status;
      throw new HttpError(
        code === 401 || code === 403 ? 503 : 502,
        "GitHub 服务暂时不可用",
        response.headers.get("retry-after") ? Number(response.headers.get("retry-after")) : 0
      );
    }
    return data;
  }

  async function ensureAssetBranch(env) {
    const repo = env.GITHUB_REPOSITORY || "sljdxde/quota-keepalive";
    const branch = env.ASSET_BRANCH || "feedback-assets";
    try {
      return await github(env, `/repos/${repo}/git/ref/heads/${encodeURIComponent(branch)}`);
    } catch (error) {
      if (!(error instanceof HttpError) || error.status !== 502) throw error;
    }
    const main = await github(env, `/repos/${repo}/git/ref/heads/main`);
    await github(env, `/repos/${repo}/git/refs`, {
      method: "POST",
      body: JSON.stringify({ ref: `refs/heads/${branch}`, sha: main.object.sha }),
    });
    return github(env, `/repos/${repo}/git/ref/heads/${encodeURIComponent(branch)}`);
  }

  async function uploadImages(env, images) {
    if (!images.length) return [];
    const repo = env.GITHUB_REPOSITORY || "sljdxde/quota-keepalive";
    const branch = env.ASSET_BRANCH || "feedback-assets";
    const ref = await ensureAssetBranch(env);
    const parent = ref.object.sha;
    const commit = await github(env, `/repos/${repo}/git/commits/${parent}`);
    const directory = `feedback/${new Date(now()).toISOString().replace(/[:.]/g, "-")}-${random()}`;
    const blobs = [];
    for (let index = 0; index < images.length; index += 1) {
      const image = images[index];
      const blob = await github(env, `/repos/${repo}/git/blobs`, {
        method: "POST",
        body: JSON.stringify({ content: base64(image.bytes), encoding: "base64" }),
      });
      blobs.push({
        path: `${directory}/${index + 1}.${extensionFor(image.mime)}`,
        mode: "100644",
        type: "blob",
        sha: blob.sha,
      });
    }
    const tree = await github(env, `/repos/${repo}/git/trees`, {
      method: "POST",
      body: JSON.stringify({ base_tree: commit.tree.sha, tree: blobs }),
    });
    const created = await github(env, `/repos/${repo}/git/commits`, {
      method: "POST",
      body: JSON.stringify({
        message: "chore(feedback): add redacted screenshots",
        tree: tree.sha,
        parents: [parent],
      }),
    });
    await github(env, `/repos/${repo}/git/refs/heads/${encodeURIComponent(branch)}`, {
      method: "PATCH",
      body: JSON.stringify({ sha: created.sha, force: false }),
    });
    return blobs.map((blob) => ({
      path: blob.path,
      url: `https://raw.githubusercontent.com/${repo}/${branch}/${blob.path}`,
    }));
  }

  async function createIssue(env, payload, assets) {
    const repo = env.GITHUB_REPOSITORY || "sljdxde/quota-keepalive";
    const body = issueBody(payload, assets);
    return github(env, `/repos/${repo}/issues`, {
      method: "POST",
      body: JSON.stringify({
        title: payload.title,
        body,
      }),
    });
  }

  return async function handle(request, env) {
    if (request.method === "OPTIONS") {
      return new Response(null, { status: 204, headers: CORS_HEADERS });
    }
    try {
      rateLimit(request);
      const url = new URL(request.url);
      if (request.method !== "POST" || url.pathname !== "/api/feedback") {
        throw new HttpError(404, "接口不存在");
      }
      if (!constantTimeEqual(request.headers.get("x-qka-key") || "", env?.SITE_KEY || "")) {
        throw new HttpError(401, "反馈入口校验失败");
      }
      const declared = Number(request.headers.get("content-length") || 0);
      if (declared > MAX_BODY_BYTES) throw new HttpError(413, "反馈内容过大");
      const raw = await request.text();
      if (new TextEncoder().encode(raw).length > MAX_BODY_BYTES) {
        throw new HttpError(413, "反馈内容过大");
      }
      const payload = validatePayload(JSON.parse(raw));
      if (!env || !env.GITHUB_TOKEN) throw new HttpError(503, "反馈服务尚未配置");
      const assets = await (commitQueue = commitQueue.then(
        () => uploadImages(env, payload.images),
        () => uploadImages(env, payload.images)
      ));
      const issue = await createIssue(env, payload, assets);
      return json(200, {
        ok: true,
        number: issue.number,
        url: issue.html_url,
      });
    } catch (error) {
      const status = error instanceof HttpError ? error.status : 400;
      const message = error instanceof HttpError ? error.message : "反馈内容无效";
      const retryAfter = error instanceof HttpError ? error.retryAfter : 0;
      return json(status, { ok: false, error: message }, retryAfter);
    }
  };
}

// Cloudflare entrypoint. The site key is deliberately injected per request so
// the handler itself remains unit-testable without module globals.
export default {
  async fetch(request, env) {
    return createHandler()(request, env);
  },
};

function validatePayload(payload) {
  if (!payload || typeof payload !== "object") throw new HttpError(400, "反馈内容无效");
  const description = cleanText(redact(payload.description), MAX_DESCRIPTION);
  if (description.length < 6) throw new HttpError(400, "请至少填写 6 个字符");
  const title = cleanText(redact(payload.title || description.split("\n")[0]), 100)
    .replaceAll("@", "@\u200b").replaceAll("#", "\\#") || "[用户反馈] 用户反馈";
  const logs = cleanText(redact(payload.logs || ""), MAX_LOGS);
  const version = cleanText(redact(payload.version || "unknown"), 80);
  const platform = cleanText(redact(payload.platform || "unknown"), 80);
  if (!Array.isArray(payload.images || [])) throw new HttpError(400, "图片格式无效");
  const images = (payload.images || []).slice(0, MAX_IMAGES).map(decodeImage);
  if ((payload.images || []).length > MAX_IMAGES) throw new HttpError(413, "最多上传 6 张图片");
  const total = images.reduce((sum, image) => sum + image.bytes.length, 0);
  if (total > MAX_TOTAL_IMAGE_BYTES) throw new HttpError(413, "图片总大小超过限制");
  return { title, description, logs, version, platform, images };
}

function decodeImage(image) {
  const match = /^data:image\/(png|jpeg|webp);base64,([A-Za-z0-9+/]+=*)$/.exec(
    String(image?.data_url || "")
  );
  if (!match) throw new HttpError(400, "图片格式必须是 PNG、JPEG 或 WebP");
  let bytes;
  try {
    const binary = atob(match[2]);
    bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
  } catch {
    throw new HttpError(400, "图片 Base64 无法解析");
  }
  if (!bytes.length || bytes.length > MAX_IMAGE_BYTES) {
    throw new HttpError(413, "单张图片超过 1.4 MB");
  }
  const mime = sniffMIME(bytes);
  if (!mime || mime !== match[1]) throw new HttpError(400, "图片内容与声明的格式不一致");
  return { mime, bytes };
}

function sniffMIME(bytes) {
  if (bytes.length >= 3 && bytes[0] === 0xff && bytes[1] === 0xd8 && bytes[2] === 0xff) {
    return "jpeg";
  }
  if (bytes.length >= 8 && [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]
    .every((value, index) => bytes[index] === value)) return "png";
  if (bytes.length >= 12 && String.fromCharCode(...bytes.slice(0, 4)) === "RIFF" &&
    String.fromCharCode(...bytes.slice(8, 12)) === "WEBP") return "webp";
  return "";
}

function extensionFor(mime) {
  return mime === "jpeg" ? "jpg" : mime;
}

function issueBody(payload, assets) {
  const fence = "`".repeat(Math.max(3, longestFence(payload.description) + 1));
  const assetLines = assets.length
    ? assets.map((asset, index) => `${index + 1}. ![截图 ${index + 1}](${asset.url})`).join("\n")
    : "未上传截图。";
  const logSection = payload.logs
    ? `<details><summary>最近 48 小时脱敏异常日志</summary>\n\n\`\`\`text\n${escapeFence(payload.logs, 3)}\n\`\`\`\n\n</details>`
    : "未附带日志。";
  return [
    "## 问题描述",
    `${fence}text`,
    escapeFence(payload.description, 3),
    fence,
    "",
    "## 运行环境",
    `- 版本：${escapeInline(payload.version)}`,
    `- 平台：${escapeInline(payload.platform)}`,
    "",
    "## 截图",
    assetLines,
    "",
    "## 日志",
    logSection,
    "",
    "---",
    "由 Quota Keepalive 内置反馈代理提交，客户端未直接持有 GitHub 凭据。",
  ].join("\n");
}

function longestFence(value) {
  return Math.max(0, ...Array.from(value.matchAll(/`+/g), (match) => match[0].length));
}

function escapeFence(value, minimum) {
  const fence = "`".repeat(minimum);
  return String(value).replaceAll(fence, "`\u200b`\u200b`\u200b");
}

function escapeInline(value) {
  return String(value).replace(/[<>@#`]/g, (char) => `&#${char.charCodeAt(0)};`);
}

function cleanText(value, max) {
  return String(value || "")
    .replace(/\r\n?/g, "\n")
    .replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/g, "")
    .slice(0, max)
    .trim();
}

function redact(value) {
  return String(value || "")
    .replace(/(bearer\s+)[a-z0-9._~+/\-=]{8,}/gi, "$1[redacted]")
    .replace(/\b(access[_-]?token|refresh[_-]?token|id[_-]?token|api[_-]?key|authorization|cookie|password|passwd|secret|token|xsec[_-]?token|session[_-]?token)(\s*[:=]\s*)[^\s,;&]+/gi, "$1$2[redacted]")
    .replace(/([?&](?:access_token|refresh_token|id_token|api_key|key|token|secret|password|xsec_token|xsec_source)=)[^&#\s]+/gi, "$1[redacted]")
    .replace(/\b([a-z0-9._%+-])[a-z0-9._%+-]*@([a-z0-9.-]+\.[a-z]{2,})\b/gi, "$1***@$2")
    .replace(/\b(1[3-9]\d)\d{4}(\d{4})\b/g, "$1****$2")
    .replace(/(?:\/Users\/|\/home\/|C:\\Users\\)[^/\s\\]+/gi, (match) => {
      if (match.startsWith("/Users/")) return "/Users/<user>";
      if (match.startsWith("/home/")) return "/home/<user>";
      return "C:\\Users\\<user>";
    })
    .replace(/\b[a-z0-9_-]{48,}\b/gi, "[redacted]");
}

function constantTimeEqual(left, right) {
  if (!left || !right || left.length !== right.length) return false;
  let diff = 0;
  for (let index = 0; index < left.length; index += 1) {
    diff |= left.charCodeAt(index) ^ right.charCodeAt(index);
  }
  return diff === 0;
}

function base64(bytes) {
  let binary = "";
  const chunk = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunk) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + chunk));
  }
  return btoa(binary);
}

function json(status, payload, retryAfter = 0) {
  const headers = { ...CORS_HEADERS, "content-type": "application/json; charset=utf-8" };
  if (retryAfter > 0) headers["retry-after"] = String(retryAfter);
  return new Response(JSON.stringify(payload), { status, headers });
}
