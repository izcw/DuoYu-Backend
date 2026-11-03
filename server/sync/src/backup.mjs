import { lockAssetCollection } from "./backup-lock.mjs";
import { createCipheriv, createDecipheriv, randomBytes } from "node:crypto";
import { spawn } from "node:child_process";
import {
  readFile,
  writeFile,
  mkdir,
  readdir,
  stat,
  unlink,
} from "node:fs/promises";
import { createWriteStream } from "node:fs";
import { pipeline } from "node:stream/promises";
import { createSnapshot } from "./snapshot-backup.mjs";
import { chmod, chown } from "node:fs/promises";
const dir = "/www/backup/duoyu-sync",
  keyPath = "/www/server/duoyu-sync-backup.key";
const key = await readFile(keyPath);
if (key.length !== 32) throw Error("Invalid backup key");
if (process.argv[2] === "--verify") {
  const file = process.argv[3];
  if (!file?.startsWith(dir + "/sync-") || !file.endsWith(".enc"))
    throw Error("Invalid backup path");
  const bytes = await readFile(file);
  if (bytes.subarray(0, 4).toString() !== "DYS1") throw Error("Invalid backup");
  const cipher = createDecipheriv("aes-256-gcm", key, bytes.subarray(4, 16));
  cipher.setAuthTag(bytes.subarray(-16));
  cipher.update(bytes.subarray(16, -16));
  cipher.final();
  console.log("Encrypted backup integrity verified");
  process.exit(0);
}
const releaseCollection = await lockAssetCollection();
let temp;
try {
  await mkdir(dir, { recursive: true, mode: 0o750 });
  temp = dir + "/.dump-" + randomBytes(12).toString("hex");
  const child = spawn(
    "runuser",
    [
      "-u",
      "postgres",
      "--",
      "/www/server/pgsql/bin/pg_dump",
      "-Fc",
      "duoyu_extensions",
    ],
    { stdio: ["ignore", "pipe", "ignore"] },
  );
  const done = new Promise((resolve, reject) => {
    child.on("error", reject);
    child.on("exit", (code) =>
      code === 0 ? resolve() : reject(Error("Database dump failed")),
    );
  });
  try {
    await Promise.all([
      pipeline(
        child.stdout,
        createWriteStream(temp, { flags: "wx", mode: 0o600 }),
      ),
      done,
    ]);
  } catch (e) {
    child.kill();
    throw e;
  }
  const record = await createSnapshot(dir, key, {
    dumpPath: temp,
    assetRoot: process.env.ASSET_ROOT ?? "/www/data/duoyu/users",
    mode: process.env.BACKUP_MODE ?? "auto",
    keep: process.argv.includes("--keep-all") ? Number.MAX_SAFE_INTEGER : 7,
  });
  console.log(
    JSON.stringify({
      id: record.id,
      kind: record.kind,
      bytes: record.bytes,
      added_bytes: record.added_bytes,
    }),
  );
} finally {
  if (temp) await unlink(temp).catch(() => {});
  await releaseCollection();
}
