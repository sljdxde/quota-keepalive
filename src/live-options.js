import { readFile } from "node:fs/promises"
import { isAbsolute, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { magpieConfigHome } from "./common.js"

export const PLUGIN_DIR = fileURLToPath(new URL("..", import.meta.url))

function entryMatches(entry) {
  const spec = typeof entry?.spec === "string" ? entry.spec.trim() : ""
  if (!spec || !isAbsolute(spec)) return false
  return resolve(spec) === resolve(PLUGIN_DIR)
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
