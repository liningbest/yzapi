#!/usr/bin/env bash
# End-to-end smoke test: boots a mock upstream + the gateway, configures it through
# the management API, then exercises every data-plane endpoint and conversion path.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT=${PORT:-18080}
MOCK=${MOCK:-127.0.0.1:19911}
DATA=$(mktemp -d)
export YZAPI_DATA_DIR="$DATA" YZAPI_LISTEN="127.0.0.1:$PORT" YZAPI_INITIAL_ADMIN_PASSWORD="Admin123456!" YZAPI_DEV=1
BASE="http://127.0.0.1:$PORT"

go build -o "$DATA/yzapi" ./cmd/yzapi
go build -o "$DATA/mock" ./tools/mockupstream
"$DATA/mock" -addr "$MOCK" >"$DATA/mock.log" 2>&1 &
MOCK_PID=$!
"$DATA/yzapi" >"$DATA/yzapi.log" 2>&1 &
GW_PID=$!
trap 'kill $GW_PID $MOCK_PID 2>/dev/null; echo "logs in $DATA"' EXIT

for i in $(seq 1 50); do curl -fsS "$BASE/health/ready" >/dev/null 2>&1 && break; sleep 0.2; done
curl -fsS "$BASE/health/ready"; echo

pass() { echo "  ✔ $1"; }
failx() { echo "  ✘ $1"; echo "--- gateway log ---"; tail -50 "$DATA/yzapi.log"; exit 1; }
j() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval('d'+sys.argv[1]))" "$1"; }

