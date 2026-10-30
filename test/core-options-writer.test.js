import { describe, expect, test } from "bun:test"
import "../ui/options-writer.js"

const writerApi = globalThis.QuotaKeepaliveOptionsWriter

function deferred() {
  let resolve
  const promise = new Promise((done) => {
    resolve = done
  })
  return { promise, resolve }
}

describe("page options writer", () => {
  test("serializes writes, coalesces pending changes, and keeps the latest state", async () => {
    let clock = 0
    const sleeps = []
    const writes = []
    const release = deferred()
    const writer = writerApi.create({
      minIntervalMs: 1_200,
      now: () => clock,
      sleep: async (ms) => {
        sleeps.push(ms)
        clock += ms
      },
      set: async (options) => {
        writes.push(options)
        if (writes.length === 1) await release.promise
        return options
      },
    })

    const first = writer.write({ version: 1 })
    await Promise.resolve()
    const second = writer.write({ version: 2 })
    const third = writer.write({ version: 3 })
    expect(writes).toEqual([{ version: 1 }])

    release.resolve()
    expect(await Promise.all([first, second, third])).toEqual([
      { version: 1 },
      { version: 3 },
      { version: 3 },
    ])
    expect(writes).toEqual([{ version: 1 }, { version: 3 }])
    expect(sleeps).toEqual([1_200])
  })

  test("retries one server-side rate-limit response", async () => {
    let clock = 0
    let attempts = 0
    const sleeps = []
    const writer = writerApi.create({
      minIntervalMs: 1_200,
      now: () => clock,
      sleep: async (ms) => {
        sleeps.push(ms)
        clock += ms
      },
      set: async (options) => {
        attempts++
        if (attempts === 1) throw new Error("a page sets its options once a second at most")
        return options
      },
    })

    expect(await writer.write({ saved: true })).toEqual({ saved: true })
    expect(attempts).toBe(2)
    expect(sleeps).toEqual([1_200])
  })

  test("rejects invalid options and non-rate-limit write failures", async () => {
    const writer = writerApi.create({ set: async () => ({}) })
    await expect(writer.write(null)).rejects.toThrow("plugin options must be an object")
    await expect(writer.write([])).rejects.toThrow("plugin options must be an object")
    expect(() => writerApi.create()).toThrow("requires a set function")

    const failing = writerApi.create({ set: async () => { throw new Error("disk full") } })
    const result = failing.write({ value: 1 })
    await expect(result).rejects.toThrow("disk full")
  })

  test("uses the real clock and timer for the default write interval", async () => {
    const writes = []
    const writer = writerApi.create({
      minIntervalMs: 1,
      set: async (options) => {
        writes.push(options)
        return options
      },
    })
    expect(await writer.write({ step: 1 })).toEqual({ step: 1 })
    expect(await writer.write({ step: 2 })).toEqual({ step: 2 })
    expect(writes).toEqual([{ step: 1 }, { step: 2 }])
  })
})
