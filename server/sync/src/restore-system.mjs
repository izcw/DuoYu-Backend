// Recovery stays an explicit, root-only operational action, never an HTTP restore.
import { open, readFile, mkdir, rm, unlink, writeFile } from "node:fs/promises";
import { randomBytes, randomUUID, createCipheriv } from "node:crypto";
import { createReadStream } from "node:fs";
import { pipeline } from "node:stream/promises";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { restoreSnapshot } from "./snapshot-backup.mjs";
import { createFileBackup } from "./file-backup.mjs";
import { lockAssetCollection } from "./backup-lock.mjs";
const run = promisify(execFile),
  args = process.argv.slice(2),
  id = args[args.indexOf("--id") + 1];
if (process.getuid?.() !== 0) throw Error("Administrator privileges required");
if (!args.includes("--id") || !/^[a-f0-9-]{36}$/.test(id))
  throw Error("Specify --id with a system backup ID");
const dry = args.includes("--dry-run");
if (
  !dry &&
  (!args.includes("--confirm") ||
    args[args.indexOf("--confirm") + 1] !== "duoyu_extensions")
)
  throw Error("Explicit --confirm duoyu_extensions required");
const dir = "/www/backup/duoyu-sync",
  key = await readFile("/www/server/duoyu-sync-backup.key");
const stage = dir + "/.restore-" + randomUUID(),
  release = await lockAssetCollection();
const file = dir + "/sync-restore-" + randomUUID() + ".dump.enc";
let stageOwned = false, fileOwned = false, assetsOwned = false;
try {
  await restoreSnapshot(dir, key, id, stage);
  stageOwned = true;
  await run("/www/server/pgsql/bin/pg_restore", [
    "--list",
    stage + "/database.dump",
  ]);
  const nonce = randomBytes(12),
    cipher = createCipheriv("aes-256-gcm", key, nonce);
  const destination = await open(file, "wx", 0o600);
  fileOwned = true;
  const out = destination.createWriteStream();
  out.write(Buffer.concat([Buffer.from("DYS1"), nonce]));
  await pipeline(createReadStream(stage + "/database.dump"), cipher, out);
  await writeFile(file, cipher.getAuthTag(), { flag: "a" });
  await mkdir(stage + "/users", { recursive: true, mode: 0o700 });
  await createFileBackup(
    file.replace(".dump.enc", ".assets.tar.enc"),
    key,
    stage + "/users",
  );
  assetsOwned = true;
  // Release before the existing recovery program starts maintenance and rotates the epoch.
  await release();
  const result = await run(
    process.execPath,
    [
      new URL("./restore.mjs", import.meta.url).pathname,
      "--backup",
      file,
      ...(dry ? ["--dry-run"] : ["--confirm", "duoyu_extensions"]),
    ],
    { maxBuffer: 4 * 1024 * 1024 },
  );
  process.stdout.write(result.stdout);
} finally {
  await release().catch(() => {});
  if (stageOwned) await rm(stage, { recursive: true, force: true });
  if (fileOwned) await unlink(file);
  if (assetsOwned) await unlink(file.replace(".dump.enc", ".assets.tar.enc"));
}