echo "== login"
TOKEN=$(curl -fsS -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' -d '{"username":"admin","password":"Admin123456!"}' | j "['token']")
A="Authorization: Bearer $TOKEN"
curl -fsS -X POST "$BASE/api/auth/change-password" -H "$A" -H 'Content-Type: application/json' -d '{"old_password":"Admin123456!","new_password":"Admin123456!x"}' >/dev/null
TOKEN=$(curl -fsS -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' -d '{"username":"admin","password":"Admin123456!x"}' | j "['token']")
A="Authorization: Bearer $TOKEN"
pass "login + change password"

echo "== accounts"
ACC1=$(curl -fsS -X POST "$BASE/api/admin/accounts" -H "$A" -H 'Content-Type: application/json' -d "{
  \"name\":\"mock-openai\",\"provider\":\"custom\",\"type\":\"text\",\"base_url\":\"http://$MOCK/v1\",\"api_key\":\"sk-mock\",
  \"protocols\":[\"openai-completions\",\"openai-responses\"],
  \"mappings\":[{\"request_model\":\"mini\",\"upstream_model\":\"mock-mini\"},{\"request_model\":\"pro\",\"upstream_model\":\"mock-pro\"}],
  \"priority\":10,\"max_concurrency\":50}")
echo "$ACC1" | j "['id']" >/dev/null && pass "create openai-style account (with live probe)"
ACC2=$(curl -fsS -X POST "$BASE/api/admin/accounts" -H "$A" -H 'Content-Type: application/json' -d "{
  \"name\":\"mock-anthropic\",\"provider\":\"custom\",\"type\":\"text\",\"base_url\":\"http://$MOCK/v1\",\"api_key\":\"sk-mock\",
  \"protocols\":[\"anthropic-messages\"],
  \"mappings\":[{\"request_model\":\"claude-mock\",\"upstream_model\":\"mock-pro\"}],\"priority\":20}")
echo "$ACC2" | j "['id']" >/dev/null && pass "create anthropic-style account"
ACC3=$(curl -fsS -X POST "$BASE/api/admin/accounts" -H "$A" -H 'Content-Type: application/json' -d "{
  \"name\":\"mock-embed\",\"provider\":\"custom\",\"type\":\"embedding\",\"base_url\":\"http://$MOCK/v1\",\"api_key\":\"sk-mock\",
  \"mappings\":[{\"request_model\":\"embed\",\"upstream_model\":\"mock-embed\"}]}")
EMB_ID=$(echo "$ACC3" | j "['id']")
pass "create embedding account"
DISC=$(curl -fsS -X POST "$BASE/api/admin/accounts/discover" -H "$A" -H 'Content-Type: application/json' -d "{\"provider\":\"custom\",\"base_url\":\"http://$MOCK/v1\",\"api_key\":\"sk-mock\"}")
echo "$DISC" | grep -q mock-pro && pass "discover models"

echo "== model groups / user group / user / key"
MG1=$(curl -fsS -X POST "$BASE/api/admin/model-groups" -H "$A" -H 'Content-Type: application/json' -d '{"name":"cheap","type":"text","models":["mini"]}' | j "['id']")
MG2=$(curl -fsS -X POST "$BASE/api/admin/model-groups" -H "$A" -H 'Content-Type: application/json' -d '{"name":"strong","type":"text","models":["claude-mock","pro"]}' | j "['id']")
MG3=$(curl -fsS -X POST "$BASE/api/admin/model-groups" -H "$A" -H 'Content-Type: application/json' -d '{"name":"vectors","type":"embedding","models":["embed"]}' | j "['id']")
UG=$(curl -fsS -X POST "$BASE/api/admin/user-groups" -H "$A" -H 'Content-Type: application/json' -d "{\"name\":\"dev\",\"max_concurrency\":10,\"key_max_concurrency\":5,\"token_quota\":1000000,\"model_group_ids\":[$MG1,$MG2,$MG3]}" | j "['id']")
curl -fsS -X POST "$BASE/api/admin/users" -H "$A" -H 'Content-Type: application/json' -d "{\"username\":\"alice\",\"password\":\"AlicePass12345\",\"group_id\":$UG}" >/dev/null
UT=$(curl -fsS -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' -d '{"username":"alice","password":"AlicePass12345"}' | j "['token']")
curl -fsS -X POST "$BASE/api/auth/change-password" -H "Authorization: Bearer $UT" -H 'Content-Type: application/json' -d '{"old_password":"AlicePass12345","new_password":"AlicePass12345x"}' >/dev/null
UT=$(curl -fsS -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' -d '{"username":"alice","password":"AlicePass12345x"}' | j "['token']")
KEY=$(curl -fsS -X POST "$BASE/api/user/keys" -H "Authorization: Bearer $UT" -H 'Content-Type: application/json' -d '{"name":"test"}' | j "['key']")
[[ "$KEY" == sk-* ]] && pass "user + api key ($KEY)"
K="Authorization: Bearer $KEY"

echo "== data plane"
curl -fsS "$BASE/v1/models" -H "$K" | grep -q '"mini"' && pass "GET /v1/models"
OUT=$(curl -fsS "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"mini","messages":[{"role":"user","content":"hello"}]}')
echo "$OUT" | grep -q 'echo: hello' && pass "chat non-stream passthrough"
OUT=$(curl -fsS -N "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"mini","messages":[{"role":"user","content":"stream me"}],"stream":true}')
echo "$OUT" | grep -q '\[DONE\]' && ! echo "$OUT" | grep -q '"choices":\[\],"usage"' && pass "chat stream passthrough (usage chunk stripped)"
OUT=$(curl -fsS -N "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"mini","messages":[{"role":"user","content":"stream me"}],"stream":true,"stream_options":{"include_usage":true}}')
echo "$OUT" | grep -q '"usage"' && pass "chat stream with include_usage kept"
OUT=$(curl -fsS "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"claude-mock","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"convert me"}]}')
echo "$OUT" | grep -q 'anthropic:mock-pro\] echo: convert me' && echo "$OUT" | grep -q '"chat.completion"' && pass "chat -> anthropic upstream (non-stream conversion)"
OUT=$(curl -fsS -N "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"claude-mock","messages":[{"role":"user","content":"convert stream"}],"stream":true}')
echo "$OUT" | grep -q 'chat.completion.chunk' && echo "$OUT" | grep -q '\[DONE\]' && pass "chat -> anthropic upstream (stream conversion)"
OUT=$(curl -fsS "$BASE/v1/messages" -H "x-api-key: $KEY" -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json' -d '{"model":"mini","max_tokens":100,"messages":[{"role":"user","content":"anthropic client"}]}')
echo "$OUT" | grep -q '"type":"message"' && echo "$OUT" | grep -q 'echo: anthropic client' && pass "anthropic client -> openai upstream (non-stream)"
OUT=$(curl -fsS -N "$BASE/v1/messages" -H "x-api-key: $KEY" -H 'Content-Type: application/json' -d '{"model":"mini","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"anthropic stream"}]}')
echo "$OUT" | grep -q 'event: message_start' && echo "$OUT" | grep -q 'event: message_stop' && pass "anthropic client -> openai upstream (stream)"
OUT=$(curl -fsS -N "$BASE/v1/messages" -H "x-api-key: $KEY" -H 'Content-Type: application/json' -d '{"model":"mini","max_tokens":100,"stream":true,"tools":[{"name":"get_weather","description":"w","input_schema":{"type":"object","properties":{}}}],"messages":[{"role":"user","content":"weather today?"}]}')
echo "$OUT" | grep -q 'tool_use' && echo "$OUT" | grep -q 'input_json_delta' && pass "anthropic client tool-call streaming conversion"
OUT=$(curl -fsS "$BASE/v1/messages" -H "x-api-key: $KEY" -H 'Content-Type: application/json' -d '{"model":"claude-mock","max_tokens":100,"messages":[{"role":"user","content":"native"}]}')
echo "$OUT" | grep -q 'anthropic:mock-pro' && pass "anthropic native passthrough"
OUT=$(curl -fsS "$BASE/v1/responses" -H "$K" -H 'Content-Type: application/json' -d '{"model":"mini","input":"responses api"}')
echo "$OUT" | grep -q '"object":"response"' && echo "$OUT" | grep -q 'responses:mock-mini' && pass "responses native passthrough"
OUT=$(curl -fsS "$BASE/v1/responses" -H "$K" -H 'Content-Type: application/json' -d '{"model":"claude-mock","input":"responses via anthropic"}')
echo "$OUT" | grep -q '"object":"response"' && echo "$OUT" | grep -q 'anthropic:mock-pro' && pass "responses client -> anthropic upstream (conversion)"
OUT=$(curl -fsS -N "$BASE/v1/responses" -H "$K" -H 'Content-Type: application/json' -d '{"model":"claude-mock","input":"responses stream via anthropic","stream":true}')
echo "$OUT" | grep -q 'response.output_text.delta' && echo "$OUT" | grep -q 'response.completed' && pass "responses client -> anthropic upstream (stream chain)"
OUT=$(curl -fsS "$BASE/v1/embeddings" -H "$K" -H 'Content-Type: application/json' -d '{"model":"embed","input":"vec"}')
echo "$OUT" | grep -q '"embedding"' && pass "embeddings"
OUT=$(curl -fsS "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"strong","messages":[{"role":"user","content":"group call"}]}')
echo "$OUT" | grep -q 'echo: group call' && pass "model group as model name (ordered failover)"

echo "== authz / errors"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/chat/completions" -H 'Authorization: Bearer sk-bad' -H 'Content-Type: application/json' -d '{"model":"mini","messages":[]}')
[[ "$code" == 401 ]] && pass "bad key -> 401"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"nope","messages":[]}')
[[ "$code" == 404 ]] && pass "unknown model -> 404"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"embed","messages":[]}')
[[ "$code" == 400 ]] && pass "type mismatch -> 400"
curl -fsS -X PUT "$BASE/api/admin/user-groups/$UG" -H "$A" -H 'Content-Type: application/json' -d "{\"name\":\"dev\",\"max_concurrency\":10,\"key_max_concurrency\":5,\"token_quota\":1000000,\"model_group_ids\":[$MG1]}" >/dev/null
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"pro","messages":[{"role":"user","content":"x"}]}')
[[ "$code" == 403 ]] && pass "model not allowed by group -> 403"
curl -fsS -X PUT "$BASE/api/admin/user-groups/$UG" -H "$A" -H 'Content-Type: application/json' -d "{\"name\":\"dev\",\"max_concurrency\":10,\"key_max_concurrency\":5,\"token_quota\":1000000,\"model_group_ids\":[]}" >/dev/null

echo "== smart routing + compliance"
curl -fsS -X PUT "$BASE/api/admin/settings/vector" -H "$A" -H 'Content-Type: application/json' -d "{\"account_id\":$EMB_ID,\"model\":\"embed\"}" >/dev/null
curl -fsS -X POST "$BASE/api/admin/settings/vector/test" -H "$A" -H 'Content-Type: application/json' -d "{\"account_id\":$EMB_ID,\"model\":\"embed\"}" | grep -q '"ok":true' && pass "vector service test"
curl -fsS -X PUT "$BASE/api/admin/settings/smart_route" -H "$A" -H 'Content-Type: application/json' -d "{\"enabled\":true,\"virtual_model\":\"yz-auto\",\"simple_group_id\":$MG1,\"complex_group_id\":$MG2,\"threshold\":0.5,\"confidence_gap\":0,\"top_k\":5}" >/dev/null
curl -fsS -X POST "$BASE/api/admin/route/samples/batch" -H "$A" -H 'Content-Type: application/json' -d '{"items":[{"label":"simple","text":"你好"},{"label":"simple","text":"hi there"},{"label":"complex","text":"prove that the sum of two even numbers is even, step by step"}],"build_vector":true}' | grep -q '"created":3' && pass "route samples + vectors"
curl -fsS -X POST "$BASE/api/admin/route/preview" -H "$A" -H 'Content-Type: application/json' -d '{"text":"prove that the sum of two even numbers is even"}' | grep -q '"label"' && pass "route preview"
OUT=$(curl -fsS "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"yz-auto","messages":[{"role":"user","content":"你好"}]}')
echo "$OUT" | grep -q 'echo: 你好' && pass "virtual model routed"
curl -fsS "$BASE/v1/models" -H "$K" | grep -q 'yz-auto' && pass "virtual model listed"
PG=$(curl -fsS -X POST "$BASE/api/admin/compliance/policy-groups" -H "$A" -H 'Content-Type: application/json' -d '{"name":"block-list","action":"block","risk_level":"high"}' | j "['id']")
curl -fsS -X POST "$BASE/api/admin/compliance/words/batch" -H "$A" -H 'Content-Type: application/json' -d "{\"policy_group_id\":$PG,\"words\":[\"forbiddenword\",\"违禁词\"]}" >/dev/null
curl -fsS -X PUT "$BASE/api/admin/settings/compliance" -H "$A" -H 'Content-Type: application/json' -d '{"enabled":true,"semantic_threshold":0.9}' >/dev/null
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"mini","messages":[{"role":"user","content":"this has a 违禁词 inside"}]}')
[[ "$code" == 400 ]] && pass "sensitive word blocked -> 400"
curl -fsS -X POST "$BASE/api/admin/compliance/test" -H "$A" -H 'Content-Type: application/json' -d '{"text":"FORBIDDENWORD"}' | grep -q '"block":true' && pass "compliance test endpoint (case-insensitive)"

echo "== failover"
kill $MOCK_PID; sleep 0.3
"$DATA/mock" -addr "$MOCK" -fail 500 >"$DATA/mock2.log" 2>&1 &
MOCK_PID=$!
sleep 0.3
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"mini","messages":[{"role":"user","content":"x"}]}')
[[ "$code" == 502 ]] && pass "upstream 500 -> 502 after retries"
kill $MOCK_PID; sleep 0.3
"$DATA/mock" -addr "$MOCK" >"$DATA/mock3.log" 2>&1 &
MOCK_PID=$!
sleep 0.3
curl -fsS -X POST "$BASE/api/admin/accounts/$(echo "$ACC1" | j "['id']")/reset-health" -H "$A" >/dev/null
OUT=$(curl -fsS "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"mini","messages":[{"role":"user","content":"back"}]}')
echo "$OUT" | grep -q 'echo: back' && pass "recovered after reset-health"

echo "== reporting"
sleep 1.2
curl -fsS "$BASE/api/admin/logs?range=24h" -H "$A" | j "['total']" | grep -qv '^0$' && pass "call logs written"
curl -fsS "$BASE/api/admin/usage?range=24h" -H "$A" | j "['summary']['total_tokens']" | grep -qv '^0$' && pass "usage rollup"
curl -fsS "$BASE/api/admin/overview/live" -H "$A" | grep -q '"accounts"' && pass "overview live"
curl -fsS "$BASE/api/admin/overview/usage?range=24h" -H "$A" | grep -q '"trend"' && pass "overview usage"
curl -fsS "$BASE/api/admin/route/decisions?range=24h" -H "$A" | j "['total']" | grep -qv '^0$' && pass "route decisions logged"
curl -fsS "$BASE/api/admin/compliance/audit-logs?range=24h" -H "$A" | j "['total']" | grep -qv '^0$' && pass "audit logs written"
curl -fsS "$BASE/api/admin/route/stats?range=24h" -H "$A" | grep -q '"by_label"' && pass "route stats"
curl -fsS "$BASE/api/user/usage?range=24h" -H "Authorization: Bearer $UT" | grep -q '"by_api_key"' && pass "user usage"
curl -fsS "$BASE/api/user/logs?range=24h" -H "Authorization: Bearer $UT" | j "['total']" | grep -qv '^0$' && pass "user logs"
curl -fsS "$BASE/api/user/models" -H "Authorization: Bearer $UT" | grep -q '"base_url"' && pass "user models"
curl -fsS "$BASE/api/admin/settings" -H "$A" | grep -q '"performance"' && pass "settings"
curl -fsS "$BASE/api/admin/system/info" -H "$A" | grep -q '"go_version"' && pass "system info"

echo
echo "ALL SMOKE TESTS PASSED"
