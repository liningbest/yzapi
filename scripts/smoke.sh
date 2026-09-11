#!/usr/bin/env bash
# End-to-end smoke test: boots a mock upstream + the gateway, configures it through
# the management API, then exercises every data-plane endpoint and conversion path.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT=${PORT:-18080}
MOCK=${MOCK:-127.0.0.1:19914}
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

PASSED=0
pass() { PASSED=$((PASSED+1)); echo "  ✔ $1"; }
failx() { echo "  ✘ $1"; echo "--- gateway log ---"; tail -50 "$DATA/yzapi.log"; exit 1; }
# check "<name>" <command...>: the command must succeed, otherwise the run fails.
check() { local name="$1"; shift; if "$@"; then pass "$name"; else failx "$name"; fi; }
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
if echo "$ACC1" | j "['id']" >/dev/null; then pass "create openai-style account (with live probe)"; else failx "create openai-style account (with live probe)"; fi
ACC2=$(curl -fsS -X POST "$BASE/api/admin/accounts" -H "$A" -H 'Content-Type: application/json' -d "{
  \"name\":\"mock-anthropic\",\"provider\":\"custom\",\"type\":\"text\",\"base_url\":\"http://$MOCK/v1\",\"api_key\":\"sk-mock\",
  \"protocols\":[\"anthropic-messages\"],
  \"mappings\":[{\"request_model\":\"claude-mock\",\"upstream_model\":\"mock-pro\"}],\"priority\":20}")
if echo "$ACC2" | j "['id']" >/dev/null; then pass "create anthropic-style account"; else failx "create anthropic-style account"; fi
ACC4=$(curl -fsS -X POST "$BASE/api/admin/accounts" -H "$A" -H 'Content-Type: application/json' -d "{
  \"name\":\"mock-gemini\",\"provider\":\"gemini\",\"account_type\":\"native\",\"type\":\"text\",\"base_url\":\"http://$MOCK/v1beta\",\"api_key\":\"sk-mock\",
  \"protocols\":[\"gemini-generate\"],
  \"mappings\":[{\"request_model\":\"gemini-mock\",\"upstream_model\":\"mock-gemini\"}],\"priority\":30}")
if echo "$ACC4" | j "['id']" >/dev/null && [ "$(echo "$ACC4" | j "['protocols'][0]")" = "gemini-generate" ]; then pass "create native gemini account"; else echo "$ACC4"; failx "create native gemini account"; fi
ACC3=$(curl -fsS -X POST "$BASE/api/admin/accounts" -H "$A" -H 'Content-Type: application/json' -d "{
  \"name\":\"mock-embed\",\"provider\":\"custom\",\"type\":\"embedding\",\"base_url\":\"http://$MOCK/v1\",\"api_key\":\"sk-mock\",
  \"mappings\":[{\"request_model\":\"embed\",\"upstream_model\":\"mock-embed\"}]}")
EMB_ID=$(echo "$ACC3" | j "['id']")
pass "create embedding account"
ACC1_ID=$(echo "$ACC1" | j "['id']")
UPD=$(curl -sS -X PUT "$BASE/api/admin/accounts/$ACC1_ID" -H "$A" -H 'Content-Type: application/json' -d "{
  \"name\":\"mock-openai-renamed\",\"provider\":\"custom\",\"type\":\"text\",\"base_url\":\"http://$MOCK/v1\",\"api_key\":\"******\",
  \"protocols\":[\"openai-completions\",\"openai-responses\"],
  \"mappings\":[{\"request_model\":\"mini\",\"upstream_model\":\"mock-mini\"},{\"request_model\":\"pro\",\"upstream_model\":\"mock-pro\"}],
  \"priority\":10,\"max_concurrency\":50,\"note\":\"edited\",\"skip_test\":true}")
