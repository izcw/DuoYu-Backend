import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer, request } from 'node:http';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';
import { createShutdown } from '../src/lifecycle.mjs';
import { Pool } from 'pg';

test('shutdown drains accepted HTTP work before ending storage, once across signals', async (t) => {
  let entered,
    release,
    ended = 0;
  const started = new Promise((resolve) => {
    entered = resolve;
  });
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  const server = createServer(async (_req, res) => {
    entered();
    await gate;
    res.end(ended ? 'storage closed too early' : 'committed');
  });
  t.after(() => {
    release();
    server.closeAllConnections();
    server.close();
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const response = fetch('http://127.0.0.1:' + server.address().port);
  await started;
  const close = createShutdown(
    server,
    {
      async end() {
        ended++;
      },
    },
    2000,
  );
  const first = close(),
    second = close();
  assert.equal(first, second);
  await delay(20);
  assert.equal(server.listening, false);
  assert.equal(ended, 0);
  release();
  assert.equal(await (await response).text(), 'committed');
  await first;
  await close();
  assert.equal(ended, 1);
});

test('shutdown deadline closes hung connections and reports incomplete draining', async (t) => {
  let entered,
    ended = 0;
  const started = new Promise((resolve) => {
    entered = resolve;
  });
  const server = createServer(() => entered());
  t.after(() => {
    server.closeAllConnections();
    server.close();
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const req = request('http://127.0.0.1:' + server.address().port);
  const disconnected = new Promise((resolve) => req.once('error', resolve));
  req.end();
  await started;
  const close = createShutdown(
    server,
    {
      async end() {
        ended++;
      },
    },
    30,
  );
  await assert.rejects(close(), /grace period exceeded/);
  assert.equal((await disconnected).code, 'ECONNRESET');
  assert.equal(ended, 1);
});

test('shutdown releases storage even if the HTTP server never started', async () => {
  let ended = 0;
  await createShutdown(createServer(), {
    async end() {
      ended++;
    },
  })();
  assert.equal(ended, 1);
});

test(
  'shutdown lets an accepted request acquire a real database connection after draining begins',
  { skip: !process.env.DUOYU_RESTORE_TEST_DATABASE_URL },
  async (t) => {
    const url = new URL(process.env.DUOYU_RESTORE_TEST_DATABASE_URL);
    assert.ok(['localhost', '127.0.0.1'].includes(url.hostname));
    assert.equal(url.pathname, '/duoyu_sync_test');
    const pool = new Pool({ connectionString: url.href });
    let entered, release;
    const started = new Promise((resolve) => {
      entered = resolve;
    });
    const gate = new Promise((resolve) => {
      release = resolve;
    });
    const server = createServer(async (_req, res) => {
      entered();
      await gate;
      try {
        const result = await pool.query(
          'SELECT current_database() AS database',
        );
        res.end(result.rows[0].database);
      } catch {
        res.writeHead(500);
        res.end('database unavailable');
      }
    });
    const close = createShutdown(server, pool, 5000);
    t.after(async () => {
      release();
      server.closeAllConnections();
      await close().catch(() => {});
    });
    server.listen(0, '127.0.0.1');
    await once(server, 'listening');
    const response = fetch('http://127.0.0.1:' + server.address().port);
    await started;
    const finished = close();
    release();
    const result = await response;
    assert.equal(result.status, 200);
    assert.equal(await result.text(), 'duoyu_sync_test');
    await finished;
    await assert.rejects(pool.query('SELECT 1'), /after calling end/);
  },
);
