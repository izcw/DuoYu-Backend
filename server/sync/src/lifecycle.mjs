// Share one shutdown across signals and keep storage available to accepted requests.
export function createShutdown(server, pool, graceMs = 30000) {
  let closing;
  return () =>
    (closing ??= (async () => {
      let forced = false;
      // Connections finishing during drain must not wait out the normal idle timeout.
      server.keepAliveTimeout = 1;
      const deadline = setTimeout(() => {
        forced = true;
        server.closeAllConnections();
      }, graceMs);
      deadline.unref();
      try {
        await new Promise((resolve, reject) => {
          server.close((error) => {
            if (error && error.code !== 'ERR_SERVER_NOT_RUNNING') reject(error);
            else resolve();
          });
        });
      } finally {
        clearTimeout(deadline);
      }
      await pool.end();
      if (forced) throw new Error('HTTP shutdown grace period exceeded');
    })());
}
