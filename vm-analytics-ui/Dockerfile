# Build one React UI and serve it as static files.
#
# Shared by all four UIs via a build ARG. Built from the REPO ROOT:
#   docker build -f vm-client-ui/Dockerfile --build-arg APP=vm-client-ui \
#       --build-arg VITE_API_BASE_URL=https://api.example.com/api .
#
# VITE_* values are baked in at BUILD time — Vite substitutes them into the
# bundle, so they cannot be changed by restarting the container. A different
# environment needs a different build. They are public by definition: never
# pass a secret as a VITE_ variable.

# ---- build ----------------------------------------------------------------
FROM node:24-alpine AS build
WORKDIR /src

ARG APP
RUN test -n "$APP" || (echo "APP build-arg is required" && exit 1)

ARG VITE_API_BASE_URL
ARG VITE_SUPPORT_EMAIL=vayal.mikrogreenz@gmail.com
ARG VITE_APP_NAME="Vayalavan"
ARG VITE_RAZORPAY_KEY_ID=
# vm-admin-ui: where the Analytics remote's remoteEntry.js lives.
ARG VITE_ANALYTICS_REMOTE_URL=
# vm-analytics-ui: its own public origin, which its chunks are addressed from.
ARG ANALYTICS_UI_PUBLIC_URL=
ENV VITE_API_BASE_URL=$VITE_API_BASE_URL \
    VITE_SUPPORT_EMAIL=$VITE_SUPPORT_EMAIL \
    VITE_APP_NAME=$VITE_APP_NAME \
    VITE_RAZORPAY_KEY_ID=$VITE_RAZORPAY_KEY_ID \
    VITE_ANALYTICS_REMOTE_URL=$VITE_ANALYTICS_REMOTE_URL \
    ANALYTICS_UI_PUBLIC_URL=$ANALYTICS_UI_PUBLIC_URL

COPY package.json package-lock.json ./
COPY packages/vm-ui-kit/package.json ./packages/vm-ui-kit/
COPY vm-gateway-api/package.json ./vm-gateway-api/
COPY vm-client-ui/package.json ./vm-client-ui/
COPY vm-admin-ui/package.json ./vm-admin-ui/
COPY vm-analytics-ui/package.json ./vm-analytics-ui/
COPY vm-supplier-ui/package.json ./vm-supplier-ui/
RUN npm ci

# The shared kit is a workspace dependency and must be built first — the apps
# import its dist/, not its source.
COPY packages/vm-ui-kit ./packages/vm-ui-kit
RUN npm run build --workspace @vayal/ui-kit

COPY ${APP} ./${APP}
RUN npm run build --workspace ${APP}

# ---- run ------------------------------------------------------------------
FROM nginx:1.27-alpine
ARG APP
# analytics.conf for vm-analytics-ui, which is fetched cross-origin.
ARG NGINX_CONF=ui.conf

COPY --from=build /src/${APP}/dist /usr/share/nginx/html
COPY infra/nginx/${NGINX_CONF} /etc/nginx/conf.d/default.conf

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1/ >/dev/null || exit 1
