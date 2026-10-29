# One builder stage for the single Go module (go/), one small runtime target per binary:
#   worker, bulkwire-service, codec-server, control-service.
# Build context is the repository root. CGO is off, so the binaries are static.
#
# DEMO_BUG (build arg, empty by default) is stamped into internal/buildflags.DemoBug with -ldflags.
# DEMO-ONLY: only the worker-v2 image of the versioning demo sets it (see docs/RUNBOOK-DEMO.md, scenario F).

FROM golang:1.27.3-alpine3.24 AS build
WORKDIR /src
COPY go/go.mod go/go.sum ./
RUN go mod download
COPY go/ ./
ARG DEMO_BUG=""
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X bulkwire/internal/buildflags.DemoBug=${DEMO_BUG}" \
      -o /out/ ./cmd/worker ./cmd/bulkwire-service ./cmd/codec-server ./cmd/control-service ./cmd/samplegen

# Alpine (not scratch/distroless) so container healthchecks can use busybox wget.
FROM alpine:3.24 AS base
RUN addgroup -S -g 10001 app && adduser -S -u 10001 -G app app && apk add --no-cache ca-certificates tzdata
USER app

FROM base AS worker
COPY --from=build /out/worker /usr/local/bin/worker
EXPOSE 9464
ENTRYPOINT ["/usr/local/bin/worker"]

FROM base AS bulkwire-service
COPY --from=build /out/bulkwire-service /usr/local/bin/bulkwire-service
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/bulkwire-service"]

FROM base AS codec-server
COPY --from=build /out/codec-server /usr/local/bin/codec-server
EXPOSE 8089
ENTRYPOINT ["/usr/local/bin/codec-server"]

# DEMO-ONLY service. It talks to the Docker Engine API over /var/run/docker.sock, so its user must be in
# the socket's group: docker-compose.yml adds that group with `group_add`.
FROM base AS control-service
COPY --from=build /out/control-service /usr/local/bin/control-service
EXPOSE 8095
ENTRYPOINT ["/usr/local/bin/control-service"]
