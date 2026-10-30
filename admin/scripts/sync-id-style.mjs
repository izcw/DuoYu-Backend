import { readFile, writeFile, mkdir, copyFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const admin = fileURLToPath(new URL('..', import.meta.url));
const source =
  process.env.DUOYU_ID_DIR ?? path.resolve(admin, '../../DuoYu-ID');
const css = await readFile(path.join(source, 'admin/src/duoyu.css'));
const target = path.join(admin, 'src/vendor/duoyu-id');
await mkdir(target, { recursive: true });
await writeFile(path.join(target, 'duoyu.css'), css);
await copyFile(
  path.join(source, 'admin/public/avatar-default.svg'),
  path.join(admin, 'public/avatar-default.svg'),
);
await writeFile(
  path.join(target, 'source.json'),
  JSON.stringify(
    {
      project: 'DuoYu-ID',
      file: 'admin/src/duoyu.css',
      sha256: createHash('sha256').update(css).digest('hex'),
    },
    null,
    2,
  ) + '\n',
);
console.log('Synced original DuoYu-ID shared stylesheet and default avatar');
