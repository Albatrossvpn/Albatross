-- The Telegram support bot (src/telegram.js). Times are unix seconds.
-- A chat is kept while it goes on and for half an hour after: then its summary
-- is mailed and its rows are deleted. A ticket's messages are kept until it is
-- closed. Nothing here is a copy of the account service's users.

-- Who is talking to the bot (a private chat's id is the person's own id).
CREATE TABLE IF NOT EXISTS tg_users (
  chat_id     INTEGER PRIMARY KEY,
  name        TEXT    NOT NULL DEFAULT '',   -- first name, as Telegram gives it
  username    TEXT    NOT NULL DEFAULT '',
  mode        TEXT    NOT NULL DEFAULT '',   -- '' the assistant answers; 'ticket' the team does; 'problem' the next message describes it
  device      TEXT    NOT NULL DEFAULT '',   -- what was picked in the guide for a connection that fails
  ticket_id   INTEGER NOT NULL DEFAULT 0,    -- the open ticket, if any
  last_update INTEGER NOT NULL DEFAULT 0,    -- the last update id handled: Telegram may send one twice
  updated     INTEGER NOT NULL,              -- the last message
  blocked     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS tg_users_updated ON tg_users (updated);

CREATE TABLE IF NOT EXISTS tg_messages (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  chat_id INTEGER NOT NULL,
  role    TEXT    NOT NULL,                  -- user, assistant or team
  content TEXT    NOT NULL,
  at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS tg_messages_chat ON tg_messages (chat_id, id);

CREATE TABLE IF NOT EXISTS tg_tickets (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  chat_id   INTEGER NOT NULL,
  status    TEXT    NOT NULL DEFAULT 'open', -- open or closed
  opened    INTEGER NOT NULL,
  updated   INTEGER NOT NULL,
  closed_by TEXT    NOT NULL DEFAULT ''      -- user, team or idle
);
CREATE INDEX IF NOT EXISTS tg_tickets_open ON tg_tickets (status, updated);

-- Which ticket a message in the team's chat belongs to, so that replying to it answers the person.
CREATE TABLE IF NOT EXISTS tg_relay (
  team_chat  INTEGER NOT NULL,
  message_id INTEGER NOT NULL,
  ticket_id  INTEGER NOT NULL,
  at         INTEGER NOT NULL,
  PRIMARY KEY (team_chat, message_id)
);

-- Counts per day (chats, answers, thumbs, tickets), for the team's /stats.
CREATE TABLE IF NOT EXISTS tg_stats (
  day TEXT    NOT NULL,
  key TEXT    NOT NULL,
  n   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (day, key)
);
