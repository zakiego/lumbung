# syntax=docker/dockerfile:1.7-labs
# The dashboard: TanStack Start on Nitro, built from the repo root so the
# workspace lockfile and @lumbung/client come along. Only the dashboard and
# the packages it uses are installed; the engine has its own image.
#
#   docker build -f apps/dashboard/Dockerfile .
FROM node:26-bookworm-slim AS build
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
# The runtime image has no shell, so /data is made here and copied over.
RUN mkdir /empty-data

# Distroless: node and its runtime libraries, no shell or package manager. The
# server has no native modules (node:sqlite is built in), so nothing else is
# needed. Debugging uses `docker compose exec dashboard node`, and file work
# on the volume uses a separate image with a shell (ops/restore-drill.sh).
FROM gcr.io/distroless/nodejs24-debian13:nonroot
WORKDIR /app
# Distroless keeps node at /nodejs/bin/node, off the PATH, and makes it the
# ENTRYPOINT. Putting it on the PATH and clearing the ENTRYPOINT keeps
# `CMD ["node", ...]`, `docker compose exec dashboard node` (ops/backup.sh)
# and `docker run --entrypoint node` (ops/lib.sh, scripts/deploy-up.sh)
# working as they did on the Debian node image.
#
# Runs as the engine image's user (65532), so it can read the engine's
# generated service token from a shared, read-only volume.
ENV PATH=/nodejs/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  NODE_ENV=production PORT=4871 HOST=0.0.0.0 LUMBUNG_DASHBOARD_DATA_DIR=/data
COPY --from=build --chown=65532:65532 /empty-data /data
COPY --from=build --chown=65532:65532 /repo/apps/dashboard/.output ./.output
USER 65532:65532
EXPOSE 4871
VOLUME ["/data"]
# Probe every second while starting (Docker's default is 5 s). A container
# started on demand by a proxy that waits for healthy then answers its first
# request about 4 s sooner, and a deploy's --wait finishes sooner too.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --start-interval=1s --retries=3 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:4871/app/api/health').then(r=>process.exit(r.ok?0:1),()=>process.exit(1))"]
ENTRYPOINT []
CMD ["node", ".output/server/index.mjs"]
