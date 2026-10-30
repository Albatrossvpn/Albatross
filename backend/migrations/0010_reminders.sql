-- An account bought in the Telegram bot is reminded, once a period, when it
-- runs low on data or days (src/telegram.js remindRenewals): when that was,
-- in unix seconds. A recharge starts a new period and so a new reminder.
ALTER TABLE users ADD COLUMN reminded_at INTEGER NOT NULL DEFAULT 0;
