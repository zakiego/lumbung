# syntax=docker/dockerfile:1.7-labs
# The dashboard: TanStack Start on Nitro, built from the repo root so the
# workspace lockfile and @lumbung/client come along. Only the dashboard and
# the packages it uses are installed; the engine has its own image.
#
#   docker build -f apps/dashboard/Dockerfile .
FROM node:24-bookworm-slim AS build
ENV PNPM_HOME=/pnpm PATH=/pnpm:$PATH COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable
WORKDIR /repo
# Manifests first, so the install layer stays cached until dependencies change.
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml .npmrc* ./
COPY --parents apps/*/package.json packages/*/package.json examples/*/package.json ./
RUN --mount=type=cache,id=pnpm,target=/pnpm/store \
  pnpm install --frozen-lockfile --filter @lumbung/dashboard...
COPY tsconfig.base.json ./
COPY packages/client ./packages/client
COPY apps/dashboard ./apps/dashboard
RUN pnpm --filter @lumbung/client build && pnpm --filter @lumbung/dashboard build

FROM node:24-bookworm-slim
WORKDIR /app
# Runs as the engine image's user (65532), so it can read the engine's
# generated service token from a shared, read-only volume.
ENV NODE_ENV=production PORT=4871 HOST=0.0.0.0 LUMBUNG_DASHBOARD_DATA_DIR=/data
RUN mkdir -p /data && chown 65532:65532 /data
COPY --from=build --chown=65532:65532 /repo/apps/dashboard/.output ./.output
USER 65532:65532
EXPOSE 4871
VOLUME ["/data"]
# Probe every second while starting (Docker's default is 5 s). A container
# started on demand by a proxy that waits for healthy then answers its first
# request about 4 s sooner, and a deploy's --wait finishes sooner too.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --start-interval=1s --retries=3 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:4871/app/api/health').then(r=>process.exit(r.ok?0:1),()=>process.exit(1))"]
CMD ["node", ".output/server/index.mjs"]
