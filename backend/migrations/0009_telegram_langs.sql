-- The language a person picked in the Telegram bot (src/telegram.js,
-- src/i18n). It is kept apart from tg_users: a chat is forgotten half an hour
-- after its last message, and the person should not have to pick again. The
-- key is a keyed hash of the person's Telegram number (the same the rate
-- limits use), never the number. Times are unix seconds; a language nobody
-- used for half a year is forgotten.
CREATE TABLE IF NOT EXISTS tg_langs (
  tag     TEXT    PRIMARY KEY,
  lang    TEXT    NOT NULL,
  updated INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS tg_langs_updated ON tg_langs (updated);
