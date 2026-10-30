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
import { createFileBackup } from "./file-backup.mjs";
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
// Hold a session advisory lock until BOTH dump and file archive are complete.
// Closing stdin also releases it when this process crashes; no sleep-based lock.
async function lockAssetCollection() {
  const holder = spawn(
    "runuser",
    [
      "-u",
      "postgres",
      "--",
      "/www/server/pgsql/bin/psql",
      "-X",
      "-qAt",
      "-v",
      "ON_ERROR_STOP=1",
      "-d",
      "duoyu_extensions",
    ],
    { stdio: ["pipe", "pipe", "ignore"] },
  );
  const ended = new Promise((resolve, reject) => {
    holder.on("error", reject);
    holder.on("exit", (code) =>
      code === 0 ? resolve() : reject(Error("Backup coordination failed")),
    );
  });
  ended.catch(() => {});
  await new Promise((resolve, reject) => {
    let output = "";
    holder.stdout.on("data", (chunk) => {
      output += chunk.toString();
      if (output.includes("DYS_GC_LOCKED")) resolve();
    });
    holder.on("error", reject);
    holder.on("exit", () =>
      reject(Error("Cannot acquire backup coordination lock")),
    );
    holder.stdin.on("error", reject);
    holder.stdin.write(
      "SELECT pg_advisory_lock(52615262);\n\\echo DYS_GC_LOCKED\n",
    );
  });
  return async () => {
    holder.stdin.end();
    await ended;
  };
}
const releaseCollection = await lockAssetCollection();
try {
  await mkdir(dir, { recursive: true, mode: 0o700 });
  const file =
    dir +
    "/sync-" +
    new Date().toISOString().replaceAll(":", "-") +
    ".dump.enc";
  const nonce = randomBytes(12),
    cipher = createCipheriv("aes-256-gcm", key, nonce);
  const output = createWriteStream(file, { flags: "wx", mode: 0o600 });
  output.write(Buffer.concat([Buffer.from("DYS1"), nonce]));
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
    { stdio: ["ignore", "pipe", "pipe"] },
  );
  child.stderr.resume();
  const completed = new Promise((resolve, reject) => {
    child.on("error", reject);
    child.on("exit", (code) =>
      code === 0 ? resolve() : reject(Error("Database dump failed")),
    );
  });
  try {
    await Promise.all([pipeline(child.stdout, cipher, output), completed]);
    await writeFile(file, cipher.getAuthTag(), { flag: "a", mode: 0o600 });
    const assets = file.replace(".dump.enc", ".assets.tar.enc");
    await createFileBackup(
      assets,
      key,
      process.env.ASSET_ROOT ?? "/www/data/duoyu/users",
    );
    if (process.env.BACKUP_READ_GID) {
      for (const path of [file, assets]) {
        await chown(path, 0, Number(process.env.BACKUP_READ_GID));
        await chmod(path, 0o640);
      }
    }
    console.log(file);
    for (const entry of process.argv.includes("--keep-all")
      ? []
      : await readdir(dir))
      if (/^sync-[\dT.Z-]+\.(?:dump|assets.tar)\.enc$/.test(entry)) {
        const path = dir + "/" + entry;
        if ((await stat(path)).mtimeMs < Date.now() - 14 * 86400000)
          await unlink(path);
      }
  } catch (e) {
    child.kill();
    await unlink(file).catch(() => {});
    await unlink(file.replace(".dump.enc", ".assets.tar.enc")).catch(() => {});
    throw e;
  }
} finally {
  await releaseCollection();
}
