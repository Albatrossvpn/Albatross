// A stand-in for Cloudflare D1 over SQLite (node:sqlite), with the service's own
// migrations applied, so a test runs the real SQL. Only what the service uses:
// prepare().bind().first()/all()/run() and batch().
import { readFileSync, readdirSync } from "node:fs";
import { DatabaseSync } from "node:sqlite";

const migrations = new URL("../migrations/", import.meta.url);

export function d1() {
  const db = new DatabaseSync(":memory:");
  for (const f of readdirSync(migrations).filter((n) => n.endsWith(".sql")).sort()) db.exec(readFileSync(new URL(f, migrations), "utf8"));
  const statement = (sql, args = []) => ({
    bind: (...a) => statement(sql, a),
    first: async () => db.prepare(sql).get(...args) ?? null,
    all: async () => ({ results: db.prepare(sql).all(...args).map((r) => ({ ...r })) }),
    run: async () => {
      const r = db.prepare(sql).run(...args);
      return { meta: { changes: Number(r.changes), last_row_id: Number(r.lastInsertRowid) } };
    },
  });
  return {
    db,
    prepare: (sql) => statement(sql),
    batch: async (list) => {
      db.exec("BEGIN");
      try {
        const out = [];
        for (const s of list) out.push(await s.run());
        db.exec("COMMIT");
        return out;
      } catch (e) {
        db.exec("ROLLBACK");
        throw e;
      }
    },
  };
}
