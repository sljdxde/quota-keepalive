import assert from "node:assert/strict";
import test from "node:test";

import { petAimPoint } from "../frontend/assets/pet-gaze.js";

const stage = { left: 0, top: 0, right: 300, bottom: 220, width: 300, height: 220 };
const avatar = { left: 90, top: 50, right: 210, bottom: 170, width: 120, height: 120 };

test("pet gaze is centered when the pointer is at the avatar center", function () {
  const aim = petAimPoint(150, 100.4, stage, avatar);
  assert.ok(aim);
  assert.ok(Math.abs(aim.x) < 0.0001);
  assert.ok(Math.abs(aim.y) < 0.0001);
});

test("pet gaze turns both left and right symmetrically", function () {
  const left = petAimPoint(10, 100, stage, avatar);
  const right = petAimPoint(290, 100, stage, avatar);
  assert.ok(left && right);
  assert.ok(left.x < 0);
  assert.ok(right.x > 0);
  assert.ok(Math.abs(left.x + right.x) < 0.0001);
});

test("pet gaze looks up and down and resets outside the stage", function () {
  const up = petAimPoint(150, 10, stage, avatar);
  const down = petAimPoint(150, 210, stage, avatar);
  assert.ok(up && down);
  assert.ok(up.y < 0);
  assert.ok(down.y > 0);
  assert.equal(petAimPoint(-1, 20, stage, avatar), null);
});
