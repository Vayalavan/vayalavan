# The Node gateway — the ONLY publicly reachable service (CLAUDE.md rule 5).
#
# Built from the REPO ROOT, because npm workspaces put the lockfile there:
#   docker build -f vm-gateway-api/Dockerfile .

# ---- build ----------------------------------------------------------------
FROM node:24-alpine AS build
WORKDIR /src

# Manifests first so a source change does not reinstall the world.
COPY package.json package-lock.json ./
COPY vm-gateway-api/package.json ./vm-gateway-api/
COPY packages/vm-ui-kit/package.json ./packages/vm-ui-kit/
COPY vm-client-ui/package.json ./vm-client-ui/
COPY vm-admin-ui/package.json ./vm-admin-ui/
COPY vm-supplier-ui/package.json ./vm-supplier-ui/

# `npm ci` needs every workspace manifest to match the lockfile, hence the
# copies above. Only the gateway's tree is actually built.
RUN npm ci --workspace vm-gateway-api --include-workspace-root

COPY vm-gateway-api ./vm-gateway-api
RUN npm run build --workspace vm-gateway-api

# Reinstall production-only, so devDependencies (tsx, typescript, types) never
# reach the runtime image.
RUN npm ci --omit=dev --workspace vm-gateway-api --include-workspace-root

# ---- run ------------------------------------------------------------------
FROM node:24-alpine
WORKDIR /app

ENV NODE_ENV=production

COPY --from=build /src/node_modules ./node_modules
COPY --from=build /src/vm-gateway-api/dist ./dist
COPY --from=build /src/vm-gateway-api/package.json ./package.json

# node:alpine already ships an unprivileged `node` user.
USER node

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:${PORT:-8080}/healthz" || exit 1

# node directly, not npm: npm swallows signals, so SIGTERM would never reach
# the server and every deploy would end in a 10-second kill instead of a
# graceful drain.
CMD ["node", "dist/index.js"]
