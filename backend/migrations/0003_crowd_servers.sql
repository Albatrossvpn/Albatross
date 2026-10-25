-- Opt-in, anonymous reports from the apps about servers: per server key
-- (a 64-bit hash of the server's identity, no address) and day, how many
-- tests passed from users' networks. The service sums them hourly into
-- list/servers/crowd.json for the collector (cmd/collect -crowd).
CREATE TABLE IF NOT EXISTS crowd_servers (
  key TEXT    NOT NULL,
  day INTEGER NOT NULL,  -- unix days
  ok  INTEGER NOT NULL DEFAULT 0,
  n   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (key, day)
);
