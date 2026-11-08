# syntax=docker/dockerfile:1.7

# --- UI build (runs on the build host arch) ---
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/ ./
RUN if [ -f package.json ]; then \
      if [ -f package-lock.json ]; then npm ci; else npm install; fi && npm run build; \
    else echo "web/package.json missing: building without UI"; mkdir -p dist; fi

# --- Go build (cross-compiles for the target arch) ---
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w \
        -X github.com/xonoko/infra-monitor/internal/buildinfo.Version=${VERSION} \
        -X github.com/xonoko/infra-monitor/internal/buildinfo.Commit=${COMMIT} \
        -X github.com/xonoko/infra-monitor/internal/buildinfo.Date=${DATE}" \
      -o /out/infra-monitor ./cmd/infra-monitor

# --- Runtime ---
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/infra-monitor /infra-monitor
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/infra-monitor"]
CMD ["serve"]
