import assert from "node:assert/strict";
import test from "node:test";

import { resolveAccountProviderView } from "../frontend/assets/account-view.js";

const providers = [
  { id: "workbuddy", accounts: [{ key: "a" }], models: [{}] },
  { id: "codebuddy", accounts: [], models: [{}] },
  { id: "qoder", accounts: [{ key: "b" }], models: [{}] },
];

test("all providers keeps every signed-in provider", function () {
  const view = resolveAccountProviderView(providers, "");
  assert.equal(view.selectedProvider, "");
  assert.deepEqual(view.providers.map(function (item) { return item.id; }), [
    "workbuddy",
    "qoder",
  ]);
});

test("switching back from an empty provider restores signed-in providers", function () {
  const empty = resolveAccountProviderView(providers, "codebuddy");
  assert.deepEqual(empty.providers.map(function (item) { return item.id; }), [
    "codebuddy",
  ]);
  const restored = resolveAccountProviderView(providers, "workbuddy");
  assert.equal(restored.selectedProvider, "workbuddy");
  assert.deepEqual(restored.providers.map(function (item) {
    return item.id;
  }), ["workbuddy"]);
  assert.equal(restored.providers[0].accounts.length, 1);
});

test("a stale provider value falls back to the complete signed-in view", function () {
  const view = resolveAccountProviderView(providers, "removed-provider");
  assert.equal(view.selectedProvider, "");
  assert.deepEqual(view.providers.map(function (item) { return item.id; }), [
    "workbuddy",
    "qoder",
  ]);
});
