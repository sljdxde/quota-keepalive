import assert from "node:assert/strict";
import test from "node:test";

import { createHandler } from "./index.js";

const PNG = Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3]);
const PNG_DATA = `data:image/png;base64,${Buffer.from(PNG).toString("base64")}`;

const ENV = {
  GITHUB_TOKEN: "fixture",
  SITE_KEY: "site-key",
  GITHUB_REPOSITORY: "o/r",
};

function request(body, key = "site-key") {
  return new Request("https://feedback.test/api/feedback", {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-qka-key": key,
      "cf-connecting-ip": "127.0.0.1",
    },
    body: JSON.stringify(body),
  });
}

function payload(overrides = {}) {
  return {
    title: "[用户反馈] 刷新失败 @alice",
    description: "刷新额度失败 token=secret user@example.com",
    logs: "HTTP 429 token=secret",
    version: "1.0.0",
    platform: "darwin/arm64",
    images: [{ name: "screenshot", data_url: PNG_DATA }],
    ...overrides,
  };
}

function githubMock(calls) {
  return async (url, options = {}) => {
    calls.push({ url, options });
    if (url.endsWith("/git/ref/heads/feedback-assets")) {
      return Response.json({ object: { sha: "parent-sha" } });
    }
    if (url.includes("/git/commits/parent-sha")) {
      return Response.json({ tree: { sha: "base-tree" } });
    }
    if (url.endsWith("/git/blobs")) return Response.json({ sha: "blob-sha" });
    if (url.endsWith("/git/trees")) return Response.json({ sha: "tree-sha" });
    if (url.endsWith("/git/commits") && options.method === "POST") {
      return Response.json({ sha: "commit-sha" });
    }
    if (url.includes("/git/refs/heads/")) return Response.json({});
    if (url.endsWith("/issues")) {
      return Response.json({ number: 42, html_url: "https://github.com/o/r/issues/42" });
    }
    throw new Error(`unexpected GitHub call: ${url}`);
  };
}

test("creates one issue and references uploaded images", async () => {
  const calls = [];
  const handler = createHandler({
    githubFetch: githubMock(calls),
    now: () => Date.parse("2026-11-02T12:00:00Z"),
    random: () => "fixed",
  });
  const response = await handler(request(payload()), ENV);
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), {
    ok: true,
    number: 42,
    url: "https://github.com/o/r/issues/42",
  });

  const issue = calls.find((call) => call.url.endsWith("/issues"));
  const issueBody = JSON.parse(issue.options.body);
  assert.match(issueBody.body, /raw\.githubusercontent\.com\/o\/r\/feedback-assets/);
  assert.match(issueBody.body, /1\.png/);
  assert.match(issueBody.body, /刷新额度失败/);
  assert.doesNotMatch(issueBody.body, /data:image/);
  assert.doesNotMatch(issueBody.body, /test-token/);
  assert.match(issueBody.body, /\[redacted\]/);
  assert.doesNotMatch(issueBody.body, /user@example\.com/);
});

test("rejects invalid site key and SVG", async () => {
  const handler = createHandler({ githubFetch: githubMock([]) });
  let response = await handler(request(payload(), "wrong"), ENV);
  assert.equal(response.status, 401);

  response = await handler(request(payload({
    images: [{ data_url: "data:image/svg+xml;base64,PHN2Zz4=" }],
  })), ENV);
  assert.equal(response.status, 400);
});

test("rate limits repeated submissions", async () => {
  const handler = createHandler({ githubFetch: githubMock([]) });
  for (let index = 0; index < 3; index += 1) {
    const response = await handler(request(payload({ images: [] })), ENV);
    assert.equal(response.status, 200);
  }
  const response = await handler(request(payload()), ENV);
  assert.equal(response.status, 429);
  assert.ok(Number(response.headers.get("retry-after")) > 0);
});

test("create branch when feedback branch does not exist", async () => {
  const calls = [];
  const githubFetch = async (url, options = {}) => {
    calls.push({ url, options });
    if (url.endsWith("/git/ref/heads/feedback-assets") && !calls.some(
      (call) => call.url.endsWith("/git/refs") && call.options.method === "POST")) {
      return new Response("missing", { status: 404 });
    }
    if (url.endsWith("/git/ref/heads/main")) return Response.json({ object: { sha: "main-sha" } });
    if (url.endsWith("/git/refs") && options.method === "POST") return Response.json({});
    return githubMock(calls)(url, options);
  };
  const handler = createHandler({ githubFetch, random: () => "fixed" });
  const response = await handler(request(payload()), ENV);
  assert.equal(response.status, 200);
  assert.ok(calls.some((call) => call.url.endsWith("/git/refs") && call.options.method === "POST"));
});
