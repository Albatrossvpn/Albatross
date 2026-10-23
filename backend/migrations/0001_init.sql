-- Account service database (Cloudflare D1). Applied by migrations/0001_init.sql.
-- Zero limits mean unlimited. Times are unix seconds.
CREATE TABLE IF NOT EXISTS users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
  pass_hash     TEXT    NOT NULL,
  created_at    INTEGER NOT NULL,
  disabled      INTEGER NOT NULL DEFAULT 0,
  quota_bytes   INTEGER NOT NULL DEFAULT 0,  -- data allowed per period
  max_seconds   INTEGER NOT NULL DEFAULT 0,  -- connected time allowed per period
  expires_at    INTEGER NOT NULL DEFAULT 0,  -- account end date
  reset_days    INTEGER NOT NULL DEFAULT 0,  -- the allowance renews every N days (0 = never)
  period_start  INTEGER NOT NULL,
  used_bytes    INTEGER NOT NULL DEFAULT 0,  -- this period
  used_seconds  INTEGER NOT NULL DEFAULT 0,  -- this period
  total_bytes   INTEGER NOT NULL DEFAULT 0,
  total_seconds INTEGER NOT NULL DEFAULT 0,
  last_seen     INTEGER NOT NULL DEFAULT 0,
  token_version INTEGER NOT NULL DEFAULT 0,  -- raised to sign every device out
  note          TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS users_last_seen ON users (last_seen);

-- Failed sign-ins and sign-ups per username/address tag (no addresses stored).
CREATE TABLE IF NOT EXISTS attempts (
  key   TEXT PRIMARY KEY,
  count INTEGER NOT NULL,
  since INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS attempts_since ON attempts (since);
