import { describe, expect, test } from "bun:test";
import { readFile } from "node:fs/promises";

const ROOT = new URL("..", import.meta.url);
const LIGHT_CARD = "#FFFFFF";
const DARK_CARD = "#232227";
const MIN_CONTRAST = 3;
const PROVIDER_ICONS = [
  "qoder.svg",
  "opencode.svg",
  "trae.svg",
];

function luminance(hex) {
  const channels = [1, 3, 5].map((offset) => Number.parseInt(hex.slice(offset, offset + 2), 16) / 255);
  const linear = channels.map((value) => (
    value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
  ));
  return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
}

function contrast(a, b) {
  const first = luminance(a);
  const second = luminance(b);
  return (Math.max(first, second) + 0.05) / (Math.min(first, second) + 0.05);
}

describe("provider icon theme visibility", () => {
  for (const name of PROVIDER_ICONS) {
    test(`${name} keeps a visible static fill in light and dark themes`, async () => {
      const svg = await readFile(new URL(`ui/provider-icons/${name}`, ROOT), "utf8");
      expect(svg).not.toContain("currentColor");
      const match = svg.match(/<svg[^>]*\bfill="(#[0-9A-F]{6})"/i);
      expect(match).not.toBeNull();
      const color = match?.[1];
      expect(contrast(color, LIGHT_CARD)).toBeGreaterThanOrEqual(MIN_CONTRAST);
      expect(contrast(color, DARK_CARD)).toBeGreaterThanOrEqual(MIN_CONTRAST);
    });
  }
});
