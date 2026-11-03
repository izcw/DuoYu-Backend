import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID, createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { Pool } from 'pg';
import { applyOperation } from '../src/store.mjs';
import { createSyncServer } from '../src/http.mjs';

test('account backup restore is isolated, revision guarded, atomic and retryable', { skip: !process.env.DUOYU_RESTORE_TEST_DATABASE_URL }, async () => {
 const pool = new Pool({ connectionString: process.env.DUOYU_RESTORE_TEST_DATABASE_URL });
 let server;
 try {
  await pool.query(await readFile(new URL('../schema.sql', import.meta.url), 'utf8'));
  const epoch = (await pool.query('SELECT epoch::text FROM sync_generation')).rows[0].epoch;
  const account = { id: randomUUID(), issuer: 'https://test.invalid', sub: randomUUID() };
  const deviceId = randomUUID();
  const commit = changes => applyOperation(pool, account, { id: randomUUID(), deviceId, epoch, changes });
  const page = { id: 'p', name: 'original', order: 0 };
  await commit([{ table: 'libraryPages', id: 'p', base: null, value: page, revision: '0' }]);
  const backup = (await pool.query('SELECT id::text FROM sync_user_backups WHERE account_id=$1', [account.id])).rows[0].id;
  await commit([{ table: 'libraryPages', id: 'p', base: page, value: null, revision: '1' }]);
  const request = { id: randomUUID(), deviceId, epoch };
  await assert.rejects(applyOperation(pool, account, request, { id: backup, revision: '1' }), e => e.code === 'backup_revision_changed');
  await assert.rejects(applyOperation(pool, account, request, { id: randomUUID(), revision: '2' }), e => e.code === 'backup_not_found');
  const result = await applyOperation(pool, account, request, { id: backup, revision: '2' });
  assert.equal(result.status, 'applied');
  assert.equal(result.changes[0].deleted, false);
  assert.deepEqual(result.changes[0].payload, page);
  assert.deepEqual(await applyOperation(pool, account, request, { id: backup, revision: '2' }), result);
  server = createSyncServer({ pool, authenticate: async () => account, origins: [] });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const root = 'http://127.0.0.1:' + server.address().port;
  const listed = await (await fetch(root + '/api/v1/sync/backups')).json();
  assert.ok(listed.backups.some(row => row.id === backup));
  assert.ok(listed.backups.length <= 5);
  const unchanged = await commit([{ table: 'libraryPages', id: 'p', base: page, value: page, revision: '3' }]);
  assert.deepEqual(unchanged.changes, []);
  assert.equal(String(unchanged.revision), '3');
  assert.equal((await pool.query('SELECT count(*)::int AS n FROM sync_user_backups WHERE account_id=$1', [account.id])).rows[0].n, 3);
  const other = { id: randomUUID(), issuer: account.issuer, sub: randomUUID() };
  await assert.rejects(applyOperation(pool, other, { id: randomUUID(), deviceId, epoch }, { id: backup, revision: '0' }), e => e.code === 'backup_not_found');
  const sha = createHash('sha256').update('missing').digest('hex');
  await pool.query(`UPDATE sync_user_backups SET snapshot=jsonb_set(snapshot,'{assets}',$2::jsonb) WHERE id=$1`, [backup, JSON.stringify({ [sha]: { body: null, media_type: 'image/png', bytes: 7 } })]);
  await assert.rejects(applyOperation(pool, account, { id: randomUUID(), deviceId, epoch }, { id: backup, revision: '3' }), e => e.code === 'backup_asset_unavailable');
  assert.equal(String((await pool.query('SELECT head_revision FROM sync_accounts WHERE id=$1', [account.id])).rows[0].head_revision), '3');
  const bytes = Buffer.from('missing');
  await pool.query(`UPDATE sync_user_backups SET snapshot=jsonb_set(snapshot,'{assets}',$2::jsonb) WHERE id=$1`, [backup, JSON.stringify({ [sha]: { body: bytes.toString('base64'), media_type: 'image/png', bytes: bytes.length } })]);
  const recovered = await applyOperation(pool, account, { id: randomUUID(), deviceId, epoch }, { id: backup, revision: '3' });
  assert.equal(recovered.status, 'applied');
  assert.deepEqual((await pool.query('SELECT body FROM sync_assets WHERE account_id=$1 AND sha256=$2', [account.id, sha])).rows[0].body, bytes);

 } finally { if (server) await new Promise(resolve => server.close(resolve)); await pool.end(); }
});
