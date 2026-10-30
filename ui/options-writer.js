(function exposeQuotaOptionsWriter(global) {
  function create({
    set,
    minIntervalMs = 1_200,
    now = () => Date.now(),
    sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
  } = {}) {
    if (typeof set !== "function") throw new TypeError("options writer requires a set function")

    let lastWriteAt = null
    let queued = null
    let waiters = []
    let running = false

    async function flush() {
      if (running) return
      running = true
      try {
        while (queued) {
          const wait = lastWriteAt === null
            ? 0
            : Math.max(0, minIntervalMs - (now() - lastWriteAt))
          if (wait > 0) await sleep(wait)
          if (!queued) break

          const options = queued
          const batch = waiters
          queued = null
          waiters = []
          try {
            let saved
            try {
              saved = await set(options)
            } catch (error) {
              const message = String(error?.message || error)
              if (!/once a second/i.test(message)) throw error
              await sleep(minIntervalMs)
              saved = await set(options)
            }
            lastWriteAt = now()
            for (const waiter of batch) waiter.resolve(saved ?? options)
          } catch (error) {
            lastWriteAt = now()
            for (const waiter of batch) waiter.reject(error)
          }
        }
      } finally {
        running = false
        if (queued) void flush()
      }
    }

    function write(options) {
      if (!options || typeof options !== "object" || Array.isArray(options)) {
        return Promise.reject(new TypeError("plugin options must be an object"))
      }
      queued = { ...options }
      const result = new Promise((resolve, reject) => waiters.push({ resolve, reject }))
      void flush()
      return result
    }

    return Object.freeze({ write })
  }

  global.QuotaKeepaliveOptionsWriter = Object.freeze({ create })
})(globalThis)
