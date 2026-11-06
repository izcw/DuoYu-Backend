import test from "node:test";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { readFile } from "node:fs/promises";
import { Pool } from "pg";
import {
  applyOperation,
  validateOperation,
  validateGraph,
} from "../src/store.mjs";
import { createSyncServer } from "../src/http.mjs";

test(
  "large account restore is atomic, isolated and idempotent without widening upload limits",
  {
    skip: !process.env.DUOYU_RESTORE_TEST_DATABASE_URL,
    timeout: 120000,
  },
  async () => {
    const dsn = new URL(process.env.DUOYU_RESTORE_TEST_DATABASE_URL);
    assert.ok(["127.0.0.1", "localhost"].includes(dsn.hostname));
    assert.equal(dsn.port, "55432");
    assert.equal(dsn.pathname, "/duoyu_sync_test");
    const schema = "restore_large_" + randomUUID().replaceAll("-", "");
    const owner = new Pool({ connectionString: dsn.href });
    let pool, server;
    try {
      await owner.query("CREATE SCHEMA " + schema);
      pool = new Pool({
        connectionString: dsn.href,
        options: "-c search_path=" + schema,
      });
      await pool.query(
        await readFile(new URL("../schema.sql", import.meta.url), "utf8"),
      );
      const epoch = (
        await pool.query("SELECT epoch::text FROM sync_generation")
      ).rows[0].epoch;
      const account = {
        id: randomUUID(),
        issuer: "https://restore.invalid",
        sub: randomUUID(),
      };
      const other = {
        id: randomUUID(),
        issuer: account.issuer,
        sub: randomUUID(),
      };
      const deviceId = randomUUID();
      const pages = Array.from({ length: 2001 }, (_, i) => ({
        id: "tab-" + i,
        collectionId: "collection",
        title: "Original " + i,
        url: "https://example.invalid/" + i,
        position: i,
      }));
      const creation = [
        {
          table: "workspaceSpaces",
          id: "space",
          value: { id: "space", name: "Space" },
        },
        {
          table: "tabCollections",
          id: "collection",
          value: { id: "collection", spaceId: "space", name: "Collection" },
        },
        ...pages.map((value) => ({ table: "savedTabs", id: value.id, value })),
      ].map((item) => ({ ...item, base: null, revision: "0" }));
      const commit = (changes) =>
        applyOperation(pool, account, {
          id: randomUUID(),
          deviceId,
          epoch,
          changes,
        });
      await commit(creation.slice(0, 2000));
      await commit(creation.slice(2000));
      const backup = (
        await pool.query(
          "SELECT id::text FROM sync_user_backups WHERE account_id=$1 AND revision=2",
          [account.id],
        )
      ).rows[0].id;
      const originals = (
        await pool.query(
          "SELECT entity_id AS id,payload,revision::text FROM sync_entities WHERE account_id=$1 AND entity_type='savedTabs' ORDER BY entity_id",
          [account.id],
        )
      ).rows;
      const changed = originals.map((row) => ({
        table: "savedTabs",
        id: row.id,
        base: row.payload,
        value: { ...row.payload, title: "Changed " + row.id },
        revision: row.revision,
      }));
      await commit(changed.slice(0, 2000));
      await commit(changed.slice(2000));
      assert.throws(
        () =>
          validateOperation({ id: randomUUID(), deviceId, changes: changed }),
        (e) => e.code === "invalid_operation",
      );
      let current = account;
      server = createSyncServer({
        pool,
        authenticate: async () => current,
        origins: [],
      });
      await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
      const root = "http://127.0.0.1:" + server.address().port;
      const restore = (body) =>
        fetch(root + "/api/v1/sync/backups/" + backup + "/restore", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
      const request = { id: randomUUID(), deviceId, epoch, revision: "4" };
      const state = async () =>
        (
          await pool.query(
            `SELECT jsonb_build_object(
      'revision',(SELECT head_revision::text FROM sync_accounts WHERE id=$1),
      'entities',(SELECT jsonb_agg(to_jsonb(e) ORDER BY entity_type,entity_id) FROM sync_entities e WHERE account_id=$1),
      'receipts',(SELECT count(*) FROM sync_receipts WHERE account_id=$1),
      'changes',(SELECT count(*) FROM sync_changes WHERE account_id=$1)) value`,
            [account.id],
          )
        ).rows[0].value;
      const before = await state();
      current = other;
      let response = await restore({ ...request, revision: "0" });
      assert.equal(response.status, 404);
      await response.arrayBuffer();
      current = account;
      response = await restore({ ...request, revision: "3" });
      assert.equal(response.status, 409);
      await response.arrayBuffer();
      // Force a real mid-transaction failure after some rows have been updated.
      await pool.query(`CREATE FUNCTION reject_large_restore() RETURNS trigger LANGUAGE plpgsql AS $$
      BEGIN IF NEW.revision=5 AND NEW.entity_id='tab-999' THEN RAISE EXCEPTION 'isolated restore failure'; END IF; RETURN NEW; END $$;
      CREATE TRIGGER reject_large_restore BEFORE UPDATE ON sync_entities FOR EACH ROW EXECUTE FUNCTION reject_large_restore();`);
      response = await restore(request);
      const failure = await response.json();
      assert.equal(
        response.status,
        500,
        "large restore must reach the transactional update rather than be rejected as invalid_operation: " +
          JSON.stringify(failure),
      );
      assert.deepEqual(
        await state(),
        before,
        "partial restore must roll back entities, revision, receipts and changes",
      );
      await pool.query("DROP TRIGGER reject_large_restore ON sync_entities");
      response = await restore(request);
      assert.equal(response.status, 200);
      const result = await response.json();
      assert.equal(result.status, "applied");
      assert.equal(result.revision, "5");
      assert.equal(result.changes.length, 2001);
      const restored = (
        await pool.query(
          "SELECT entity_id AS id,payload FROM sync_entities WHERE account_id=$1 AND entity_type='savedTabs' ORDER BY entity_id",
          [account.id],
        )
      ).rows;
      assert.deepEqual(
        restored,
        originals.map(({ id, payload }) => ({ id, payload })),
      );
      response = await restore(request);
      assert.equal(response.status, 200);
      assert.deepEqual(
        await response.json(),
        result,
        "lost-response retry must return the original receipt",
      );
      assert.equal((await state()).revision, "5");
      // Exercise the exact 10,000-entity boundary, including the parent graph.
      // Existing quota counts tombstones too; preserve that compatibility rule.
      const maximum = { ...account, id: randomUUID(), sub: randomUUID() };
      const graph = (prefix) =>
        [
          {
            table: "workspaceSpaces",
            id: "space",
            payload: { id: "space", name: prefix + "Space" },
          },
          {
            table: "tabCollections",
            id: "collection",
            payload: {
              id: "collection",
              spaceId: "space",
              name: prefix + "Collection",
            },
          },
          ...Array.from({ length: 9998 }, (_, i) => ({
            table: "savedTabs",
            id: "tab-" + i,
            payload: {
              id: "tab-" + i,
              collectionId: "collection",
              title: prefix + i,
              url: "https://example.invalid/" + i,
              position: i,
            },
          })),
        ].map((row) => ({ ...row, deleted: false, revision: "1" }));
      const savedGraph = graph("saved-"),
        liveGraph = graph("live-");
      validateGraph(savedGraph);
      validateGraph(liveGraph);
      assert.throws(
        () =>
          validateGraph([
            ...liveGraph,
            {
              ...liveGraph[2],
              id: "overflow",
              payload: {
                ...liveGraph[2].payload,
                id: "overflow",
                position: 9998,
              },
            },
          ]),
        (e) => e.code === "account_data_quota",
      );
      await pool.query(
        "INSERT INTO sync_accounts(id,issuer,subject,head_revision) VALUES($1,$2,$3,1)",
        [maximum.id, maximum.issuer, maximum.sub],
      );
      await pool.query(
        `INSERT INTO sync_entities(account_id,entity_type,entity_id,payload,deleted,revision)
        SELECT $1,r.table,r.id,r.payload,false,1 FROM jsonb_to_recordset($2::jsonb) AS r("table" text,id text,payload jsonb)`,
        [maximum.id, JSON.stringify(liveGraph)],
      );
      const fullBackup = randomUUID();
      await pool.query(
        "INSERT INTO sync_user_backups(id,account_id,epoch,revision,snapshot) VALUES($1,$2,$3,0,$4)",
        [
          fullBackup,
          maximum.id,
          epoch,
          JSON.stringify({ entities: savedGraph, assets: {} }),
        ],
      );
      current = maximum;
      response = await fetch(
        root + "/api/v1/sync/backups/" + fullBackup + "/restore",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            id: randomUUID(),
            deviceId,
            epoch,
            revision: "1",
          }),
        },
      );
      const replacement = await response.json();
      assert.equal(
        response.status,
        200,
        "full account must restore all 10,000 entities: " + replacement.error,
      );
      assert.equal(replacement.changes.length, 10000);
      assert.equal(replacement.revision, "2");
      const finalGraph = (
        await pool.query(
          "SELECT entity_type AS table,entity_id AS id,payload FROM sync_entities WHERE account_id=$1 AND NOT deleted ORDER BY entity_id",
          [maximum.id],
        )
      ).rows;
      assert.deepEqual(
        finalGraph,
        savedGraph
          .map(({ table, id, payload }) => ({ table, id, payload }))
          .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)),
      );
      assert.equal(
        (
          await pool.query(
            "SELECT count(*)::int AS count FROM sync_user_backups WHERE account_id=$1",
            [account.id],
          )
        ).rows[0].count,
        5,
      );
      current = account;
      // An ordinary HTTP write still cannot submit a >2,000-change batch.
      response = await fetch(root + "/api/v1/sync/operations", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          id: randomUUID(),
          deviceId,
          epoch,
          changes: changed,
        }),
      });
      assert.equal(response.status, 400);
      await response.arrayBuffer();
      assert.equal((await state()).revision, "5");
    } finally {
      if (server) await new Promise((resolve) => server.close(resolve));
      if (pool) await pool.end();
      await owner.query("DROP SCHEMA IF EXISTS " + schema + " CASCADE");
      await owner.end();
    }
  },
);
