import { spawn } from "node:child_process";
// Hold a session advisory lock until BOTH dump and file archive are complete.
// Closing stdin also releases it when this process crashes; no sleep-based lock.
export async function lockAssetCollection() {
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
