-- Ids of usage reports already applied, so a report whose answer was lost
-- and that the app sends again is counted once (src/index.js usage).
CREATE TABLE IF NOT EXISTS usage_ids (
  id  TEXT    PRIMARY KEY,
  day INTEGER NOT NULL
);
