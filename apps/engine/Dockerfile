# syntax=docker/dockerfile:1
# The Lumbung engine image: lumbungd and the lumbung CLI on a distroless
# base, running as a non-root user with its data in /data.
#
#   docker build -t lumbung-engine apps/engine
#
# The build context is apps/engine: the Go module is self-contained, so a
# change to the dashboard or the docs never rebuilds this image.

FROM golang:1.27.4-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
# No cgo: the SQLite driver is pure Go, so the binaries are static.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/zakiego/lumbung/apps/engine/internal/version.Version=${VERSION}" \
      -o /out/ ./cmd/lumbungd ./cmd/lumbung \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/lumbungd /out/lumbung /usr/local/bin/
# An empty /data owned by the nonroot user, so a fresh named volume starts
# writable.
COPY --from=build --chown=65532:65532 /out/data /data
ENV LUMBUNG_DATA_DIR=/data \
    LUMBUNG_ADDR=0.0.0.0:4870
VOLUME ["/data"]
EXPOSE 4870
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["lumbungd", "healthcheck"]
ENTRYPOINT ["lumbungd"]
