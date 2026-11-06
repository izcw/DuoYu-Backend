import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const program = fileURLToPath(new URL("../src/restore.mjs", import.meta.url));
const backup = "/www/backup/duoyu-sync/sync-cli-check.dump.enc";
for (const [name, args, message] of [
  ["rejects a positional confirmation", ["duoyu_extensions", "--backup", backup], /positional|unexpected argument/i],
  ["rejects absent confirmation", ["--backup", backup], /Explicit --confirm/],
  ["rejects nested backup paths", ["--dry-run", "--backup", backup.replace("cli-check", "nested/name")], /Specify --backup/],
]) {
  test(name, { skip: process.getuid?.() !== 0 }, () => {
    const result = spawnSync(process.execPath, [program, ...args], { encoding: "utf8" });
    assert.equal(result.status, 1);
    assert.match(result.stderr, message);
  });
}
