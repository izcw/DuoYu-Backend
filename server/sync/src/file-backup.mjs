import {
  createCipheriv,
  createDecipheriv,
  randomBytes,
  randomUUID,
} from 'node:crypto';
import { open, readFile, writeFile, unlink, mkdir } from 'node:fs/promises';
import { createReadStream, createWriteStream } from 'node:fs';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { pipeline } from 'node:stream/promises';
const run = promisify(execFile);
export async function createFileBackup(file, key, root) {
  await mkdir(root, { recursive: true, mode: 0o700 });
  const nonce = randomBytes(12),
    cipher = createCipheriv('aes-256-gcm', key, nonce),
    out = createWriteStream(file, { flags: 'wx', mode: 0o600 });
  out.write(Buffer.concat([Buffer.from('DYA1'), nonce]));
  const child = spawn(
    'tar',
    ['--exclude=./*/.upload-*', '-cf', '-', '-C', root, '.'],
    { stdio: ['ignore', 'pipe', 'ignore'] },
  );
  const done = new Promise((resolve, reject) => {
    child.on('error', reject);
    child.on('exit', (c) =>
      c === 0 ? resolve() : reject(Error('Asset archive failed')),
    );
  });
  try {
    await Promise.all([pipeline(child.stdout, cipher, out), done]);
    await writeFile(file, cipher.getAuthTag(), { flag: 'a', mode: 0o600 });
  } catch (e) {
    child.kill();
    await unlink(file).catch(() => {});
    throw e;
  }
}
export async function verifyFileBackup(file, key, temp) {
  const f = await open(file, 'r');
  const size = (await f.stat()).size;
  const head = Buffer.alloc(16),
    tag = Buffer.alloc(16);
  try {
    if (size < 33) throw Error('Invalid archive');
    await f.read(head, 0, 16, 0);
    await f.read(tag, 0, 16, size - 16);
  } finally {
    await f.close();
  }
  if (head.subarray(0, 4).toString() !== 'DYA1')
    throw Error('Invalid asset archive');
  const cipher = createDecipheriv('aes-256-gcm', key, head.subarray(4));
  cipher.setAuthTag(tag);
  await pipeline(
    createReadStream(file, { start: 16, end: size - 17 }),
    cipher,
    createWriteStream(temp, { flags: 'wx', mode: 0o600 }),
  );
  const { stdout } = await run('tar', ['-tf', temp], {
    maxBuffer: 32 * 1024 * 1024,
  });
  for (const path of stdout.trim().split(/\r?\n/))
    if (
      path !== './' &&
      !/^\.\/(?:[0-9a-f-]{36}|catalog)\/(?:[0-9a-f]{64})?$/.test(path)
    )
      throw Error('Unsafe asset archive entry');
  const verbose = await run('tar', ['-tvf', temp], {
    maxBuffer: 64 * 1024 * 1024,
  });
  if (
    verbose.stdout
      .split('\n')
      .filter(Boolean)
      .some((line) => !['-', 'd'].includes(line[0]))
  )
    throw Error('Archive links are forbidden');
}
export async function restoreFileBackup(file, key, root, dry = true) {
  const temp = '/www/backup/duoyu-sync/.assets-' + randomUUID() + '.tar';
  try {
    await verifyFileBackup(file, key, temp);
    if (!dry) {
      await mkdir(root, { recursive: true, mode: 0o700 });
      await run('tar', [
        '--no-same-owner',
        '--no-same-permissions',
        '-xf',
        temp,
        '-C',
        root,
      ]);
      await run('chown', ['-R', 'duoyu-backend:duoyu-backend', root]);
    }
  } finally {
    await unlink(temp).catch(() => {});
  }
}
