import { randomBytes } from "node:crypto"
import { chmod, mkdir, open, readFile, rename, stat, unlink } from "node:fs/promises"
import { dirname } from "node:path"
import { errorText, sanitize } from "./common.js"

const VERSION = 1
const KEEP_DAYS = 32
const memory = new Map()

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

async function readDocument(path) {
  let raw
  try {
    raw = await readFile(path, "utf8")
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

async function writeDocument(path, doc) {
  await mkdir(dirname(path), { recursive: true, mode: 0o700 })
  const tmp = `${path}.tmp-${process.pid}-${randomBytes(6).toString("hex")}`
  let handle
  try {
    handle = await open(tmp, "wx", 0o600)
    await handle.writeFile(`${JSON.stringify(doc, null, 2)}\n`, "utf8")
    await handle.sync()
    await handle.close()
    handle = null
    for (let attempt = 0; ; attempt++) {
      try {
        await rename(tmp, path)
        break
      } catch (error) {
        if (attempt >= 20 || !["EPERM", "EACCES", "EBUSY"].includes(error?.code)) throw error
        await sleep(10)
      }
    }
    await chmod(path, 0o600)
  } catch (error) {
    if (handle) await handle.close().catch(() => {})
    await unlink(tmp).catch(() => {})
    throw error
  }
}

async function acquireLock(lockPath, waitMs, staleMs) {
  const deadline = Date.now() + waitMs
  for (;;) {
    let handle
    try {
      handle = await open(lockPath, "wx", 0o600)
      await handle.writeFile(JSON.stringify({ pid: process.pid, at: new Date().toISOString() }))
      await handle.close()
      return async () => {
        await unlink(lockPath).catch((error) => {
          if (error?.code !== "ENOENT") throw error
        })
      }
    } catch (error) {
      if (handle) await handle.close().catch(() => {})
      if (error?.code !== "EEXIST") {
        throw new StateFileError(`cannot lock ${lockPath}: ${errorText(error)}`)
      }
      let age = 0
      try {
        age = Date.now() - (await stat(lockPath)).mtimeMs
      } catch {
        continue
      }
      if (age > staleMs) {
        await unlink(lockPath).catch(() => {})
        continue
      }
      if (Date.now() >= deadline) {
        throw new StateBusyError(`another process is sending WorkBuddy's keepalive (${lockPath})`)
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
}) {
  const lockPath = `${statePath}.lock`
  let release
  try {
    release = await acquireLock(
      lockPath,
      lockWaitMs,
      Math.max(30_000, timeoutMs + 30_000),
    )
  } catch (error) {
    return { kind: "busy", error: errorText(error) }
  }

  try {
    const inMemory = memoryFor(statePath, account, day)
    if (inMemory?.status === "ok" && !force) {
      return { kind: "skipped", reason: "done-today", attempts: inMemory.attempts }
    }

    let doc
    try {
      doc = await readDocument(statePath)
    } catch (error) {
      return { kind: "unavailable", error: errorText(error) }
    }
    prune(doc, day)
    const previous = newer(doc.accounts[account], inMemory)
    if (previous?.day === day) {
      remember(statePath, account, previous)
      if (previous.status === "ok" && !force) {
        return { kind: "skipped", reason: "done-today", attempts: previous.attempts }
      }
      if (previous.attempts >= maxAttempts && !force) {
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
      await writeDocument(statePath, doc)
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
        await writeDocument(statePath, doc)
      } catch {
        persisted = false
      }
      return { kind: "sent", ok: true, attempts, persisted, result }
    } catch (error) {
      const failedEntry = {
        ...attemptEntry,
        status: "failed",
        at: now.toISOString(),
        error: errorText(error),
      }
      doc.accounts[account] = failedEntry
      remember(statePath, account, failedEntry)
      let persisted = true
      try {
        await writeDocument(statePath, doc)
      } catch {
        persisted = false
      }
      return {
        kind: "sent",
        ok: false,
        attempts,
        persisted,
        error: failedEntry.error,
      }
    }
  } finally {
    await release().catch(() => {})
  }
}
