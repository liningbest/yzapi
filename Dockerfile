# ---------- frontend ----------
FROM node:22-alpine AS web
WORKDIR /src/web
# pnpm 9 on purpose: pnpm 10+ keeps its store index in SQLite, which fails with
# "disk I/O error" on some Docker storage drivers (seen on overlay2 hosts), and newer
# majors also refuse installs over un-approved build scripts. pnpm 9 reads the same
# lockfile (v9.0) and needs neither. Installed via npm so corepack cannot swap versions.
RUN npm install -g pnpm@9.15.9
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
# npm run: pnpm 9 would treat the local pnpm-workspace.yaml (pnpm 11 allowBuilds) as a workspace file
RUN npm run build

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
