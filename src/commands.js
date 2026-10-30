import { randomBytes } from "node:crypto"
import { chmod, mkdir, open, readFile, rename, stat, unlink } from "node:fs/promises"
import { dirname } from "node:path"
import { errorText, sanitize } from "./common.js"

const VERSION = 1
const ACTIONS = new Set(["detect", "checkin", "keepalive", "all", "refresh"])
const CONSUMED_LIMIT = 64
const RUNNING_STALE_MS = 10 * 60 * 1000
const FILE_OPS = { chmod, mkdir, open, readFile, rename, stat, unlink }

function emptyState() {
  return {
    version: VERSION,
    current: null,
    consumed: [],
    keepaliveClaims: [],
  }
}

function validAction(value) {
  return typeof value === "string" && ACTIONS.has(value)
}

function validState(value) {
  return !!value &&
    typeof value === "object" &&
    !Array.isArray(value) &&
    value.version === VERSION &&
    (value.current == null || (typeof value.current === "object" && !Array.isArray(value.current))) &&
    Array.isArray(value.consumed) &&
    Array.isArray(value.keepaliveClaims)
}

export async function readState(path) {
  let raw
  try {
    raw = await readFile(path, "utf8")
  } catch (error) {
    if (error?.code === "ENOENT") return emptyState()
    throw error
  }
  let state
  try {
    state = JSON.parse(raw)
  } catch {
    throw new Error(`${path} is not valid JSON; it was left unchanged`)
  }
  if (!validState(state)) {
    throw new Error(`${path} has an unsupported command shape; it was left unchanged`)
  }
  return state
}

export async function writeState(path, state, ops = FILE_OPS) {
  await ops.mkdir(dirname(path), { recursive: true, mode: 0o700 })
  const tmp = `${path}.tmp-${process.pid}-${randomBytes(6).toString("hex")}`
  let handle
  try {
    handle = await ops.open(tmp, "wx", 0o600)
    await handle.writeFile(`${JSON.stringify(state, null, 2)}\n`, "utf8")
    await handle.sync()
    await handle.close()
    handle = null
    await ops.rename(tmp, path)
    await ops.chmod(path, 0o600)
  } catch (error) {
    if (handle) {
      try {
        await handle.close()
      } catch {}
    }
    try {
      await ops.unlink(tmp)
    } catch {}
    throw error
  }
}

