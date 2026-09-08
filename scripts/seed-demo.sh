#!/usr/bin/env bash
# Seeds a running gateway with demo accounts/groups/users and generates traffic.
# Usage: BASE=http://127.0.0.1:8080 MOCK=127.0.0.1:9911 ADMIN_PASS=... ./scripts/seed-demo.sh
set -euo pipefail
BASE=${BASE:-http://127.0.0.1:8080}
MOCK=${MOCK:-127.0.0.1:9911}
ADMIN_PASS=${ADMIN_PASS:-Admin123456!}
j() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval('d'+sys.argv[1]))" "$1"; }
post() { curl -fsS -X POST "$BASE$1" -H "$A" -H 'Content-Type: application/json' -d "$2"; }
put() { curl -fsS -X PUT "$BASE$1" -H "$A" -H 'Content-Type: application/json' -d "$2"; }

T=$(curl -fsS -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}" | j "['token']")
A="Authorization: Bearer $T"
ME=$(curl -fsS "$BASE/api/auth/me" -H "$A")
if echo "$ME" | grep -q '"must_change_password":true'; then
  post /api/auth/change-password "{\"old_password\":\"$ADMIN_PASS\",\"new_password\":\"${ADMIN_PASS}\"}" >/dev/null 2>&1 || true
fi

echo "accounts"
post /api/admin/accounts "{\"name\":\"OpenAI 主账号\",\"provider\":\"custom\",\"type\":\"text\",\"base_url\":\"http://$MOCK/v1\",\"api_key\":\"sk-mock-openai\",\"protocols\":[\"openai-completions\",\"openai-responses\"],\"mappings\":[{\"request_model\":\"gpt-4o\",\"upstream_model\":\"mock-pro\"},{\"request_model\":\"gpt-4o-mini\",\"upstream_model\":\"mock-mini\"}],\"priority\":10,\"max_concurrency\":100,\"note\":\"按量付费\"}" >/dev/null
post /api/admin/accounts "{\"name\":\"DeepSeek\",\"provider\":\"custom\",\"type\":\"text\",\"base_url\":\"http://$MOCK/v1\",\"api_key\":\"sk-mock-ds\",\"protocols\":[\"openai-completions\",\"anthropic-messages\"],\"mappings\":[{\"request_model\":\"deepseek-chat\",\"upstream_model\":\"mock-mini\"},{\"request_model\":\"deepseek-reasoner\",\"upstream_model\":\"mock-pro\"}],\"priority\":20,\"max_concurrency\":50}" >/dev/null
post /api/admin/accounts "{\"name\":\"Claude 官方\",\"provider\":\"custom\",\"type\":\"text\",\"base_url\":\"http://$MOCK/v1\",\"api_key\":\"sk-mock-claude\",\"protocols\":[\"anthropic-messages\"],\"mappings\":[{\"request_model\":\"claude-sonnet\",\"upstream_model\":\"mock-pro\"}],\"priority\":30,\"max_concurrency\":20}" >/dev/null
EMB=$(post /api/admin/accounts "{\"name\":\"向量服务\",\"provider\":\"custom\",\"type\":\"embedding\",\"base_url\":\"http://$MOCK/v1\",\"api_key\":\"sk-mock-emb\",\"mappings\":[{\"request_model\":\"text-embedding\",\"upstream_model\":\"mock-embed\"}]}" | j "['id']")
post /api/admin/accounts "{\"name\":\"文生图\",\"provider\":\"custom\",\"type\":\"image\",\"base_url\":\"http://$MOCK/v1\",\"api_key\":\"sk-mock-img\",\"mappings\":[{\"request_model\":\"dall-e\",\"upstream_model\":\"mock-image\"}]}" >/dev/null

echo "model groups"
MG1=$(post /api/admin/model-groups '{"name":"简单模型","type":"text","models":["gpt-4o-mini","deepseek-chat"],"note":"便宜、快"}' | j "['id']")
MG2=$(post /api/admin/model-groups '{"name":"复杂模型","type":"text","models":["claude-sonnet","gpt-4o","deepseek-reasoner"],"note":"能力强"}' | j "['id']")
MG3=$(post /api/admin/model-groups '{"name":"向量模型","type":"embedding","models":["text-embedding"]}' | j "['id']")

