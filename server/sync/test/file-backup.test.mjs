import test from 'node:test';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import {
  mkdtemp,
  mkdir,
  writeFile,
  readFile,
  rm,
  link,
  unlink,
} from 'node:fs/promises';
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
  await writeFile(
    path.join(root, user, '.upload-incomplete'),
    'unfinished upload',
  );
  await mkdir(path.join(root, 'catalog'));
  await writeFile(path.join(root, 'catalog', 'b'.repeat(64)), 'public image');
  const key = randomBytes(32),
    backup = path.join(dir, 'assets.enc');
  await createFileBackup(backup, key, root);
  await verifyFileBackup(backup, key, path.join(dir, 'verified.tar'));
  const original = await readFile(backup);
  await assert.rejects(createFileBackup(backup, key, root), { code: 'EEXIST' });
  assert.deepEqual(
    await readFile(backup),
    original,
    'existing backup must survive a failed duplicate',
  );
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

test('concurrent archive creation preserves the winner and failed tar leaves no partial file', async (t) => {
  const dir = await mkdtemp(path.join(tmpdir(), 'duoyu-assets-test-'));
  t.after(async () => {
    if (!path.resolve(dir).startsWith(path.resolve(tmpdir()) + path.sep))
      throw Error('Invalid cleanup target');
    await rm(dir, { recursive: true, force: true });
  });
  const root = path.join(dir, 'users');
  const key = randomBytes(32);
  const backup = path.join(dir, 'race.enc');
  const outcomes = await Promise.allSettled([
    createFileBackup(backup, key, root),
    createFileBackup(backup, key, root),
  ]);
  assert.equal(outcomes.filter((r) => r.status === 'fulfilled').length, 1);
  assert.equal(
    outcomes.find((r) => r.status === 'rejected').reason.code,
    'EEXIST',
  );
  await verifyFileBackup(backup, key, path.join(dir, 'race.tar'));

  const failed = path.join(dir, 'failed.enc');
  const originalPath = process.env.PATH;
  try {
    process.env.PATH = '';
    await assert.rejects(createFileBackup(failed, key, root), {
      code: 'ENOENT',
    });
  } finally {
    if (originalPath === undefined) delete process.env.PATH;
    else process.env.PATH = originalPath;
  }
  await assert.rejects(readFile(failed), { code: 'ENOENT' });
  await createFileBackup(failed, key, root);
  await verifyFileBackup(failed, key, path.join(dir, 'retry.tar'));
});

test('authenticated archives still reject unexpected names and hard links', async (t) => {
  const dir = await mkdtemp(path.join(tmpdir(), 'duoyu-assets-test-'));
  t.after(async () => {
    if (!path.resolve(dir).startsWith(path.resolve(tmpdir()) + path.sep))
      throw Error('Invalid cleanup target');
    await rm(dir, { recursive: true, force: true });
  });
  const root = path.join(dir, 'users');
  const user = '11111111-1111-4111-8111-111111111111';
  await mkdir(path.join(root, user), { recursive: true });
  const key = randomBytes(32);
  const unexpected = path.join(root, user, 'unexpected.txt');
  await writeFile(unexpected, 'not a hashed asset');
  await createFileBackup(path.join(dir, 'names.enc'), key, root);
  await assert.rejects(
    verifyFileBackup(
      path.join(dir, 'names.enc'),
      key,
      path.join(dir, 'names.tar'),
    ),
    /Unsafe asset archive entry/,
  );
  await unlink(unexpected);
  const asset = path.join(root, user, 'a'.repeat(64));
  await writeFile(asset, 'image');
  await link(asset, path.join(root, user, 'b'.repeat(64)));
  await createFileBackup(path.join(dir, 'links.enc'), key, root);
  await assert.rejects(
    verifyFileBackup(
      path.join(dir, 'links.enc'),
      key,
      path.join(dir, 'links.tar'),
    ),
    /Archive links are forbidden/,
  );
});
