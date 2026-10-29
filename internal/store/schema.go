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