if [ "$(echo "$UPD" | j "['name']" 2>/dev/null)" = "mock-openai-renamed" ] && [ "$(echo "$UPD" | j "['protocols'][1]" 2>/dev/null)" = "openai-responses" ]; then pass "update account basic info keeps protocols and mappings"; else echo "$UPD"; failx "update account basic info"; fi
DISC=$(curl -fsS -X POST "$BASE/api/admin/accounts/discover" -H "$A" -H 'Content-Type: application/json' -d "{\"provider\":\"custom\",\"base_url\":\"http://$MOCK/v1\",\"api_key\":\"sk-mock\"}")
if echo "$DISC" | grep -q mock-pro; then pass "discover models"; else failx "discover models"; fi

echo "== model groups / user group / user / key"
MG1=$(curl -fsS -X POST "$BASE/api/admin/model-groups" -H "$A" -H 'Content-Type: application/json' -d '{"name":"cheap","type":"text","models":["mini"]}' | j "['id']")
MG2=$(curl -fsS -X POST "$BASE/api/admin/model-groups" -H "$A" -H 'Content-Type: application/json' -d '{"name":"strong","type":"text","models":["claude-mock","pro","gemini-mock"]}' | j "['id']")
MG3=$(curl -fsS -X POST "$BASE/api/admin/model-groups" -H "$A" -H 'Content-Type: application/json' -d '{"name":"vectors","type":"embedding","models":["embed"]}' | j "['id']")
UG=$(curl -fsS -X POST "$BASE/api/admin/user-groups" -H "$A" -H 'Content-Type: application/json' -d "{\"name\":\"dev\",\"max_concurrency\":10,\"key_max_concurrency\":5,\"token_quota\":1000000,\"model_group_ids\":[$MG1,$MG2,$MG3]}" | j "['id']")
curl -fsS -X POST "$BASE/api/admin/users" -H "$A" -H 'Content-Type: application/json' -d "{\"username\":\"alice\",\"password\":\"AlicePass12345\",\"group_id\":$UG}" >/dev/null
UT=$(curl -fsS -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' -d '{"username":"alice","password":"AlicePass12345"}' | j "['token']")
curl -fsS -X POST "$BASE/api/auth/change-password" -H "Authorization: Bearer $UT" -H 'Content-Type: application/json' -d '{"old_password":"AlicePass12345","new_password":"AlicePass12345x"}' >/dev/null
UT=$(curl -fsS -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' -d '{"username":"alice","password":"AlicePass12345x"}' | j "['token']")
KEY=$(curl -fsS -X POST "$BASE/api/user/keys" -H "Authorization: Bearer $UT" -H 'Content-Type: application/json' -d '{"name":"test"}' | j "['key']")
if [[ "$KEY" == sk-* ]]; then pass "user + api key ($KEY)"; else failx "user + api key ($KEY)"; fi
K="Authorization: Bearer $KEY"

echo "== data plane"
if curl -fsS "$BASE/v1/models" -H "$K" | grep -q '"mini"'; then pass "GET /v1/models"; else failx "GET /v1/models"; fi
OUT=$(curl -fsS "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"mini","messages":[{"role":"user","content":"hello"}]}')
if echo "$OUT" | grep -q 'echo: hello'; then pass "chat non-stream passthrough"; else failx "chat non-stream passthrough"; fi
OUT=$(curl -fsS -N "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"mini","messages":[{"role":"user","content":"stream me"}],"stream":true}')
if echo "$OUT" | grep -q '\[DONE\]' && ! echo "$OUT" | grep -q '"choices":\[\],"usage"'; then pass "chat stream passthrough (usage chunk stripped)"; else failx "chat stream passthrough (usage chunk stripped)"; fi
OUT=$(curl -fsS -N "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"mini","messages":[{"role":"user","content":"stream me"}],"stream":true,"stream_options":{"include_usage":true}}')
if echo "$OUT" | grep -q '"usage"'; then pass "chat stream with include_usage kept"; else failx "chat stream with include_usage kept"; fi
OUT=$(curl -fsS "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"claude-mock","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"convert me"}]}')
if echo "$OUT" | grep -q 'anthropic:mock-pro\] echo: convert me' && echo "$OUT" | grep -q '"chat.completion"'; then pass "chat -> anthropic upstream (non-stream conversion)"; else failx "chat -> anthropic upstream (non-stream conversion)"; fi
OUT=$(curl -fsS -N "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"claude-mock","messages":[{"role":"user","content":"convert stream"}],"stream":true}')
if echo "$OUT" | grep -q 'chat.completion.chunk' && echo "$OUT" | grep -q '\[DONE\]'; then pass "chat -> anthropic upstream (stream conversion)"; else failx "chat -> anthropic upstream (stream conversion)"; fi
OUT=$(curl -fsS "$BASE/v1/messages" -H "x-api-key: $KEY" -H 'anthropic-version: 2023-06-01' -H 'Content-Type: application/json' -d '{"model":"mini","max_tokens":100,"messages":[{"role":"user","content":"anthropic client"}]}')
if echo "$OUT" | grep -q '"type":"message"' && echo "$OUT" | grep -q 'echo: anthropic client'; then pass "anthropic client -> openai upstream (non-stream)"; else failx "anthropic client -> openai upstream (non-stream)"; fi
OUT=$(curl -fsS -N "$BASE/v1/messages" -H "x-api-key: $KEY" -H 'Content-Type: application/json' -d '{"model":"mini","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"anthropic stream"}]}')
if echo "$OUT" | grep -q 'event: message_start' && echo "$OUT" | grep -q 'event: message_stop'; then pass "anthropic client -> openai upstream (stream)"; else failx "anthropic client -> openai upstream (stream)"; fi
OUT=$(curl -fsS -N "$BASE/v1/messages" -H "x-api-key: $KEY" -H 'Content-Type: application/json' -d '{"model":"mini","max_tokens":100,"stream":true,"tools":[{"name":"get_weather","description":"w","input_schema":{"type":"object","properties":{}}}],"messages":[{"role":"user","content":"weather today?"}]}')
if echo "$OUT" | grep -q 'tool_use' && echo "$OUT" | grep -q 'input_json_delta'; then pass "anthropic client tool-call streaming conversion"; else failx "anthropic client tool-call streaming conversion"; fi
OUT=$(curl -fsS "$BASE/v1/messages" -H "x-api-key: $KEY" -H 'Content-Type: application/json' -d '{"model":"claude-mock","max_tokens":100,"messages":[{"role":"user","content":"native"}]}')
if echo "$OUT" | grep -q 'anthropic:mock-pro'; then pass "anthropic native passthrough"; else failx "anthropic native passthrough"; fi
OUT=$(curl -fsS "$BASE/v1/responses" -H "$K" -H 'Content-Type: application/json' -d '{"model":"mini","input":"responses api"}')
if echo "$OUT" | grep -q '"object":"response"' && echo "$OUT" | grep -q 'responses:mock-mini'; then pass "responses native passthrough"; else failx "responses native passthrough"; fi
OUT=$(curl -fsS "$BASE/v1/responses" -H "$K" -H 'Content-Type: application/json' -d '{"model":"claude-mock","input":"responses via anthropic"}')
if echo "$OUT" | grep -q '"object":"response"' && echo "$OUT" | grep -q 'anthropic:mock-pro'; then pass "responses client -> anthropic upstream (conversion)"; else failx "responses client -> anthropic upstream (conversion)"; fi
OUT=$(curl -fsS -N "$BASE/v1/responses" -H "$K" -H 'Content-Type: application/json' -d '{"model":"claude-mock","input":"responses stream via anthropic","stream":true}')
if echo "$OUT" | grep -q 'response.output_text.delta' && echo "$OUT" | grep -q 'response.completed'; then pass "responses client -> anthropic upstream (stream chain)"; else failx "responses client -> anthropic upstream (stream chain)"; fi
OUT=$(curl -fsS "$BASE/v1beta/models/gemini-mock:generateContent" -H "x-goog-api-key: $KEY" -H 'Content-Type: application/json' -d '{"contents":[{"role":"user","parts":[{"text":"gemini native"}]}]}')
if echo "$OUT" | grep -q 'gemini:mock-gemini\] echo: gemini native' && echo "$OUT" | grep -q '"usageMetadata"'; then pass "gemini native passthrough (x-goog-api-key)"; else echo "$OUT"; failx "gemini native passthrough"; fi
OUT=$(curl -fsS -N "$BASE/v1beta/models/gemini-mock:streamGenerateContent?alt=sse&key=$KEY" -H 'Content-Type: application/json' -d '{"contents":[{"role":"user","parts":[{"text":"gemini stream"}]}]}')
if echo "$OUT" | grep -q '"finishReason":"STOP"' && echo "$OUT" | grep -q 'promptTokenCount'; then pass "gemini native stream passthrough (?key=)"; else echo "$OUT"; failx "gemini native stream passthrough"; fi
OUT=$(curl -fsS "$BASE/v1beta/models/mini:generateContent" -H "x-goog-api-key: $KEY" -H 'Content-Type: application/json' -d '{"systemInstruction":{"parts":[{"text":"be brief"}]},"contents":[{"role":"user","parts":[{"text":"gemini via openai"}]}]}')
if echo "$OUT" | grep -q '"candidates"' && echo "$OUT" | grep -q 'mock-mini\] echo: gemini via openai'; then pass "gemini client -> openai upstream (conversion)"; else echo "$OUT"; failx "gemini client -> openai upstream"; fi
OUT=$(curl -fsS -N "$BASE/v1beta/models/claude-mock:streamGenerateContent?alt=sse" -H "x-goog-api-key: $KEY" -H 'Content-Type: application/json' -d '{"contents":[{"role":"user","parts":[{"text":"gemini via anthropic"}]}]}')
if echo "$OUT" | grep -q '"finishReason":"STOP"' && echo "$OUT" | grep -q 'anthropic:mock-pro'; then pass "gemini client -> anthropic upstream (stream chain)"; else echo "$OUT"; failx "gemini client -> anthropic upstream (stream chain)"; fi
OUT=$(curl -fsS "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"gemini-mock","messages":[{"role":"user","content":"chat via gemini"}]}')
if echo "$OUT" | grep -q '"chat.completion"' && echo "$OUT" | grep -q 'gemini:mock-gemini\] echo: chat via gemini' && echo "$OUT" | grep -q '"prompt_tokens":11'; then pass "openai client -> gemini upstream (conversion + usage)"; else echo "$OUT"; failx "openai client -> gemini upstream"; fi
OUT=$(curl -fsS -N "$BASE/v1/messages" -H "x-api-key: $KEY" -H 'Content-Type: application/json' -d '{"model":"gemini-mock","max_tokens":100,"stream":true,"tools":[{"name":"get_weather","description":"w","input_schema":{"type":"object","properties":{}}}],"messages":[{"role":"user","content":"weather today?"}]}')
if echo "$OUT" | grep -q 'tool_use' && echo "$OUT" | grep -q 'message_stop'; then pass "anthropic client -> gemini upstream (tool-call stream chain)"; else echo "$OUT"; failx "anthropic client -> gemini upstream (tool-call stream chain)"; fi
OUT=$(curl -fsS "$BASE/v1beta/models" -H "x-goog-api-key: $KEY")
if echo "$OUT" | grep -q '"name":"models/gemini-mock"' && echo "$OUT" | grep -q 'generateContent'; then pass "gemini model list"; else echo "$OUT"; failx "gemini model list"; fi
code=$(curl -s -o /tmp/yz_gem_err -w '%{http_code}' "$BASE/v1beta/models/gemini-mock:generateContent" -H 'x-goog-api-key: sk-bad' -H 'Content-Type: application/json' -d '{"contents":[]}')
if [ "$code" = "401" ] && grep -q 'UNAUTHENTICATED' /tmp/yz_gem_err; then pass "gemini error shape (401 UNAUTHENTICATED)"; else failx "gemini error shape"; fi
OUT=$(curl -fsS "$BASE/v1/embeddings" -H "$K" -H 'Content-Type: application/json' -d '{"model":"embed","input":"vec"}')
if echo "$OUT" | grep -q '"embedding"'; then pass "embeddings"; else failx "embeddings"; fi
OUT=$(curl -fsS "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"strong","messages":[{"role":"user","content":"group call"}]}')
if echo "$OUT" | grep -q 'echo: group call'; then pass "model group as model name (ordered failover)"; else failx "model group as model name (ordered failover)"; fi

echo "== authz / errors"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/chat/completions" -H 'Authorization: Bearer sk-bad' -H 'Content-Type: application/json' -d '{"model":"mini","messages":[]}')
if [[ "$code" == 401 ]]; then pass "bad key -> 401"; else failx "bad key -> 401"; fi
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"nope","messages":[]}')
if [[ "$code" == 404 ]]; then pass "unknown model -> 404"; else failx "unknown model -> 404"; fi
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"embed","messages":[]}')
if [[ "$code" == 400 ]]; then pass "type mismatch -> 400"; else failx "type mismatch -> 400"; fi
curl -fsS -X PUT "$BASE/api/admin/user-groups/$UG" -H "$A" -H 'Content-Type: application/json' -d "{\"name\":\"dev\",\"max_concurrency\":10,\"key_max_concurrency\":5,\"token_quota\":1000000,\"model_group_ids\":[$MG1]}" >/dev/null
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"pro","messages":[{"role":"user","content":"x"}]}')
if [[ "$code" == 403 ]]; then pass "model not allowed by group -> 403"; else failx "model not allowed by group -> 403"; fi
curl -fsS -X PUT "$BASE/api/admin/user-groups/$UG" -H "$A" -H 'Content-Type: application/json' -d "{\"name\":\"dev\",\"max_concurrency\":10,\"key_max_concurrency\":5,\"token_quota\":1000000,\"model_group_ids\":[]}" >/dev/null

echo "== smart routing + compliance"
curl -fsS -X PUT "$BASE/api/admin/settings/vector" -H "$A" -H 'Content-Type: application/json' -d "{\"account_id\":$EMB_ID,\"model\":\"embed\"}" >/dev/null
if curl -fsS -X POST "$BASE/api/admin/settings/vector/test" -H "$A" -H 'Content-Type: application/json' -d "{\"account_id\":$EMB_ID,\"model\":\"embed\"}" | grep -q '"ok":true'; then pass "vector service test"; else failx "vector service test"; fi
curl -fsS -X PUT "$BASE/api/admin/settings/smart_route" -H "$A" -H 'Content-Type: application/json' -d "{\"enabled\":true,\"virtual_model\":\"yz-auto\",\"simple_group_id\":$MG1,\"complex_group_id\":$MG2,\"threshold\":0.5,\"confidence_gap\":0,\"top_k\":5}" >/dev/null
if curl -fsS -X POST "$BASE/api/admin/route/samples/batch" -H "$A" -H 'Content-Type: application/json' -d '{"items":[{"label":"simple","text":"你好"},{"label":"simple","text":"hi there"},{"label":"complex","text":"prove that the sum of two even numbers is even, step by step"}],"build_vector":true}' | grep -q '"created":3'; then pass "route samples + vectors"; else failx "route samples + vectors"; fi
if curl -fsS -X POST "$BASE/api/admin/route/preview" -H "$A" -H 'Content-Type: application/json' -d '{"text":"prove that the sum of two even numbers is even"}' | grep -q '"label"'; then pass "route preview"; else failx "route preview"; fi
OUT=$(curl -fsS "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"yz-auto","messages":[{"role":"user","content":"你好"}]}')
if echo "$OUT" | grep -q 'echo: 你好'; then pass "virtual model routed"; else failx "virtual model routed"; fi
if curl -fsS "$BASE/v1/models" -H "$K" | grep -q 'yz-auto'; then pass "virtual model listed"; else failx "virtual model listed"; fi
PG=$(curl -fsS -X POST "$BASE/api/admin/compliance/policy-groups" -H "$A" -H 'Content-Type: application/json' -d '{"name":"block-list","action":"block","risk_level":"high"}' | j "['id']")
curl -fsS -X POST "$BASE/api/admin/compliance/words/batch" -H "$A" -H 'Content-Type: application/json' -d "{\"policy_group_id\":$PG,\"words\":[\"forbiddenword\",\"违禁词\"]}" >/dev/null
curl -fsS -X PUT "$BASE/api/admin/settings/compliance" -H "$A" -H 'Content-Type: application/json' -d '{"enabled":true,"semantic_threshold":0.9}' >/dev/null
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"mini","messages":[{"role":"user","content":"this has a 违禁词 inside"}]}')
if [[ "$code" == 400 ]]; then pass "sensitive word blocked -> 400"; else failx "sensitive word blocked -> 400"; fi
if curl -fsS -X POST "$BASE/api/admin/compliance/test" -H "$A" -H 'Content-Type: application/json' -d '{"text":"FORBIDDENWORD"}' | grep -q '"block":true'; then pass "compliance test endpoint (case-insensitive)"; else failx "compliance test endpoint (case-insensitive)"; fi

echo "== failover"
kill $MOCK_PID; sleep 0.3
"$DATA/mock" -addr "$MOCK" -fail 500 >"$DATA/mock2.log" 2>&1 &
MOCK_PID=$!
sleep 0.3
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"mini","messages":[{"role":"user","content":"x"}]}')
if [[ "$code" == 502 ]]; then pass "upstream 500 -> 502 after retries"; else failx "upstream 500 -> 502 after retries"; fi
kill $MOCK_PID; sleep 0.3
"$DATA/mock" -addr "$MOCK" >"$DATA/mock3.log" 2>&1 &
MOCK_PID=$!
sleep 0.3
curl -fsS -X POST "$BASE/api/admin/accounts/$(echo "$ACC1" | j "['id']")/reset-health" -H "$A" >/dev/null
OUT=$(curl -fsS "$BASE/v1/chat/completions" -H "$K" -H 'Content-Type: application/json' -d '{"model":"mini","messages":[{"role":"user","content":"back"}]}')
if echo "$OUT" | grep -q 'echo: back'; then pass "recovered after reset-health"; else failx "recovered after reset-health"; fi

echo "== reporting"
sleep 1.2
if curl -fsS "$BASE/api/admin/logs?range=24h" -H "$A" | j "['total']" | grep -qv '^0$'; then pass "call logs written"; else failx "call logs written"; fi
if curl -fsS "$BASE/api/admin/usage?range=24h" -H "$A" | j "['summary']['total_tokens']" | grep -qv '^0$'; then pass "usage rollup"; else failx "usage rollup"; fi
if curl -fsS "$BASE/api/admin/overview/live" -H "$A" | grep -q '"accounts"'; then pass "overview live"; else failx "overview live"; fi
if curl -fsS "$BASE/api/admin/overview/usage?range=24h" -H "$A" | grep -q '"trend"'; then pass "overview usage"; else failx "overview usage"; fi
if curl -fsS "$BASE/api/admin/route/decisions?range=24h" -H "$A" | j "['total']" | grep -qv '^0$'; then pass "route decisions logged"; else failx "route decisions logged"; fi
if curl -fsS "$BASE/api/admin/compliance/audit-logs?range=24h" -H "$A" | j "['total']" | grep -qv '^0$'; then pass "audit logs written"; else failx "audit logs written"; fi
if curl -fsS "$BASE/api/admin/route/stats?range=24h" -H "$A" | grep -q '"by_label"'; then pass "route stats"; else failx "route stats"; fi
if curl -fsS "$BASE/api/user/usage?range=24h" -H "Authorization: Bearer $UT" | grep -q '"by_api_key"'; then pass "user usage"; else failx "user usage"; fi
if curl -fsS "$BASE/api/user/logs?range=24h" -H "Authorization: Bearer $UT" | j "['total']" | grep -qv '^0$'; then pass "user logs"; else failx "user logs"; fi
if curl -fsS "$BASE/api/user/models" -H "Authorization: Bearer $UT" | grep -q '"base_url"'; then pass "user models"; else failx "user models"; fi
if curl -fsS "$BASE/api/admin/settings" -H "$A" | grep -q '"performance"'; then pass "settings"; else failx "settings"; fi
if curl -fsS "$BASE/api/admin/system/info" -H "$A" | grep -q '"go_version"'; then pass "system info"; else failx "system info"; fi

echo
EXPECTED=56
if [ "$PASSED" -ne "$EXPECTED" ]; then echo "only $PASSED/$EXPECTED checks ran"; exit 1; fi
echo "ALL $PASSED SMOKE TESTS PASSED"
