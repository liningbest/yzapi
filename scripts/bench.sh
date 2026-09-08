#!/usr/bin/env bash
# Throughput/latency benchmark: gateway vs direct mock upstream.
set -euo pipefail
cd "$(dirname "$0")/.."
PORT=${PORT:-18081}; MOCK=${MOCK:-127.0.0.1:19912}; DATA=$(mktemp -d)
export YZAPI_DATA_DIR="$DATA" YZAPI_LISTEN="127.0.0.1:$PORT" YZAPI_INITIAL_ADMIN_PASSWORD="Admin123456!"
BASE="http://127.0.0.1:$PORT"
go build -o "$DATA/yzapi" ./cmd/yzapi && go build -o "$DATA/mock" ./tools/mockupstream && go build -o "$DATA/loadgen" ./tools/loadgen
"$DATA/mock" -addr "$MOCK" -delay 0 >/dev/null 2>&1 & MP=$!
"$DATA/yzapi" >"$DATA/yzapi.log" 2>&1 & GP=$!
trap 'kill $GP $MP 2>/dev/null' EXIT
for i in $(seq 1 50); do curl -fsS "$BASE/health/ready" >/dev/null 2>&1 && break; sleep 0.2; done
j() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval('d'+sys.argv[1]))" "$1"; }
T=$(curl -fsS -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' -d '{"username":"admin","password":"Admin123456!"}' | j "['token']")
A="Authorization: Bearer $T"
curl -fsS -X POST "$BASE/api/admin/accounts" -H "$A" -H 'Content-Type: application/json' -d "{\"name\":\"m\",\"provider\":\"custom\",\"type\":\"text\",\"base_url\":\"http://$MOCK/v1\",\"api_key\":\"sk-mock\",\"protocols\":[\"openai-completions\"],\"mappings\":[{\"request_model\":\"mini\",\"upstream_model\":\"mock-mini\"}],\"max_concurrency\":0,\"skip_test\":true}" >/dev/null
KEY=$(curl -fsS -X POST "$BASE/api/user/keys" -H "$A" -H 'Content-Type: application/json' -d '{"name":"bench"}' | j "['key']")
C=${C:-128}; N=${N:-5000}
echo "### direct to mock (non-stream)";  "$DATA/loadgen" -url "http://$MOCK/v1/chat/completions" -key sk-mock -model mock-mini -c $C -n $N
echo "### via gateway (non-stream)";     "$DATA/loadgen" -url "$BASE/v1/chat/completions" -key "$KEY" -model mini -c $C -n $N
echo "### direct to mock (stream)";      "$DATA/loadgen" -url "http://$MOCK/v1/chat/completions" -key sk-mock -model mock-mini -c $C -n $N -stream
echo "### via gateway (stream)";         "$DATA/loadgen" -url "$BASE/v1/chat/completions" -key "$KEY" -model mini -c $C -n $N -stream
sleep 1.5
echo "### gateway log tail"; grep -i -E "error|warn" "$DATA/yzapi.log" | tail -5 || true
echo "### rows logged: $(curl -fsS "$BASE/api/admin/logs?range=24h&page_size=1" -H "$A" | j "['total']")"
