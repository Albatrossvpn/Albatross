-- Opt-in, anonymous connection statistics from the apps (see crowd in
-- src/index.js): per network operator (ASN) and day, how many tests of each
-- kind of connection passed. Nothing identifies a device or an address.
CREATE TABLE IF NOT EXISTS crowd (
  asn  INTEGER NOT NULL,
  kind TEXT    NOT NULL,  -- proto, style, hello or port
  code INTEGER NOT NULL,
  day  INTEGER NOT NULL,  -- unix days
  ok   INTEGER NOT NULL DEFAULT 0,
  n    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (asn, kind, code, day)
);
CREATE TABLE IF NOT EXISTS crowd_reports (
  asn     INTEGER NOT NULL,
  day     INTEGER NOT NULL,
  reports INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (asn, day)
);
