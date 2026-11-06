import test from 'node:test';
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { Readable } from 'node:stream';
import { createSyncServer } from '../src/http.mjs';

test('HTTP validation rejects malformed and oversized bodies without database writes', async (t) => {
  let connections = 0;
  const server = createSyncServer({
    pool: {
      async connect() {
        connections++;
        throw Error('Invalid input reached database');
      },
      async query(sql) {
        assert.equal(sql, 'SELECT 1');
        return { rows: [] };
      },
    },
    authenticate: async () => ({
      id: randomUUID(),
      issuer: 'https://test.invalid',
      sub: 'boundary-test',
    }),
    origins: [],
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(
    () =>
      new Promise((resolve) => {
        server.close(resolve);
        server.closeAllConnections();
      }),
  );
  const origin = 'http://127.0.0.1:' + server.address().port;
  const operations = '/api/v1/sync/operations';
  const restore = '/api/v1/sync/backups/' + randomUUID() + '/restore';
  async function post(route, body, status, error, streamed = false) {
    const response = await fetch(origin + route, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: streamed
        ? Readable.from([body.slice(0, 20), body.slice(20)])
        : body,
      ...(streamed ? { duplex: 'half' } : {}),
      signal: AbortSignal.timeout(5000),
    });
    assert.equal(response.status, status);
    assert.deepEqual(await response.json(), { error });
  }
  await t.test('malformed JSON and empty operations', async () => {
    await post(operations, '{', 400, 'invalid_json');
    await post(operations, 'null', 400, 'invalid_operation');
    await post(restore, '{', 400, 'invalid_json');
    await post(restore, 'null', 400, 'invalid_json');
  });
  await t.test('non-object changes are client errors', async () => {
    for (const change of [null, [], 0, false, 'invalid']) {
      await post(
        operations,
        JSON.stringify({
          id: randomUUID(),
          deviceId: randomUUID(),
          changes: [change],
        }),
        400,
        'invalid_change',
      );
    }
  });
  for (const streamed of [false, true]) {
    await t.test(`restore body limit, streamed=${streamed}`, () =>
      post(
        restore,
        JSON.stringify({ data: 'x'.repeat(4096) }),
        413,
        'body_too_large',
        streamed,
      ),
    );
    await t.test(`operation body limit, streamed=${streamed}`, () =>
      post(
        operations,
        JSON.stringify({ data: 'x'.repeat(4 * 1048576) }),
        413,
        'body_too_large',
        streamed,
      ),
    );
  }
  await t.test(
    'service remains usable and no invalid request reaches storage',
    async () => {
      assert.equal(connections, 0);
      assert.equal((await fetch(origin + '/healthz')).status, 200);
    },
  );
});
