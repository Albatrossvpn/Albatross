-- Plans (tiers) and the devices an account is used on. The plan decides
-- how many devices may be connected at once (src/lib.js TIERS).
ALTER TABLE users ADD COLUMN tier TEXT NOT NULL DEFAULT 'bronze';

-- One row per device that signed in to an account: the apps name
-- themselves with a random number kept on the device. active_until is
-- when the slot it holds, if any, is given up (src/index.js claimSlot).
CREATE TABLE IF NOT EXISTS devices (
  user_id    INTEGER NOT NULL,
  device_id  TEXT    NOT NULL,
  name       TEXT    NOT NULL DEFAULT '',
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  active_until INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, device_id)
);
CREATE INDEX IF NOT EXISTS devices_active ON devices (user_id, active_until);
