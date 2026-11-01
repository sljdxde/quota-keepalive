import { readFile } from "node:fs/promises"
import { createRequire } from "node:module"
import { isAbsolute, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { magpieConfigHome } from "./common.js"

const require = createRequire(import.meta.url)
const PACKAGE = require("../package.json")
const EXACT_VERSION = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/

export const PLUGIN_DIR = fileURLToPath(new URL("..", import.meta.url))
export const PLUGIN_NAME = PACKAGE.name

export function pluginSpecMatches(
  spec,
  { packageName = PLUGIN_NAME, pluginDir = PLUGIN_DIR } = {},
) {
  const value = typeof spec === "string" ? spec.trim() : ""
  if (!value) return false
  if (isAbsolute(value)) return resolve(value) === resolve(pluginDir)

  const npmSpec = value.startsWith("npm:") ? value.slice(4) : value
  if (npmSpec === packageName) return true
  const versionPrefix = `${packageName}@`
  if (!npmSpec.startsWith(versionPrefix)) return false

  const version = npmSpec.slice(versionPrefix.length)
  return version === "latest" || EXACT_VERSION.test(version)
}

function entryMatches(entry) {
  return pluginSpecMatches(entry?.spec)
}

export async function readLivePluginOptions(configHome = magpieConfigHome()) {
  try {
    const parsed = JSON.parse(await readFile(join(configHome, "plugins.json"), "utf8"))
    const entry = (Array.isArray(parsed?.plugins) ? parsed.plugins : []).find(entryMatches)
    return entry?.options && typeof entry.options === "object" && !Array.isArray(entry.options)
      ? entry.options
      : {}
  } catch {
    return {}
  }
}
