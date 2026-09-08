# ---------- frontend ----------
FROM node:22-alpine AS web
WORKDIR /src/web
# Pin pnpm: newer majors turn "ignored build scripts" into a hard error (ERR_PNPM_IGNORED_BUILDS)
RUN corepack enable && corepack prepare pnpm@11.22.0 --activate
# pnpm-workspace.yaml carries allowBuilds (esbuild); without it pnpm refuses the install
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# ---------- backend ----------
FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
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
