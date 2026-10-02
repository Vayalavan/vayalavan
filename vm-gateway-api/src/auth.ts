/**
 * JWT verification and role guards.
 *
 * The gateway is the only place a customer-supplied token is trusted. Once
 * verified here, the identity travels to internal services as X-User-Id and
 * X-User-Role headers, which those services trust ONLY because they also
 * require INTERNAL_SERVICE_TOKEN (CLAUDE.md rule 5).
 */
import { jwtVerify, type JWTPayload } from "jose";
import type { Request, Response, NextFunction, RequestHandler } from "express";
import type { Config } from "./config.js";
import { AppError } from "./errors.js";

/** Must match the values vm-profile-api mints with. */
const ISSUER = "vm-profile-api";
const AUDIENCE = "vayal-mikrogreenz";

export const Role = {
  Customer: "customer",
  Supplier: "supplier",
  Admin: "admin",
  // Reads analytics and nothing else (CLAUDE.md §7). No admin route admits
  // it: every one of them names Role.Admin alone.
  Analyst: "analyst",
} as const;

export type RoleValue = (typeof Role)[keyof typeof Role];

export interface Identity {
  userId: string;
  role: RoleValue;
  status: string;
  /** Present only for supplier accounts. */
  supplierId?: string;
}

declare global {
  // eslint-disable-next-line @typescript-eslint/no-namespace
  namespace Express {
    interface Request {
      /** Set by requireAuth / optionalAuth once a token is verified. */
      identity?: Identity;
    }
  }
}

interface VayalClaims extends JWTPayload {
  role?: string;
  status?: string;
  supplier_id?: string;
}

function isRole(value: unknown): value is RoleValue {
  return (
    value === Role.Customer ||
    value === Role.Supplier ||
    value === Role.Admin ||
    value === Role.Analyst
  );
}

/** Extracts a bearer token, or null. */
function bearerToken(req: Request): string | null {
  const header = req.header("authorization");
  if (!header) return null;

  const [scheme, token] = header.split(" ");
  // Case-insensitive scheme per RFC 7235.
  if (!scheme || scheme.toLowerCase() !== "bearer" || !token) return null;
  return token.trim() || null;
}

/**
 * Verifies an access token and returns the identity it asserts.
 *
 * The algorithm is pinned to HS256. Without pinning, a token declaring
 * `alg: none` would be accepted as valid — the classic JWT algorithm
 * confusion attack. Issuer and audience are checked so a token minted by some
 * other system, or for another environment, is rejected.
 */
export async function verifyAccessToken(
  token: string,
  secret: Uint8Array,
): Promise<Identity> {
  let payload: VayalClaims;
  try {
    const result = await jwtVerify<VayalClaims>(token, secret, {
      algorithms: ["HS256"],
      issuer: ISSUER,
      audience: AUDIENCE,
    });
    payload = result.payload;
  } catch {
    // Expired, wrong signature, wrong issuer — all one message. Telling the
    // client which would help an attacker tune their forgery.
    throw AppError.unauthorized("Your session is invalid or has expired.");
  }

  if (!payload.sub || !isRole(payload.role)) {
    throw AppError.unauthorized("Your session is invalid or has expired.");
  }

  // A token minted before an account was suspended is still cryptographically
  // valid. Rejecting on the embedded status closes that window to the access
  // token's 15-minute TTL.
  if (payload.status && payload.status !== "active") {
    throw AppError.forbidden("This account is not active.");
  }

  return {
    userId: payload.sub,
    role: payload.role,
    status: payload.status ?? "active",
    ...(payload.supplier_id ? { supplierId: payload.supplier_id } : {}),
  };
}

/** Rejects the request unless it carries a valid access token. */
export function requireAuth(config: Config): RequestHandler {
  const secret = new TextEncoder().encode(config.JWT_SECRET);

  return (req: Request, _res: Response, next: NextFunction) => {
    const token = bearerToken(req);
    if (!token) {
      next(AppError.unauthorized("Authentication is required."));
      return;
    }
    verifyAccessToken(token, secret)
      .then((identity) => {
        req.identity = identity;
        next();
      })
      .catch(next);
  };
}

/**
 * Admits only the listed roles. Must run after requireAuth.
 *
 * The internal services enforce roles again (CLAUDE.md §7). This is the first
 * gate, not the only one — a mistake here should not be sufficient to reach
 * an admin endpoint.
 */
export function requireRole(...roles: RoleValue[]): RequestHandler {
  const allowed = new Set<string>(roles);

  return (req: Request, _res: Response, next: NextFunction) => {
    if (!req.identity) {
      next(AppError.unauthorized("Authentication is required."));
      return;
    }
    if (!allowed.has(req.identity.role)) {
      req.log.warn(
        {
          user_id: req.identity.userId,
          role: req.identity.role,
          required: roles,
          path: req.originalUrl,
        },
        "role check rejected request",
      );
      next(AppError.forbidden("You do not have access to this resource."));
      return;
    }
    next();
  };
}
