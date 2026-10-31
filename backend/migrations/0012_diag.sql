-- Anonymous diagnostics from installs that share statistics (the same
-- consent as the crowd reports): per day, app version, platform and
-- operator (ASN), named counters. Kind "diag" counts events (how
-- connections went and why they did not, how long they took, what the
-- network looked like); kind "demand" counts, per protocol the user chose,
-- the times no server of it could be had. Names and counts only: no
-- address, no install number, nothing of what anyone opened. The admin
-- summary groups them by version and platform, so the next version fixes
-- what breaks where; the collector reads the demand to look harder for the
-- protocols people ask for.
CREATE TABLE IF NOT EXISTS diag (
  day      INTEGER NOT NULL,  -- unix days
  version  TEXT    NOT NULL,
  platform TEXT    NOT NULL,
  asn      INTEGER NOT NULL,
  kind     TEXT    NOT NULL,  -- diag or demand
  code     TEXT    NOT NULL,  -- the event's or the protocol's name
  n        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (day, version, platform, asn, kind, code)
);
CREATE INDEX IF NOT EXISTS diag_day ON diag (day);
