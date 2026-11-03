import test from "node:test";
import assert from "node:assert/strict";
import {
  mkdtemp,
  mkdir,
  writeFile,
  readFile,
  rm,
  unlink,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { randomBytes } from "node:crypto";
import {
  createSnapshot,
  restoreSnapshot,
  readRecords,
} from "../src/snapshot-backup.mjs";
test("full and incremental snapshots retain seven independently recoverable points", async () => {
  const root = await mkdtemp(join(tmpdir(), "duoyu-snapshots-"));
  try {
    const dir = join(root, "backups"),
      assets = join(root, "users"),
      user = "11111111-1111-4111-8111-111111111111",
      sha = "a".repeat(64),
      key = randomBytes(32),
      dump = join(root, "database.dump");
    await mkdir(join(assets, user), { recursive: true });
    await writeFile(join(assets, user, sha), "image-original");
    const database = randomBytes(4 * 1024 * 1024 + 1000);
    await writeFile(dump, database);
    const first = await createSnapshot(dir, key, {
      dumpPath: dump,
      assetRoot: assets,
      mode: "auto",
      now: new Date("2026-11-01T00:00:00Z"),
    });
    assert.equal(first.kind, "full");
    let newest;
    for (let i = 1; i <= 8; i++) {
      database[database.length - 1] = i;
      await writeFile(dump, database);
      newest = await createSnapshot(dir, key, {
        dumpPath: dump,
        assetRoot: assets,
        mode: "incremental",
        now: new Date(1761955200000 + i * 1000 + 365 * 86400000),
      });
      assert.equal(newest.kind, "incremental");
      assert(newest.added_bytes < newest.bytes);
    }
    assert.equal((await readRecords(dir)).length, 7);
    assert(!(await readRecords(dir)).some((r) => r.id === first.id));
    const target = join(root, "restored");
    await restoreSnapshot(dir, key, newest.id, target);
    assert.deepEqual(await readFile(join(target, "database.dump")), database);
    assert.equal(
      await readFile(join(target, "users", user, sha), "utf8"),
      "image-original",
    );
    const object = newest.files[0].parts[0].object;
    const encrypted = await readFile(join(dir, "objects", object));
    encrypted[20] ^= 1;
    await writeFile(join(dir, "objects", object), encrypted);
    await assert.rejects(
      restoreSnapshot(dir, key, newest.id, join(root, "corrupt")),
    );
    await assert.rejects(
      createSnapshot(dir, key, {
        dumpPath: dump,
        assetRoot: assets,
        mode: "incremental",
      }),
    );
    assert.equal((await readRecords(dir)).length, 7);
    await assert.rejects(
      restoreSnapshot(dir, key, "../private", join(root, "unsafe")),
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