echo "settings"
put /api/admin/settings/vector "{\"account_id\":$EMB,\"model\":\"text-embedding\"}" >/dev/null
put /api/admin/settings/smart_route "{\"enabled\":true,\"virtual_model\":\"yz-auto\",\"simple_group_id\":$MG1,\"complex_group_id\":$MG2,\"threshold\":0.5,\"confidence_gap\":0,\"top_k\":5}" >/dev/null
post /api/admin/route/samples/batch '{"items":[{"label":"simple","text":"你好"},{"label":"simple","text":"今天天气怎么样"},{"label":"simple","text":"翻译成英文：早上好"},{"label":"complex","text":"请证明两个偶数之和仍是偶数，并逐步推导"},{"label":"complex","text":"设计一个高可用的分布式缓存架构并分析取舍"},{"label":"complex","text":"帮我重构这段 Go 代码并解释性能问题"}],"build_vector":true}' >/dev/null
PG=$(post /api/admin/compliance/policy-groups '{"name":"高风险拦截","action":"block","risk_level":"high","description":"命中即拦截"}' | j "['id']")
PG2=$(post /api/admin/compliance/policy-groups '{"name":"敏感审计","action":"audit","risk_level":"medium","description":"仅记录"}' | j "['id']")
post /api/admin/compliance/words/batch "{\"policy_group_id\":$PG,\"words\":[\"违禁词\",\"forbiddenword\"]}" >/dev/null
post /api/admin/compliance/words/batch "{\"policy_group_id\":$PG2,\"words\":[\"内部机密\"]}" >/dev/null
put /api/admin/settings/compliance '{"enabled":true,"semantic_threshold":0.9}' >/dev/null

echo "groups & users"
UG=$(post /api/admin/user-groups "{\"name\":\"研发组\",\"max_concurrency\":50,\"key_max_concurrency\":10,\"token_quota\":50000000,\"model_group_ids\":[$MG1,$MG2,$MG3],\"note\":\"研发团队\"}" | j "['id']")
UG2=$(post /api/admin/user-groups "{\"name\":\"运营组\",\"max_concurrency\":10,\"key_max_concurrency\":3,\"token_quota\":2000000,\"model_group_ids\":[$MG1]}" | j "['id']")
for u in alice bob; do post /api/admin/users "{\"username\":\"$u\",\"password\":\"UserPass123456\",\"group_id\":$UG,\"note\":\"研发\"}" >/dev/null; done
post /api/admin/users "{\"username\":\"carol\",\"password\":\"UserPass123456\",\"group_id\":$UG2,\"note\":\"运营\"}" >/dev/null

echo "keys + traffic"
declare -a KEYS
for u in alice bob carol; do
  UT=$(curl -fsS -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"$u\",\"password\":\"UserPass123456\"}" | j "['token']")
  curl -fsS -X POST "$BASE/api/auth/change-password" -H "Authorization: Bearer $UT" -H 'Content-Type: application/json' -d '{"old_password":"UserPass123456","new_password":"UserPass123456x"}' >/dev/null
  UT=$(curl -fsS -X POST "$BASE/api/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"$u\",\"password\":\"UserPass123456x\"}" | j "['token']")
  K=$(curl -fsS -X POST "$BASE/api/user/keys" -H "Authorization: Bearer $UT" -H 'Content-Type: application/json' -d "{\"name\":\"$u 的默认 Key\"}" | j "['key']")
  KEYS+=("$K")
done
models=(gpt-4o gpt-4o-mini deepseek-chat claude-sonnet yz-auto)
prompts=("你好" "请证明两个偶数之和仍是偶数" "翻译成英文：早上好" "设计一个分布式缓存架构" "weather today?")
for i in $(seq 1 60); do
  K=${KEYS[$((i % 2))]}
  m=${models[$((i % 5))]}
  p=${prompts[$((i % 5))]}
  if (( i % 3 == 0 )); then
    curl -s -N "$BASE/v1/chat/completions" -H "Authorization: Bearer $K" -H 'Content-Type: application/json' -d "{\"model\":\"$m\",\"messages\":[{\"role\":\"user\",\"content\":\"$p\"}],\"stream\":true}" >/dev/null
  else
    curl -s "$BASE/v1/chat/completions" -H "Authorization: Bearer $K" -H 'Content-Type: application/json' -d "{\"model\":\"$m\",\"messages\":[{\"role\":\"user\",\"content\":\"$p\"}]}" >/dev/null
  fi
done
curl -s "$BASE/v1/chat/completions" -H "Authorization: Bearer ${KEYS[2]}" -H 'Content-Type: application/json' -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"这里有违禁词"}]}' >/dev/null
curl -s "$BASE/v1/chat/completions" -H "Authorization: Bearer ${KEYS[2]}" -H 'Content-Type: application/json' -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"内部机密文件"}]}' >/dev/null
curl -s "$BASE/v1/chat/completions" -H "Authorization: Bearer ${KEYS[2]}" -H 'Content-Type: application/json' -d '{"model":"claude-sonnet","messages":[{"role":"user","content":"not allowed"}]}' >/dev/null
curl -s "$BASE/v1/embeddings" -H "Authorization: Bearer ${KEYS[0]}" -H 'Content-Type: application/json' -d '{"model":"text-embedding","input":"hello"}' >/dev/null
curl -s "$BASE/v1/messages" -H "x-api-key: ${KEYS[0]}" -H 'Content-Type: application/json' -d '{"model":"gpt-4o","max_tokens":50,"messages":[{"role":"user","content":"anthropic client"}]}' >/dev/null
echo "done. alice key: ${KEYS[0]}"
