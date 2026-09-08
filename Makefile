VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all web build run dev test smoke bench docker clean

all: web build

web:
	cd web && pnpm install && pnpm build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/yzapi ./cmd/yzapi

run: build
	YZAPI_DATA_DIR=./data YZAPI_DEV=1 ./bin/yzapi

# Backend in dev mode + Vite dev server with proxy (run in two terminals or use this target with &)
dev:
	YZAPI_DATA_DIR=./data YZAPI_DEV=1 YZAPI_LISTEN=127.0.0.1:8080 go run ./cmd/yzapi & \
	cd web && pnpm dev

test:
	go test ./...

smoke:
	./scripts/smoke.sh

bench:
	go run ./tools/mockupstream -delay 0 & \
	sleep 1; go run ./tools/loadgen -url http://127.0.0.1:8080/v1/chat/completions -key $(KEY) -c 128 -n 5000

docker: web
	docker build --build-arg VERSION=$(VERSION) -t yzapi/gateway:$(VERSION) -t yzapi/gateway:latest .

clean:
	rm -rf bin web/dist
