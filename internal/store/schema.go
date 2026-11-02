package store

// schemaV1 建立账号、登录态来源、每日操作（含执行租约）与日志所需表。
// 全部使用 IF NOT EXISTS，配合版本登记实现可重复执行（幂等）。
const schemaV1 = `
CREATE TABLE IF NOT EXISTS accounts (
    id              TEXT NOT NULL,
    target          TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    masked_identity TEXT NOT NULL DEFAULT '',
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    PRIMARY KEY (target, id)
);

CREATE TABLE IF NOT EXISTS sources (
    id             TEXT NOT NULL,
    target         TEXT NOT NULL,
    account_id     TEXT NOT NULL,
    kind           TEXT NOT NULL,
    read_only      INTEGER NOT NULL DEFAULT 0,
    health         TEXT NOT NULL DEFAULT 'healthy',
    pinned         INTEGER NOT NULL DEFAULT 0,
    credential_ref TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    PRIMARY KEY (target, account_id, id),
    FOREIGN KEY (target, account_id) REFERENCES accounts (target, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS daily_ops (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    target           TEXT NOT NULL,
    account_id       TEXT NOT NULL,
    operation        TEXT NOT NULL,
    day_key          TEXT NOT NULL,
    state            TEXT NOT NULL DEFAULT 'pending',
    lease_holder     TEXT NOT NULL DEFAULT '',
    lease_expires_at TEXT NOT NULL DEFAULT '',
    attempts         INTEGER NOT NULL DEFAULT 0,
    cooldown_until   TEXT NOT NULL DEFAULT '',
    credits          INTEGER NOT NULL DEFAULT 0,
    balance          INTEGER NOT NULL DEFAULT 0,
    message          TEXT NOT NULL DEFAULT '',
    updated_at       TEXT NOT NULL,
    UNIQUE (target, account_id, operation, day_key)
);

CREATE TABLE IF NOT EXISTS logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    ts         TEXT NOT NULL,
    target     TEXT NOT NULL DEFAULT '',
    account_id TEXT NOT NULL DEFAULT '',
    operation  TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL DEFAULT '',
    level      TEXT NOT NULL DEFAULT 'info',
    message    TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_logs_ts ON logs (ts);
CREATE INDEX IF NOT EXISTS idx_logs_filter ON logs (target, account_id, operation, state);
`

// schemaV2 distinguishes a real zero quota reading from "quota unknown".
const schemaV2 = `
ALTER TABLE daily_ops ADD COLUMN credits_known INTEGER NOT NULL DEFAULT 0;
ALTER TABLE daily_ops ADD COLUMN balance_known INTEGER NOT NULL DEFAULT 0;
`

// schemaV3 stores per-account automation preferences. Missing rows mean the
// operation is enabled, so existing accounts keep their current behavior.
const schemaV3 = `
CREATE TABLE IF NOT EXISTS account_task_preferences (
    target     TEXT NOT NULL,
    account_id TEXT NOT NULL,
    operation  TEXT NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (target, account_id, operation),
    FOREIGN KEY (target, account_id) REFERENCES accounts (target, id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_account_task_preferences_account
ON account_task_preferences (target, account_id);
`

// schemaV4 persists provider plugins, provider enablement, run history, and
// quota observations. Credentials remain outside SQLite and are referenced by
// the existing managed source row.
const schemaV4 = `
CREATE TABLE IF NOT EXISTS provider_plugins (
    spec       TEXT PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    version    TEXT NOT NULL DEFAULT '',
    source     TEXT NOT NULL DEFAULT 'bundled',
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS providers (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    plugin_spec TEXT NOT NULL DEFAULT '',
    icon        TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL DEFAULT 1,
    usage       INTEGER NOT NULL DEFAULT 0,
    checkin     INTEGER NOT NULL DEFAULT 0,
    keepalive   INTEGER NOT NULL DEFAULT 0,
    models_json TEXT NOT NULL DEFAULT '[]',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
    id              TEXT PRIMARY KEY,
    started_at      TEXT NOT NULL,
    finished_at     TEXT NOT NULL DEFAULT '',
    action          TEXT NOT NULL,
    state           TEXT NOT NULL,
    attempted       INTEGER NOT NULL DEFAULT 0,
    succeeded       INTEGER NOT NULL DEFAULT 0,
    needs_attention INTEGER NOT NULL DEFAULT 0,
    skipped         INTEGER NOT NULL DEFAULT 0,
    message         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_runs_started ON runs (started_at);

CREATE TABLE IF NOT EXISTS run_items (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id     TEXT NOT NULL,
    provider   TEXT NOT NULL,
    account_id TEXT NOT NULL DEFAULT '',
    action     TEXT NOT NULL,
    state      TEXT NOT NULL,
    message    TEXT NOT NULL DEFAULT '',
    credits    REAL,
    balance    REAL,
    model      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_run_items_run ON run_items (run_id);

CREATE TABLE IF NOT EXISTS quota_readings (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    provider    TEXT NOT NULL,
    account_id  TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    plan        TEXT NOT NULL DEFAULT '',
    until       TEXT NOT NULL DEFAULT '',
    balance     TEXT NOT NULL DEFAULT '',
    windows_json TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS idx_quota_readings_account
ON quota_readings (provider, account_id, observed_at);
`

// schemaV5 records the stable upstream identity behind a reusable plugin auth
// slot. A slot reused by a different account must not inherit QKA history.
const schemaV5 = `
CREATE TABLE IF NOT EXISTS provider_account_identities (
    provider      TEXT NOT NULL,
    slot_key      TEXT NOT NULL,
    identity_hash TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    PRIMARY KEY (provider, slot_key)
);
`

// schemaV6 makes WorkBuddy the only enabled provider once on upgraded
// installations. Later user toggles are preserved by normal provider updates.
const schemaV6 = `
UPDATE providers
SET enabled = CASE WHEN id = 'workbuddy' THEN 1 ELSE 0 END;
`
