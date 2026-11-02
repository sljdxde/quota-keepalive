#!/usr/bin/env node

import { resolve } from "node:path"
import {
  defaultLogPath,
  formatLogRecords,
  readDailyLogs,
} from "../src/log.js"

const STATUSES = new Set(["started", "success", "failed", "skipped"])

function usage() {
  return `Usage: workbuddy-daily-log [options]

Options:
  --limit N          Show the last N matching records (default 50, max 500)
  --day YYYY-MM-DD   Filter by Beijing date
  --status STATUS    started, success, failed, or skipped
  --file PATH        Read one exact log file instead of current plus previous
  --json             Print JSONL instead of human-readable lines
  -h, --help         Show this help

Default log:
  ${defaultLogPath()}`
}

function parse(args) {
  const out = { limit: 50, json: false }
  for (let i = 0; i < args.length; i++) {
    const arg = args[i]
    if (arg === "-h" || arg === "--help") return { help: true }
    if (arg === "--json") {
      out.json = true
      continue
    }
    if (!["--limit", "--day", "--status", "--file"].includes(arg)) {
      throw new Error(`unknown option: ${arg}`)
    }
    const value = args[++i]
    if (value === undefined || value.startsWith("--")) {
      throw new Error(`${arg} needs a value`)
    }
    if (arg === "--limit") out.limit = Number(value)
    if (arg === "--day") out.day = value
    if (arg === "--status") out.status = value
    if (arg === "--file") out.file = resolve(value)
  }
  if (!Number.isInteger(out.limit) || out.limit < 1 || out.limit > 500) {
    throw new Error("--limit must be an integer from 1 to 500")
  }
  if (out.day && !/^\d{4}-\d{2}-\d{2}$/.test(out.day)) {
    throw new Error("--day must be YYYY-MM-DD")
  }
  if (out.status && !STATUSES.has(out.status)) {
    throw new Error("--status must be started, success, failed, or skipped")
  }
  return out
}

async function main() {
  const args = parse(process.argv.slice(2))
  if (args.help) {
    console.log(usage())
    return
  }
  const logPath = args.file || process.env.WORKBUDDY_DAILY_LOG || defaultLogPath()
  const result = await readDailyLogs({
    logPath,
    includePrevious: !args.file,
    day: args.day || "",
    status: args.status || "",
    limit: args.limit,
  })
  if (result.malformed) {
    console.error(`Skipped ${result.malformed} malformed or unrelated JSONL line(s).`)
  }
  if (!result.records.length) {
    console.log("没有找到符合条件的日志记录。")
    console.log(`已查看:\n${result.files.join("\n")}`)
    return
  }
  if (args.json) {
    console.log(result.records.map((record) => JSON.stringify(record)).join("\n"))
  } else {
    console.log(formatLogRecords(result.records))
  }
}

main().catch((error) => {
  console.error(`读取日志失败: ${error instanceof Error ? error.message : error}`)
  process.exitCode = 1
})
