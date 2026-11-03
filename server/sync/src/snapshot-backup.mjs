// Encrypted immutable chunks. Every retained manifest is independently reconstructible.
import {
  createHash,
  createCipheriv,
  createDecipheriv,
  randomBytes,
  randomUUID,
} from "node:crypto";
import {
  mkdir,
  open,
  readFile,
  writeFile,
  rename,
  readdir,
  lstat,
  unlink,
  rm,
} from "node:fs/promises";
import { join, dirname } from "node:path";
const hash = (b) => createHash("sha256").update(b).digest("hex");
const uuid = /^[a-f0-9-]{36}$/;
const objectPattern = /^[a-f0-9-]{36}\/[a-f0-9]{64}\.enc$/;
const filePattern =
  /^(?:database\.dump|users\/(?:[a-f0-9-]{36}|catalog)\/[a-f0-9]{64})$/;
const chunkSize = 4 * 1024 * 1024;
async function durableWrite(path, bytes) {
  const f = await open(path, "wx", 0o640);
  try {
    await f.writeFile(bytes);
    await f.sync();
  } finally {
    await f.close();
  }
}
async function syncDirectory(path) {
  let f;
  try {
    f = await open(path, "r");
    await f.sync();
  } catch (e) {
    if (process.platform !== "win32") throw e;
  } finally {
    await f?.close();
  }
}
export function encryptChunk(bytes, key) {
  const nonce = randomBytes(12),
    c = createCipheriv("aes-256-gcm", key, nonce);
  return Buffer.concat([
    Buffer.from("DYB2"),
    nonce,
    c.update(bytes),
    c.final(),
    c.getAuthTag(),
  ]);
}
export function decryptChunk(bytes, key) {
  if (bytes.length < 32 || bytes.subarray(0, 4).toString() !== "DYB2")
    throw Error("Invalid encrypted chunk");
  const c = createDecipheriv("aes-256-gcm", key, bytes.subarray(4, 16));
  c.setAuthTag(bytes.subarray(-16));
  return Buffer.concat([c.update(bytes.subarray(16, -16)), c.final()]);
}
export async function readRecords(dir) {
  const records = [];
  for (const name of await readdir(dir)) {
    if (!name.endsWith(".record.json") || !uuid.test(name.slice(0, -12)))
      continue;
    if (!(await lstat(join(dir, name))).isFile())
      throw Error("Unsafe backup manifest");
    const record = JSON.parse(await readFile(join(dir, name), "utf8"));
    validateRecord(record);
    if (record.id !== name.slice(0, -12))
      throw Error("Backup manifest ID mismatch");
    records.push(record);
  }
  return records.sort(
    (a, b) =>
      b.created_at.localeCompare(a.created_at) || b.id.localeCompare(a.id),
  );
}
function validateRecord(r) {
  if (
    r.format !== "duoyu-system-backup" ||
    r.version !== 2 ||
    !uuid.test(r.id) ||
    !uuid.test(r.namespace) ||
    !["full", "incremental"].includes(r.kind) ||
    !Number.isFinite(Date.parse(r.created_at)) ||
    !Array.isArray(r.files)
  )
    throw Error("Invalid backup manifest");
  const names = new Set();
  for (const f of r.files) {
    if (
      !filePattern.test(f.path) ||
      names.has(f.path) ||
      !Array.isArray(f.parts) ||
      !Number.isSafeInteger(f.bytes) ||
      f.bytes < 0 ||
      !/^[a-f0-9]{64}$/.test(f.sha256)
    )
      throw Error("Invalid backup entry");
    names.add(f.path);
    for (const p of f.parts)
      if (
        !objectPattern.test(p.object) ||
        !Number.isSafeInteger(p.bytes) ||
        p.bytes < 0 ||
        p.bytes > chunkSize
      )
        throw Error("Invalid backup chunk");
  }
  if (!names.has("database.dump")) throw Error("Missing database dump");
}
async function assetFiles(root) {
  const files = [];
  for (const directory of await readdir(root, { withFileTypes: true })) {
    if (
      !directory.isDirectory() ||
      !(uuid.test(directory.name) || directory.name === "catalog")
    )
      continue;
    for (const entry of await readdir(join(root, directory.name), {
      withFileTypes: true,
    })) {
      if (!entry.isFile() || !/^[a-f0-9]{64}$/.test(entry.name)) continue;
      files.push({
        path: "users/" + directory.name + "/" + entry.name,
        source: join(root, directory.name, entry.name),
      });
    }
  }
  return files.sort((a, b) => a.path.localeCompare(b.path));
}
export async function createSnapshot(
  dir,
  key,
  { dumpPath, assetRoot, mode = "auto", now = new Date(), keep = 7 },
) {
  if (
    !["auto", "full", "incremental"].includes(mode) ||
    !Number.isInteger(keep) ||
    keep < 1
  )
    throw Error("Invalid backup policy");
  await mkdir(dir, { recursive: true, mode: 0o750 });
  const previous = (await readRecords(dir))[0];
  const age = previous
    ? now - new Date(previous.full_at || previous.created_at)
    : Infinity;
  const kind =
    mode === "full" || !previous || (mode === "auto" && age >= 7 * 86400000)
      ? "full"
      : "incremental";
  const id = randomUUID(),
    namespace = kind === "full" ? id : previous.namespace;
  const objectDir = join(dir, "objects", namespace);
  await mkdir(objectDir, { recursive: true, mode: 0o750 });
  const record = {
    format: "duoyu-system-backup",
    version: 2,
    id,
    namespace,
    kind,
    created_at: now.toISOString(),
    full_at:
      kind === "full"
        ? now.toISOString()
        : previous.full_at || previous.created_at,
    bytes: 0,
    added_bytes: 0,
    files: [],
  };
  const inputs = [
    { path: "database.dump", source: dumpPath },
    ...(await assetFiles(assetRoot)),
  ];
  for (const input of inputs) {
    const file = await open(input.source, "r");
    const digest = createHash("sha256");
    const entry = { path: input.path, bytes: 0, sha256: "", parts: [] };
    try {
      for (;;) {
        const buffer = Buffer.alloc(chunkSize);
        const { bytesRead } = await file.read(buffer, 0, chunkSize, null);
        if (!bytesRead) break;
        const bytes = buffer.subarray(0, bytesRead),
          h = hash(bytes),
          object = namespace + "/" + h + ".enc";
        digest.update(bytes);
        entry.bytes += bytesRead;
        const target = join(dir, "objects", object);
        let existing;
        try {
          existing = await readFile(target);
        } catch (e) {
          if (e.code !== "ENOENT") throw e;
        }
        // Verify reused content before committing a new record, including GCM and digest.
        if (existing) {
          if (hash(decryptChunk(existing, key)) !== h)
            throw Error("Existing backup object corrupted");
        } else {
          const temp = target + "." + randomUUID() + ".tmp";
          await durableWrite(temp, encryptChunk(bytes, key));
          await rename(temp, target);
          await syncDirectory(objectDir);
          record.added_bytes += bytesRead;
        }
        entry.parts.push({ object, bytes: bytesRead });
      }
    } finally {
      await file.close();
    }
    entry.sha256 = digest.digest("hex");
    record.bytes += entry.bytes;
    record.files.push(entry);
  }
  validateRecord(record);
  const temp = join(dir, id + ".record.tmp");
  await durableWrite(temp, JSON.stringify(record));
  await rename(temp, join(dir, id + ".record.json"));
  await syncDirectory(dir);
  // Retention only runs after a complete, verified record is visible.
  await pruneSnapshots(dir, keep);
  return record;
}
export async function pruneSnapshots(dir, keep = 7) {
  const records = await readRecords(dir);
  for (const r of records.slice(keep))
    await unlink(join(dir, r.id + ".record.json"));
  const retained = records.slice(0, keep),
    live = new Set(
      retained.flatMap((r) =>
        r.files.flatMap((f) => f.parts.map((p) => p.object)),
      ),
    );
  const objects = join(dir, "objects");
  let namespaces = [];
  try {
    namespaces = await readdir(objects, { withFileTypes: true });
  } catch (e) {
    if (e.code !== "ENOENT") throw e;
  }
  for (const ns of namespaces) {
    if (!ns.isDirectory() || !uuid.test(ns.name)) continue;
    for (const f of await readdir(join(objects, ns.name), {
      withFileTypes: true,
    }))
      if (
        f.isFile() &&
        /^[a-f0-9]{64}\.enc$/.test(f.name) &&
        !live.has(ns.name + "/" + f.name)
      )
        await unlink(join(objects, ns.name, f.name));
  }
  // Legacy database + asset pairs count as a single recovery point during migration.
  const legacy = (await readdir(dir))
    .filter((n) => /^sync-[0-9TZ:.\-]+\.dump\.enc$/.test(n))
    .sort()
    .reverse();
  for (const name of legacy.slice(Math.max(0, keep - retained.length))) {
    await unlink(join(dir, name));
    await unlink(join(dir, name.replace(".dump.enc", ".assets.tar.enc"))).catch(
      (e) => {
        if (e.code !== "ENOENT") throw e;
      },
    );
  }
}
export async function restoreSnapshot(dir, key, id, target) {
  if (!uuid.test(id)) throw Error("Invalid backup ID");
  const record = JSON.parse(
    await readFile(join(dir, id + ".record.json"), "utf8"),
  );
  validateRecord(record);
  if (record.id !== id) throw Error("Backup ID mismatch");
  await mkdir(target, { recursive: false, mode: 0o700 });
  try {
    for (const f of record.files) {
      const dest = join(target, f.path);
      await mkdir(dirname(dest), { recursive: true, mode: 0o700 });
      const out = await open(dest, "wx", 0o600);
      const digest = createHash("sha256");
      let size = 0;
      try {
        for (const p of f.parts) {
          const location = join(dir, "objects", p.object);
          if (
            !(await lstat(location)).isFile() ||
            (await lstat(dirname(location))).isSymbolicLink()
          )
            throw Error("Unsafe backup object");
          const b = decryptChunk(await readFile(location), key);
          if (
            b.length !== p.bytes ||
            hash(b) !== p.object.split("/")[1].slice(0, -4)
          )
            throw Error("Backup chunk mismatch");
          digest.update(b);
          size += b.length;
          await out.writeFile(b);
        }
      } finally {
        await out.close();
      }
      if (size !== f.bytes || digest.digest("hex") !== f.sha256)
        throw Error("Backup file mismatch");
    }
  } catch (e) {
    await rm(target, { recursive: true, force: true });
    throw e;
  }
  return record;
}
