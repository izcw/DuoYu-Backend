// Administrative recovery only. This script never runs from an HTTP endpoint.
import { createDecipheriv, randomUUID } from "node:crypto";
import { open, readFile, unlink, writeFile } from "node:fs/promises";
import { createReadStream, createWriteStream } from "node:fs";
import { execFile, spawn } from "node:child_process";
import { promisify } from "node:util";
import { pipeline } from "node:stream/promises";
import { restoreFileBackup } from "./file-backup.mjs";
import { access } from "node:fs/promises";
const execute = promisify(execFile),
  args = process.argv.slice(2),
  backup = args[args.indexOf("--backup") + 1];
// Native BaoTa Go projects must be stopped through the panel so its watchdog
// does not restart a process during database recovery. Legacy installs retain systemd.
async function backendLifecycle(action) {
  const panel = await access("/www/server/duoyu-backend-go.env")
    .then(() => true)
    .catch(() => false);
  if (!panel)
    return execute("systemctl", [
      action === "status" ? "is-active" : action,
      ...(action === "status" ? ["--quiet"] : []),
      "duoyu-backend",
    ]);
  const code = `import sys;sys.path.insert(0,'class');import public;from projectModel import goModel;m=goModel.main();a=sys.argv[1];n='duoyu_backend';args=public.to_dict_obj({'project_name':n});r=m.get_project_run_state(project_name=n) if a=='status' else getattr(m,a+'_project')(args);sys.exit(0 if (r if a=='status' else m.get_project_run_state(project_name=n)==(a=='start')) else 1)`;
  return execute("/www/server/panel/pyenv/bin/python", ["-c", code, action], {
    cwd: "/www/server/panel",
  });
}
if (process.getuid?.() !== 0) throw Error("Administrator privileges required");
if (
  !backup?.startsWith("/www/backup/duoyu-sync/sync-") ||
  !backup.endsWith(".dump.enc") ||
  backup.includes("..")
)
  throw Error("Specify --backup with a DuoYu encrypted backup");
const dry = args.includes("--dry-run");
if (!dry && args[args.indexOf("--confirm") + 1] !== "duoyu_extensions")
  throw Error("Explicit --confirm duoyu_extensions required");
const temp = "/www/backup/duoyu-sync/.verified-" + randomUUID() + ".dump",
  maintenance = "/www/server/duoyu-sync-maintenance";
let stopped = false,
  restored = false,
  healthy = false,
  wasActive = false;
let backendActive = false;
async function psql(bytes) {
  const child = spawn(
    "runuser",
    [
      "-u",
      "postgres",
      "--",
      "/www/server/pgsql/bin/psql",
      "-v",
      "ON_ERROR_STOP=1",
      "-d",
      "duoyu_extensions",
    ],
    { stdio: ["pipe", "ignore", "ignore"] },
  );
  const done = new Promise((resolve, reject) => {
    child.on("error", reject);
    child.on("exit", (code) =>
      code === 0
        ? resolve()
        : reject(Error("Migration or generation rotation failed")),
    );
  });
  child.stdin.on("error", () => {});
  child.stdin.end(bytes);
  await done;
}
try {
  const file = await open(backup, "r");
  const size = (await file.stat()).size;
  if (size < 33) throw Error("Invalid encrypted backup");
  const header = Buffer.alloc(16),
    tag = Buffer.alloc(16);
  await file.read(header, 0, 16, 0);
  await file.read(tag, 0, 16, size - 16);
  await file.close();
  if (header.subarray(0, 4).toString() !== "DYS1")
    throw Error("Invalid backup format");
  const key = await readFile("/www/server/duoyu-sync-backup.key");
  const decipher = createDecipheriv("aes-256-gcm", key, header.subarray(4));
  decipher.setAuthTag(tag);
  // Complete GCM validation before any plaintext reaches pg_restore.
  await pipeline(
    createReadStream(backup, { start: 16, end: size - 17 }),
    decipher,
    createWriteStream(temp, { flags: "wx", mode: 0o600 }),
  );
  const assetBackup = backup.replace(".dump.enc", ".assets.tar.enc");
  const hasAssets = await access(assetBackup)
    .then(() => true)
    .catch(() => false);
  if (hasAssets)
    await restoreFileBackup(
      assetBackup,
      key,
      process.env.ASSET_ROOT ?? "/www/data/duoyu/users",
      true,
    );
  if (dry) {
    console.log("Backup verified; no database or service changes");
    healthy = true;
  } else {
    await execute("/bin/node", [
      new URL("./backup.mjs", import.meta.url).pathname,
      "--keep-all",
    ]);
    wasActive = await execute("systemctl", [
      "is-active",
      "--quiet",
      "duoyu-sync",
    ])
      .then(() => true)
      .catch(() => false);
    await writeFile(maintenance, "Recovery in progress\n", {
      flag: "wx",
      mode: 0o600,
    });
    await execute("systemctl", ["stop", "duoyu-sync"]);
    backendActive = await backendLifecycle("status")
      .then(() => true)
      .catch(() => false);
    if (backendActive) await backendLifecycle("stop");
    stopped = true;
    const child = spawn(
      "runuser",
      [
        "-u",
        "postgres",
        "--",
        "/www/server/pgsql/bin/pg_restore",
        "--clean",
        "--if-exists",
        "--single-transaction",
        "-d",
        "duoyu_extensions",
      ],
      { stdio: ["pipe", "ignore", "ignore"] },
    );
    const done = new Promise((resolve, reject) => {
      child.on("error", reject);
      child.on("exit", (code) =>
        code === 0
          ? resolve()
          : reject(Error("Restore failed; database transaction rolled back")),
      );
    });
    await Promise.all([pipeline(createReadStream(temp), child.stdin), done]);
    restored = true;
    await psql(await readFile(new URL("../schema.sql", import.meta.url)));
    const backendRoot = await access("/www/server/duoyu-backend-go.env")
      .then(() => "/www/wwwroot/duoyu-backend-go")
      .catch(() => "/www/wwwroot/duoyu-backend");
    const backendSchema = backendRoot + "/schema.sql";
    if (
      await access(backendSchema)
        .then(() => true)
        .catch(() => false)
    )
      await psql(await readFile(backendSchema));
    const backendGrants = backendRoot + "/grants.sql";
    if (
      await access(backendGrants)
        .then(() => true)
        .catch(() => false)
    ) {
      await psql(await readFile(backendGrants));
      await psql("TRUNCATE backend_sessions;");
    }
    if (hasAssets)
      await restoreFileBackup(
        assetBackup,
        key,
        process.env.ASSET_ROOT ?? "/www/data/duoyu/users",
        false,
      );
    await psql(
      "UPDATE sync_generation SET epoch=gen_random_uuid() WHERE singleton; GRANT SELECT ON sync_generation TO duoyu_sync_runtime;",
    );
    healthy = true;
    console.log(
      "Database restored and dataset generation rotated; offline clients will review pending changes",
    );
  }
} finally {
  await unlink(temp).catch(() => {});
  if (stopped && (healthy || !restored)) {
    await unlink(maintenance).catch(() => {});
    if (wasActive) await execute("systemctl", ["start", "duoyu-sync"]);
    if (backendActive) await backendLifecycle("start");
  }
  if (stopped && restored && !healthy)
    console.error(
      "Service remains in maintenance mode; review recovery before restarting",
    );
}
