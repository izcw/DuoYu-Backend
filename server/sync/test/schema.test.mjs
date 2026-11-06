import test from "node:test";
import assert from "node:assert/strict";
import { randomBytes, randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";
import { Client } from "pg";

test(
  "schema migration is repeatable and isolates accounts outside public",
  {
    skip: !process.env.DUOYU_RESTORE_TEST_DATABASE_URL,
  },
  async () => {
    const url = new URL(process.env.DUOYU_RESTORE_TEST_DATABASE_URL);
    assert.ok(["localhost", "127.0.0.1"].includes(url.hostname));
    assert.equal(url.pathname, "/duoyu_sync_test");
    const db = new Client({ connectionString: url.href });
    const schema = "schema_test_" + randomBytes(8).toString("hex");
    const role = schema + "_reader";
    let schemaCreated = false,
      roleCreated = false;
    await db.connect();
    try {
      await db.query(`CREATE SCHEMA ${schema}`);
      schemaCreated = true;
      await db.query(`SET search_path TO ${schema}`);
      const sql = await readFile(
        new URL("../schema.sql", import.meta.url),
        "utf8",
      );
      await db.query(sql);
      const policies = await db.query(
        "SELECT tablename FROM pg_policies WHERE schemaname=$1 AND policyname='account_isolation' ORDER BY tablename",
        [schema],
      );
      assert.deepEqual(
        policies.rows.map((row) => row.tablename),
        [
          "sync_accounts",
          "sync_asset_deletions",
          "sync_assets",
          "sync_changes",
          "sync_conflicts",
          "sync_entities",
          "sync_receipts",
          "sync_user_backups",
        ],
      );
      await db.query(sql);
      const owner = randomUUID(),
        other = randomUUID();
      await db.query(
        "INSERT INTO sync_accounts(id,issuer,subject) VALUES($1::uuid,$3,$1::text),($2::uuid,$3,$2::text)",
        [owner, other, "https://schema.example.invalid"],
      );
      await db.query(
        `CREATE ROLE ${role} NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS`,
      );
      roleCreated = true;
      await db.query(`GRANT USAGE ON SCHEMA ${schema} TO ${role}`);
      await db.query(`GRANT SELECT ON sync_accounts TO ${role}`);
      await db.query(`SET ROLE ${role}`);
      assert.equal((await db.query("SELECT * FROM sync_accounts")).rowCount, 0);
      await db.query("SELECT set_config('app.account_id',$1,false)", [owner]);
      assert.deepEqual((await db.query("SELECT id FROM sync_accounts")).rows, [
        { id: owner },
      ]);
      await db.query("SELECT set_config('app.account_id',$1,false)", [other]);
      assert.deepEqual((await db.query("SELECT id FROM sync_accounts")).rows, [
        { id: other },
      ]);
    } finally {
      await db.query("ROLLBACK");
      await db.query("RESET ROLE");
      if (schemaCreated) await db.query(`DROP SCHEMA ${schema} CASCADE`);
      if (roleCreated) await db.query(`DROP ROLE ${role}`);
      await db.end();
    }
  },
);
