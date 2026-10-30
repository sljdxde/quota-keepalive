import { randomBytes } from "node:crypto"
import { chmod, mkdir, open, readFile, rename, stat, unlink } from "node:fs/promises"
import { dirname } from "node:path"
import { errorText, sanitize, terminalQuotaFailure } from "./common.js"

const VERSION = 1
const KEEP_DAYS = 32
const memory = new Map()
const FILE_OPS = { chmod, mkdir, open, readFile, rename, stat, unlink }

export class StateFileError extends Error {
  constructor(message) {
    super(message)
    this.name = "StateFileError"
  }
}

export class StateBusyError extends Error {
  constructor(message) {
    super(message)
    this.name = "StateBusyError"
  }
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

function stateKey(statePath, account, day) {
  return `${statePath}\n${account}\n${day}`
}

function memoryFor(statePath, account, day) {
  return memory.get(stateKey(statePath, account, day)) || null
}

function remember(statePath, account, entry) {
  if (!entry) return
  memory.set(stateKey(statePath, account, entry.day), entry)
  if (memory.size > 256) {
    const first = memory.keys().next().value
    if (first) memory.delete(first)
  }
}

function validEntry(entry) {
  return !!entry &&
    typeof entry === "object" &&
    typeof entry.day === "string" &&
    ["ok", "failed", "running"].includes(entry.status) &&
    Number.isInteger(entry.attempts) &&
    entry.attempts >= 1
}

function terminalFailure(entry) {
  if (entry?.terminal === true) return true
  if (entry?.status !== "failed" || typeof entry?.error !== "string") return false
  const status = /HTTP\s+429\b/.test(entry.error)
    ? 429
    : /HTTP\s+402\b/.test(entry.error)
      ? 402
      : 0
  return terminalQuotaFailure(status, entry.error)
}

export async function readDocument(path, ops = FILE_OPS) {
  let raw
  try {
    raw = await ops.readFile(path, "utf8")
  } catch (error) {
    if (error?.code === "ENOENT") return { version: VERSION, accounts: {} }
    throw new StateFileError(`cannot read ${path}: ${errorText(error)}`)
  }
  let doc
  try {
    doc = JSON.parse(raw)
  } catch {
    throw new StateFileError(`${path} is not valid JSON; it was left unchanged`)
  }
  if (!doc || typeof doc !== "object" || doc.version !== VERSION || !doc.accounts ||
      typeof doc.accounts !== "object" || Array.isArray(doc.accounts)) {
    throw new StateFileError(`${path} has an unsupported shape; it was left unchanged`)
  }
  for (const [key, entry] of Object.entries(doc.accounts)) {
    if (!validEntry(entry)) {
      throw new StateFileError(`${path} has an invalid entry (${sanitize(key, 80)}); it was left unchanged`)
    }
  }
  return doc
}

function prune(doc, day) {
  const at = Date.parse(`${day}T00:00:00Z`)
  if (!Number.isFinite(at)) return doc
  const cutoff = new Date(at - KEEP_DAYS * 86_400_000).toISOString().slice(0, 10)
  for (const [key, entry] of Object.entries(doc.accounts)) {
    if (typeof entry?.day === "string" && entry.day < cutoff) delete doc.accounts[key]
  }
  return doc
}

export async function writeDocument(path, doc, ops = FILE_OPS) {
  await ops.mkdir(dirname(path), { recursive: true, mode: 0o700 })
  const tmp = `${path}.tmp-${process.pid}-${randomBytes(6).toString("hex")}`
  let handle
  try {
    handle = await ops.open(tmp, "wx", 0o600)
    await handle.writeFile(`${JSON.stringify(doc, null, 2)}\n`, "utf8")
    await handle.sync()
    await handle.close()
    handle = null
    for (let attempt = 0; ; attempt++) {
      try {
        await ops.rename(tmp, path)
        break
      } catch (error) {
        if (attempt >= 20 || !["EPERM", "EACCES", "EBUSY"].includes(error?.code)) throw error
        await sleep(10)
      }
    }
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

export async function acquireLock(lockPath, waitMs, staleMs, ops = FILE_OPS) {
  const deadline = Date.now() + waitMs
  for (;;) {
    let handle
    try {
      handle = await ops.open(lockPath, "wx", 0o600)
      await handle.writeFile(JSON.stringify({ pid: process.pid, at: new Date().toISOString() }))
      await handle.close()
      return async () => {
        try {
          await ops.unlink(lockPath)
        } catch (error) {
          if (error?.code !== "ENOENT") throw error
        }
      }
    } catch (error) {
      if (handle) {
        await handle.close().catch(() => {})
      }
      if (error?.code !== "EEXIST") {
        throw new StateFileError(`cannot lock ${lockPath}: ${errorText(error)}`)
      }
      let age = 0
      try {
        age = Date.now() - (await ops.stat(lockPath)).mtimeMs
      } catch {
        continue
      }
      if (age > staleMs) {
        try {
          await ops.unlink(lockPath)
        } catch {}
        continue
      }
      if (Date.now() >= deadline) {
        throw new StateBusyError(`another process is running this daily task (${lockPath})`)
      }
      await sleep(100)
    }
  }
}

function newer(a, b) {
  if (!a) return b
  if (!b) return a
  if (a.day !== b.day) return a.day > b.day ? a : b
  if (a.status === "ok" || b.status === "ok") return a.status === "ok" ? a : b
  return (a.attempts || 0) >= (b.attempts || 0) ? a : b
}

export async function runDailyOnce({
  statePath,
  account,
  day,
  now = new Date(),
  model,
  inputTokens,
  maxAttempts,
  lockWaitMs,
  timeoutMs,
  force = false,
  task,
  writeState = null,
  fileOps = FILE_OPS,
}) {
  const lockPath = `${statePath}.lock`
  const persist = writeState || ((path, doc) => writeDocument(path, doc, fileOps))
  let release
  try {
    release = await acquireLock(
      lockPath,
      lockWaitMs,
      Math.max(30_000, timeoutMs + 30_000),
      fileOps,
    )
  } catch (error) {
    return { kind: "busy", error: errorText(error) }
  }

  try {
    const inMemory = memoryFor(statePath, account, day)
    if (inMemory?.status === "ok") {
      return { kind: "skipped", reason: "done-today", attempts: inMemory.attempts }
    }
    if (terminalFailure(inMemory) && !force) {
      return {
        kind: "skipped",
        reason: "quota-exhausted",
        attempts: inMemory.attempts,
        error: inMemory.error || "",
      }
    }

    let doc
    try {
      doc = await readDocument(statePath, fileOps)
    } catch (error) {
      return { kind: "unavailable", error: errorText(error) }
    }
    prune(doc, day)
    let previous = newer(doc.accounts[account], inMemory)
    let interrupted = false
    if (previous?.day === day) {
      const startedAt = Date.parse(previous.at || "")
      const staleMs = Math.max(30_000, timeoutMs + 30_000)
      if (previous.status === "running" && Number.isFinite(startedAt) &&
          now.getTime() - startedAt > staleMs) {
        interrupted = true
        previous = {
          ...previous,
          status: "failed",
          at: new Date(startedAt).toISOString(),
          error: "the previous attempt was interrupted",
        }
      }
      remember(statePath, account, previous)
      if (previous.status === "ok") {
        return { kind: "skipped", reason: "done-today", attempts: previous.attempts }
      }
      if (terminalFailure(previous) && !force) {
        return {
          kind: "skipped",
          reason: "quota-exhausted",
          attempts: previous.attempts,
          error: previous.error || "",
        }
      }
      if (previous.attempts >= maxAttempts && !force && !interrupted) {
        return {
          kind: "skipped",
          reason: "exhausted",
          attempts: previous.attempts,
          error: previous.error || "",
        }
      }
    }

    const attempts = (previous?.day === day ? previous.attempts : 0) + 1
    const attemptEntry = {
      day,
      status: "running",
      attempts,
      at: now.toISOString(),
      model,
      inputTokens,
    }
    doc.accounts[account] = attemptEntry
    remember(statePath, account, attemptEntry)
    try {
      await persist(statePath, doc)
    } catch (error) {
      return { kind: "unavailable", error: `cannot save keepalive state: ${errorText(error)}` }
    }

    try {
      const result = await task()
      const okEntry = {
        ...attemptEntry,
        status: "ok",
        at: now.toISOString(),
      }
      doc.accounts[account] = okEntry
      remember(statePath, account, okEntry)
      let persisted = true
      try {
        await persist(statePath, doc)
      } catch {
        persisted = false
      }
      return { kind: "sent", ok: true, attempts, persisted, result }
    } catch (error) {
      const failedEntry = {
        ...attemptEntry,
        status: "failed",
        terminal: error?.retryToday === false,
        at: now.toISOString(),
        error: errorText(error),
      }
      doc.accounts[account] = failedEntry
      remember(statePath, account, failedEntry)
      let persisted = true
      try {
        await persist(statePath, doc)
      } catch {
        persisted = false
      }
      return {
        kind: "sent",
        ok: false,
        attempts,
        persisted,
        error: failedEntry.error,
        cause: error,
      }
    }
  } finally {
    try {
      await release()
    } catch {}
  }
}
