/**
 * The platform's single error shape, mirroring vm-go-common/httpx.
 *
 * CLAUDE.md rule 8 fixes one wire shape for every service:
 *
 *   {"error": {"code": "SNAKE_CASE_CODE", "message": "...", "details": {}}}
 *
 * The gateway must produce byte-identical envelopes to the Go services, or
 * the three UIs need two error handlers instead of one.
 */
import type { Request, Response, NextFunction } from "express";
import type { Logger } from "./logger.js";

export const ErrorCode = {
  BadRequest: "BAD_REQUEST",
  Validation: "VALIDATION_FAILED",
  Unauthorized: "UNAUTHORIZED",
  Forbidden: "FORBIDDEN",
  NotFound: "NOT_FOUND",
  Conflict: "CONFLICT",
  TooManyRequests: "TOO_MANY_REQUESTS",
  Internal: "INTERNAL_ERROR",
  Unavailable: "SERVICE_UNAVAILABLE",
} as const;

export type ErrorCodeValue = (typeof ErrorCode)[keyof typeof ErrorCode];

/** An error carrying an HTTP status and a stable client-facing code. */
export class AppError extends Error {
  readonly status: number;
  readonly code: ErrorCodeValue;
  readonly details: Record<string, unknown> | undefined;
  /**
   * Logged, never serialised — it can carry internal hostnames or SQL.
   * `override` because ES2022's Error already declares `cause`.
   */
  override readonly cause: unknown;

  constructor(
    status: number,
    code: ErrorCodeValue,
    message: string,
    options: { details?: Record<string, unknown>; cause?: unknown } = {},
  ) {
    super(message);
    this.name = "AppError";
    this.status = status;
    this.code = code;
    this.details = options.details;
    this.cause = options.cause;
  }

  static badRequest(message: string, details?: Record<string, unknown>) {
    return new AppError(400, ErrorCode.BadRequest, message, { ...(details && { details }) });
  }

  static unauthorized(message = "Authentication is required.") {
    return new AppError(401, ErrorCode.Unauthorized, message);
  }

  static forbidden(message = "You do not have access to this resource.") {
    return new AppError(403, ErrorCode.Forbidden, message);
  }

  static notFound(message = "The requested resource was not found.") {
    return new AppError(404, ErrorCode.NotFound, message);
  }

  static tooManyRequests(message = "Too many requests. Please slow down.") {
    return new AppError(429, ErrorCode.TooManyRequests, message);
  }

  static unavailable(message = "A dependency is currently unavailable.") {
    return new AppError(503, ErrorCode.Unavailable, message);
  }

  static internal(cause: unknown) {
    return new AppError(
      500,
      ErrorCode.Internal,
      "Something went wrong on our end. Please try again.",
      { cause },
    );
  }
}

interface ErrorBody {
  error: {
    code: string;
    message: string;
    details?: Record<string, unknown>;
  };
}

function toBody(err: AppError): ErrorBody {
  return {
    error: {
      code: err.code,
      message: err.message,
      // Omit rather than serialise as null, so clients can rely on `details`
      // being an object whenever the key is present.
      ...(err.details && { details: err.details }),
    },
  };
}

/** 404 handler for unmatched routes. Mounted after all real routes. */
export function notFoundHandler(_req: Request, _res: Response, next: NextFunction): void {
  next(AppError.notFound("No route matches this path."));
}

/**
 * Terminal error middleware. Renders any thrown value in the standard shape.
 *
 * Unrecognised errors become a generic 500 with the original logged rather
 * than returned, so a stray stack trace or connection string cannot leak to
 * a customer.
 */
export function errorHandler(logger: Logger) {
  return (err: unknown, req: Request, res: Response, next: NextFunction): void => {
    // Express requires the 4-arg signature to recognise this as an error
    // handler; delegate if the response has already started streaming.
    if (res.headersSent) {
      next(err);
      return;
    }

    const appErr = err instanceof AppError ? err : AppError.internal(err);
    const log = req.log ?? logger;

    const payload = {
      error_code: appErr.code,
      status: appErr.status,
      method: req.method,
      path: req.originalUrl,
    };

    // 5xx is our bug and needs attention; 4xx is the client's and is
    // expected traffic. Logging both at error level makes the signal useless.
    //
    // The stack trace rides along only on 5xx: a 404 is routine, and
    // attaching ten frames to every one of them buries real failures under
    // noise that is expensive to store and useless to read.
    if (appErr.status >= 500) {
      log.error({ ...payload, err: appErr.cause ?? appErr }, "request failed");
    } else {
      log.info(payload, "request rejected");
    }

    res.status(appErr.status).json(toBody(appErr));
  };
}
