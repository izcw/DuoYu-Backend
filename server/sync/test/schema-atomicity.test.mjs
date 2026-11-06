import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { Client } from 'pg';

const dsn = process.env.DUOYU_RESTORE_TEST_DATABASE_URL;
for (const service of ['sync', 'backend']) {
  test(service + ' schema upgrade rolls back after a late error', { skip: !dsn }, async () => {
    const url = new URL(dsn);
    assert.equal(url.pathname, '/duoyu_sync_test');
    assert.ok(['localhost', '127.0.0.1'].includes(url.hostname));
    const client = new Client({ connectionString: dsn });
    const schema = 'atomic_' + randomUUID().replaceAll('-', '');
    await client.connect();
    try {
      await client.query('CREATE SCHEMA ' + schema);
      await client.query('SET search_path TO ' + schema);
      const sync = await readFile(new URL('../schema.sql', import.meta.url), 'utf8');
      let sql = sync, marker = 'CREATE OR REPLACE FUNCTION sync_backup_has_asset', relation = 'sync_accounts';
      if (service === 'backend') {
        await client.query(sync);
        sql = await readFile(new URL('../../backend/schema.sql', import.meta.url), 'utf8');
        marker = 'ALTER TABLE backend_catalog_files ADD COLUMN IF NOT EXISTS preview_bytes';
        relation = 'backend_users';
      }
      assert.ok(sql.includes(marker));
      await assert.rejects(client.query(sql.replace(marker, 'SELECT 1/0;\n' + marker)), { code: '22012' });
      await client.query('ROLLBACK');
      const entry = await client.query('SELECT to_regclass($1)::text AS name', [schema + '.' + relation]);
      assert.equal(entry.rows[0].name, null, 'failed upgrade left a partial schema');
      await client.query(sql);
      await client.query(sql);
      assert.equal((await client.query('SELECT to_regclass($1)::text AS name', [schema + '.' + relation])).rows[0].name, relation);
    } finally {
      try {
        await client.query('ROLLBACK');
        await client.query('DROP SCHEMA IF EXISTS ' + schema + ' CASCADE');
      } finally { await client.end(); }
    }
  });
}
