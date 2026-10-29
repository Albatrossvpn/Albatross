-- The Telegram bot's schedule (src/telegram.js) removes the message ids of
-- tickets closed long ago every minute: by time, so this index.
CREATE INDEX IF NOT EXISTS tg_relay_at ON tg_relay (at);
