-- Selling data with Telegram Stars (src/sales.js, the Telegram bot's "Buy
-- data"). Times are unix seconds.

-- An account bought with Stars remembers the Telegram number of its buyer, so
-- that only that Telegram account can ask the bot for a new password. It goes
-- with the account when the account is deleted. 0: not bought in the bot.
ALTER TABLE users ADD COLUMN tg_owner INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS users_tg_owner ON users (tg_owner);

-- What a person is about to pay for: the package, its price when the order
-- was made, and for whom (a new account, or the username of one to recharge).
-- The invoice carries the order's number; an order nobody paid is forgotten
-- after a day.
CREATE TABLE IF NOT EXISTS stars_orders (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  chat_id INTEGER NOT NULL,                   -- the buyer's Telegram number
  kind    TEXT    NOT NULL DEFAULT 'new',     -- new account, topup, or test
  gb      INTEGER NOT NULL,
  stars   INTEGER NOT NULL,
  days    INTEGER NOT NULL,                   -- how long the data is valid (0: no end)
  target  TEXT    NOT NULL DEFAULT '',        -- topup: the account's username
  status  TEXT    NOT NULL DEFAULT 'new',     -- new, checkout (Telegram asked to pay), paid
  created INTEGER NOT NULL,
  updated INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS stars_orders_created ON stars_orders (created);

-- What Telegram says was paid. The charge id and the buyer's number are what
-- a refund needs, so they are kept, with the account the data went to, for
-- two years.
CREATE TABLE IF NOT EXISTS stars_payments (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  charge_id   TEXT    NOT NULL UNIQUE,        -- Telegram's id of the payment
  chat_id     INTEGER NOT NULL,
  order_id    INTEGER NOT NULL,
  kind        TEXT    NOT NULL,
  gb          INTEGER NOT NULL,
  stars       INTEGER NOT NULL,
  username    TEXT    NOT NULL DEFAULT '',    -- the account that got the data
  grant_bytes INTEGER NOT NULL DEFAULT 0,
  status      TEXT    NOT NULL DEFAULT 'paid', -- paid, delivered, refunded, failed
  created     INTEGER NOT NULL,
  updated     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS stars_payments_created ON stars_payments (created);
CREATE INDEX IF NOT EXISTS stars_payments_status ON stars_payments (status, updated);
