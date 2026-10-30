import test from 'node:test';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { mkdtemp, mkdir, writeFile, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { createFileBackup, verifyFileBackup } from '../src/file-backup.mjs';
await test('encrypted file backup validates hash-tree archive and rejects corruption', async (t) => {
  const dir = await mkdtemp(path.join(tmpdir(), 'duoyu-assets-test-'));
  t.after(async () => {
    if (!path.resolve(dir).startsWith(path.resolve(tmpdir()) + path.sep))
      throw Error('Invalid cleanup target');
    await rm(dir, { recursive: true, force: true });
  });
  const root = path.join(dir, 'users'),
    user = '11111111-1111-4111-8111-111111111111',
    hash = 'a'.repeat(64);
  await mkdir(path.join(root, user), { recursive: true });
  await writeFile(path.join(root, user, hash), 'sample private image');
  const key = randomBytes(32),
    backup = path.join(dir, 'assets.enc');
  await createFileBackup(backup, key, root);
  await verifyFileBackup(backup, key, path.join(dir, 'verified.tar'));
  const damaged = await readFile(backup);
  damaged[damaged.length - 1] ^= 1;
  await writeFile(path.join(dir, 'damaged.enc'), damaged);
  await assert.rejects(
    verifyFileBackup(
      path.join(dir, 'damaged.enc'),
      key,
      path.join(dir, 'damaged.tar'),
    ),
  );
});
