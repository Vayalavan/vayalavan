/**
 * vm-gateway-api entrypoint.
 *
 * Loads configuration, starts the HTTP server, and drains gracefully on
 * SIGTERM/SIGINT so a deploy cannot abort an in-flight checkout.
 */
import { createApp } from "./app.js";
import { loadConfig, loadDotEnv, SERVICE_NAME } from "./config.js";
import { createLogger } from "./logger.js";

function main(): void {
  // Local development convenience. Absent in deployed environments, where
  // real env vars are set by the platform.
  loadDotEnv();

  let config;
  try {
    config = loadConfig();
  } catch (err) {
    // No logger yet — configuration is what builds it. Plain stderr.
    process.stderr.write(`${SERVICE_NAME}: ${(err as Error).message}\n`);
    process.exit(1);
  }

  const logger = createLogger(config.LOG_LEVEL);
  const app = createApp(config, logger);

  const server = app.listen(config.GATEWAY_PORT, () => {
    logger.info(
      { addr: `:${config.GATEWAY_PORT}`, env: config.APP_ENV },
      "http server listening",
    );
  });

  server.on("error", (err) => {
    // Almost always EADDRINUSE. Fatal: there is no service without a socket.
    logger.error({ err }, "http server failed to start");
    process.exit(1);
  });

  let shuttingDown = false;

  const shutdown = (signal: string): void => {
    // A second SIGTERM during the drain should not restart the timer.
    if (shuttingDown) return;
    shuttingDown = true;

    logger.info(
      { signal, timeout_ms: config.SHUTDOWN_TIMEOUT },
      "shutdown signal received, draining connections",
    );

    // Stop accepting new connections; the callback fires once in-flight
    // requests finish.
    server.close((err) => {
      if (err) {
        logger.error({ err }, "error during shutdown");
        process.exit(1);
      }
      logger.info("shutdown complete");
      process.exit(0);
    });

    // Backstop: a wedged keep-alive connection must not block the deploy
    // forever. unref so this timer alone never keeps the process alive.
    setTimeout(() => {
      logger.error("graceful shutdown timed out; forcing exit");
      process.exit(1);
    }, config.SHUTDOWN_TIMEOUT).unref();
  };

  process.on("SIGTERM", () => shutdown("SIGTERM"));
  process.on("SIGINT", () => shutdown("SIGINT"));

  // An unhandled rejection leaves the process in an unknown state. Log it
  // loudly and let the orchestrator restart rather than serving from a
  // corrupted one.
  process.on("unhandledRejection", (reason) => {
    logger.error({ err: reason }, "unhandled promise rejection");
    process.exit(1);
  });
}

main();
