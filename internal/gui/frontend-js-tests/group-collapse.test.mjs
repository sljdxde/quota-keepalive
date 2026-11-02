import assert from "node:assert/strict";
import test from "node:test";

import { nextGroupExpansion } from "../frontend/assets/group-collapse.js";

test("partially collapsed groups collapse all on the first click", function () {
  const targets = ["workbuddy", "deepseek"];
  const next = nextGroupExpansion(targets, { workbuddy: true, deepseek: false });
  assert.deepEqual(next, { workbuddy: false, deepseek: false });
});

test("fully collapsed groups expand all on the next click", function () {
  const targets = ["workbuddy", "deepseek"];
  const next = nextGroupExpansion(targets, { workbuddy: false, deepseek: false });
  assert.deepEqual(next, { workbuddy: true, deepseek: true });
});

test("groups without explicit state count as expanded", function () {
  const targets = ["workbuddy"];
  assert.deepEqual(nextGroupExpansion(targets, {}), { workbuddy: false });
  assert.deepEqual(
    nextGroupExpansion(["workbuddy"], { workbuddy: true }),
    { workbuddy: false }
  );
});

test("an empty list keeps an empty result and does not mutate input", function () {
  const expanded = { workbuddy: true };
  assert.deepEqual(nextGroupExpansion([], expanded), {});
  assert.deepEqual(nextGroupExpansion(null, null), {});
  assert.deepEqual(expanded, { workbuddy: true });
});

test("two clicks restore the original expanded state", function () {
  const targets = ["workbuddy", "codebuddy"];
  const start = { workbuddy: true, codebuddy: true };
  const collapsed = nextGroupExpansion(targets, start);
  const restored = nextGroupExpansion(targets, collapsed);
  assert.deepEqual(restored, start);
});