export async function withLock(path, work, waitMs = 2_000, ops = FILE_OPS) {
  const lockPath = `${path}.lock`
  const deadline = Date.now() + waitMs
  let handle
  for (;;) {
    try {
      handle = await ops.open(lockPath, "wx", 0o600)
      await handle.writeFile(JSON.stringify({ pid: process.pid, at: new Date().toISOString() }))
      await handle.close()
      break
    } catch (error) {
      if (handle) {
        try {
          await handle.close()
        } catch {}
      }
      if (error?.code !== "EEXIST") throw error
      let age = 0
      try {
        age = Date.now() - (await ops.stat(lockPath)).mtimeMs
      } catch {
        continue
      }
      if (age > 30_000) {
        await ops.unlink(lockPath).catch(() => {})
        continue
      }
      if (Date.now() >= deadline) throw new Error("the daily command queue is busy")
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
  }
  try {
    return await work()
  } finally {
    try {
      await ops.unlink(lockPath)
    } catch {}
  }
}

export function newCommandId(now = new Date()) {
  const stamp = now.toISOString().replace(/[-:]/g, "").replace(/\.\d{3}Z$/, "Z")
  return `${stamp}-${randomBytes(4).toString("hex")}`
}

export async function enqueueCommand({
  commandPath,
  action,
  source = "manual",
  id = "",
  provider = "",
}) {
  if (!validAction(action)) throw new Error("action must be detect, checkin, keepalive, all, or refresh")
  return withLock(commandPath, async () => {
    const state = await readState(commandPath)
    const wanted = sanitize(id, 80) || newCommandId()
    if (state.consumed.includes(wanted) || state.current?.id === wanted) {
      return { queued: false, command: state.current }
    }
    if (state.current && ["queued", "running"].includes(state.current.status)) {
      return { queued: false, command: state.current, busy: true }
    }
    state.current = {
      id: wanted,
      action,
      provider: sanitize(provider, 80),
      source: sanitize(source, 40) || "manual",
      status: "queued",
      createdAt: new Date().toISOString(),
    }
    state.keepaliveClaims = []
    await writeState(commandPath, state)
    return { queued: true, command: state.current }
  })
}

export async function claimCommand({ commandPath, id = "" }) {
  return withLock(commandPath, async () => {
    const state = await readState(commandPath)
    const command = state.current
    if (!command || (id && command.id !== id) || state.consumed.includes(command.id)) {
      return null
    }
    if (command.status === "running") {
      const started = Date.parse(command.startedAt || command.createdAt)
      if (Number.isFinite(started) && Date.now() - started < RUNNING_STALE_MS) return null
    }
    if (command.status !== "queued" && command.status !== "running") return null
    state.current = {
      ...command,
      status: "running",
      startedAt: new Date().toISOString(),
      error: "",
    }
    await writeState(commandPath, state)
    return state.current
  })
}

export async function finishCommand({
  commandPath,
  id,
  status,
  error = "",
}) {
  if (!["success", "failed", "skipped"].includes(status)) {
    throw new Error("status must be success, failed, or skipped")
  }
  return withLock(commandPath, async () => {
    const state = await readState(commandPath)
    if (!state.current || state.current.id !== id) return null
    state.current = {
      ...state.current,
      status,
      finishedAt: new Date().toISOString(),
      error: sanitize(errorText(error), 480),
    }
    state.consumed = [id, ...state.consumed.filter((x) => x !== id)].slice(0, CONSUMED_LIMIT)
    state.keepaliveClaims = []
    await writeState(commandPath, state)
    return state.current
  })
}

export async function readCommand(commandPath) {
  try {
    const state = await readState(commandPath)
    return state.current
  } catch {
    return null
  }
}

// A manual keepalive command is claimed once per account before the network
// request. The claim prevents two accounts or two concurrent runners from
// sending the same account twice if a run is interrupted.
export async function claimForcedKeepalive({ commandPath, account }) {
  return withLock(commandPath, async () => {
    const state = await readState(commandPath)
    const command = state.current
    if (!command ||
        !["keepalive", "all"].includes(command.action) ||
        command.status !== "running" ||
        !account) {
      return false
    }
    if (state.keepaliveClaims.includes(account)) return false
    state.keepaliveClaims.push(account)
    await writeState(commandPath, state)
    return true
  })
}

export function commandActionSpends(action) {
  return action === "keepalive" || action === "all"
}

export async function recordCommandResult({
  commandPath,
  id,
  status,
  summary = "",
  error = "",
  provider,
  accounts = [],
}) {
  return withLock(commandPath, async () => {
    const state = await readState(commandPath)
    if (!state.current || state.current.id !== id) return null
    state.current = {
      ...state.current,
      status,
      finishedAt: new Date().toISOString(),
      summary: sanitize(summary, 480),
      error: sanitize(errorText(error), 480),
      ...(provider !== undefined ? { provider: sanitize(provider, 80) } : {}),
      accounts: (Array.isArray(accounts) ? accounts : []).slice(0, 80).map((account) => ({
        provider: sanitize(account?.provider, 80),
        account: sanitize(account?.account, 80),
        action: sanitize(account?.action, 24),
        status: sanitize(account?.status, 24),
        outcome: sanitize(account?.outcome, 32),
        message: sanitize(account?.message, 300),
      })),
    }
    state.consumed = [id, ...state.consumed.filter((x) => x !== id)].slice(0, CONSUMED_LIMIT)
    state.keepaliveClaims = []
    await writeState(commandPath, state)
    return state.current
  })
}
