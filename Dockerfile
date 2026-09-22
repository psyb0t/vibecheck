# Production Dockerfile - Multi-stage build
FROM golang:1.26.6-alpine@sha256:af8d6740070b8906d12eae1c3e3ea0957fb63f492051ea05e354c38ef9fe88df AS builder

ARG BUILD_COMMIT=""
ARG BUILD_VERSION="dev"

# Coverage instrumentation for the API test suite, empty for a real build.
# The suite passes "-cover -coverpkg=<module>/..." here so the containerised
# service writes covdata to GOCOVERDIR and its lines count toward the gate.
# A production image is built with this unset and is not instrumented.
ARG BUILD_COVER=""

# Install build dependencies
RUN apk add --no-cache \
    gcc \
    musl-dev

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build binary with static linking.
#
# The app name is derived from go.mod's module path and injected into
# main.appName, which cmd/main.go uses as cobra's Use:/Short:. Without it the
# binary falls back to the literal "servicepack" and introduces itself by the
# framework's name in its own --help.
RUN APP_NAME="$(head -n 1 go.mod | awk '{print $2}' | awk -F'/' '{print $NF}')" && \
    CGO_ENABLED=0 go build -a -mod=vendor ${BUILD_COVER} \
    -ldflags "-X main.appName=${APP_NAME} -X main.buildCommit=${BUILD_COMMIT} -X main.buildVersion=${BUILD_VERSION}" \
    -o ./build/app ./cmd

# Final stage - minimal runtime image
# Pinned by digest, not by tag: a tag is mutable, so `alpine:latest` lets an
# upstream republish different bytes under the same name and your next build
# silently picks them up. Bump this deliberately.
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

# Create the non-root user at a fixed UID/GID. A fixed number, rather than
# whatever adduser picks next, is what makes a bind-mounted host directory's
# ownership predictable across rebuilds.
RUN addgroup -g 10001 -S appuser && \
    adduser -u 10001 -S -G appuser -s /bin/sh appuser

# The two paths the service touches outside its own binary.
#
# /data is the only writable location: the SQLite database and its WAL live
# here, so a read_only root filesystem still works as long as this is a
# volume. /config/policies is where policy documents are mounted read-only,
# and it is created empty so a deployment with no policies still starts.
RUN mkdir -p /data /config/policies && \
    chown -R appuser:appuser /data /config/policies

# Coverage drop point. It stays empty in a production image; the API suite
# bind-mounts over it and sets GOCOVERDIR so the instrumented binary can
# write counters as the non-root user.
RUN mkdir -p /covdata && chown appuser:appuser /covdata

WORKDIR /app

# --chown at COPY time, so the binary never exists as root-owned in a layer.
COPY --from=builder --chown=appuser:appuser /app/build/app .

USER appuser

VOLUME ["/data"]

EXPOSE 8080 9091

# Readiness over liveness: /ready reports that the database answers, which is
# the condition under which this container can actually serve. wget comes
# from busybox and is already in the image, so the check adds no packages.
HEALTHCHECK --interval=10s --timeout=3s --retries=5 --start-period=30s \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/ready || exit 1

# Exec form: SIGTERM reaches the binary directly, so the graceful drain runs
# instead of docker stop waiting out its timeout.
ENTRYPOINT ["./app"]

# Default command if no args provided
CMD ["--help"]
