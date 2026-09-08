# The image build compiles only the Go binary. The frontend (web/dist) is built
# beforehand on the developer machine or the CI runner (scripts/package-src.sh,
# .github/workflows/docker.yml): running pnpm inside `docker build` proved fragile on
# some hosts' storage drivers (SQLite store "disk I/O error", EPERM on hard links).
#
#   cd web && pnpm install && pnpm build && cd ..      # or: ./scripts/package-src.sh
#   docker build -t yzapi/gateway:1.0.0 .

# ---------- backend ----------
FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Fail early with a clear message if the frontend was not built.
RUN test -f web/dist/index.html || (echo "web/dist is missing: build the frontend first (cd web && pnpm build) or use scripts/package-src.sh" >&2; exit 1)
RUN go build -ldflags="-s -w -X main.version=${VERSION}" -o /out/yzapi ./cmd/yzapi

# ---------- runtime ----------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata curl && adduser -D -u 10001 yzapi
ENV YZAPI_DATA_DIR=/opt/yzapi YZAPI_LISTEN=0.0.0.0:8080 TZ=Asia/Shanghai
COPY --from=build /out/yzapi /usr/local/bin/yzapi
RUN mkdir -p /opt/yzapi && chown -R yzapi:yzapi /opt/yzapi
USER yzapi
VOLUME ["/opt/yzapi"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD curl -fsS http://127.0.0.1:8080/health/ready || exit 1
ENTRYPOINT ["/usr/local/bin/yzapi"]
