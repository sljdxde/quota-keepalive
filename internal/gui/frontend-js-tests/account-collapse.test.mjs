import assert from "node:assert/strict";
import test from "node:test";

import {
  accountStateKey,
  nextAccountExpansion,
  shouldCollapseAccount,
} from "../frontend/assets/account-collapse.js";

test("accounts start collapsed unless explicitly expanded", function () {
  const key = accountStateKey("workbuddy", "wb_1");
  assert.equal(key, "workbuddy/wb_1");
  assert.equal(shouldCollapseAccount(new Set(), key), true);
  assert.equal(shouldCollapseAccount(new Set([key]), key), false);
});

test("toggling expands a collapsed account then collapses it again", function () {
  const expanded = new Set();
  const key = accountStateKey("workbuddy", "wb_1");
  const first = nextAccountExpansion(expanded, key);
  assert.equal(first, true);
  expanded.add(key);
  assert.equal(nextAccountExpansion(expanded, key), false);
});

test("missing account key never expands", function () {
  assert.equal(shouldCollapseAccount(new Set(["workbuddy/"]), ""), true);
  assert.equal(nextAccountExpansion(new Set(), ""), false);
  assert.equal(accountStateKey("", "wb_1"), "");
});

test("array form of expanded keys is supported", function () {
  assert.equal(shouldCollapseAccount(["a/1"], "a/1"), false);
  assert.equal(shouldCollapseAccount(["a/1"], "a/2"), true);
});

test("unknown container collapses by default", function () {
  assert.equal(shouldCollapseAccount(null, "a/1"), true);
  assert.equal(shouldCollapseAccount(undefined, "a/1"), true);
});
