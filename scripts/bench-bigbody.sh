#!/usr/bin/env bash
# Gateway front-end overhead on large request bodies: same-protocol Anthropic stream,
# gateway vs direct zero-delay mock, curl time_starttransfer (first response byte) median.
#   YZBIN=<gateway binary> [N=20] [PORT=18120] [MPORT=19950] scripts/bench-bigbody.sh
# Builds the mock from the repo. Clear HTTP_PROXY/HTTPS_PROXY first (loopback is bypassed
# since 1.0.12, but the gateway under test may be older). Single client, no concurrency:
# this measures request parsing / re-encoding / extraction, not generation smoothness.
set -euo pipefail
cd "$(dirname "$0")/.."
YZBIN=${YZBIN:?gateway binary path}; D=$(mktemp -d); PORT=${PORT:-18120}; MPORT=${MPORT:-19950}; N=${N:-20}
MOCKBIN="$D/mock"; go build -o "$MOCKBIN" ./tools/mockupstream
RAW=${RAW:-}   # file to append every sample line to (kb target status seconds)
echo "gateway=$YZBIN sha256=$(shasum -a 256 "$YZBIN" | cut -c1-12) label=${LABEL:-} worktree=$(git rev-parse --short HEAD) N=$N proxy=${HTTP_PROXY:-none} $(date '+%F %T')"
"$MOCKBIN" -addr 127.0.0.1:$MPORT -delay 0 >"$D/mock.log" 2>&1 & MP=$!
YZAPI_DATA_DIR=$D YZAPI_LISTEN=127.0.0.1:$PORT YZAPI_INITIAL_ADMIN_PASSWORD='Admin123456!' "$YZBIN" >"$D/yz.log" 2>&1 & GP=$!
trap 'kill $GP $MP 2>/dev/null' EXIT
B=http://127.0.0.1:$PORT
for i in $(seq 1 50); do curl -fsS $B/health/ready >/dev/null 2>&1 && break; sleep 0.2; done
for i in $(seq 1 50); do curl -fsS -H 'Authorization: Bearer x' http://127.0.0.1:$MPORT/v1/models >/dev/null 2>&1 && break; sleep 0.2; done
j() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval('d'+sys.argv[1]))" "$1"; }
T=$(curl -fsS -X POST $B/api/auth/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"Admin123456!"}' | j "['token']")
A="Authorization: Bearer $T"
ACC=$(curl -sS -X POST $B/api/admin/accounts -H "$A" -H 'Content-Type: application/json' -d "{\"name\":\"a\",\"provider\":\"custom-anthropic\",\"type\":\"text\",\"base_url\":\"http://127.0.0.1:$MPORT/v1\",\"api_key\":\"sk-mock\",\"protocols\":[\"anthropic-messages\"],\"mappings\":[{\"request_model\":\"claude\",\"upstream_model\":\"mock-pro\"}]}")
echo "$ACC" | j "['id']" >/dev/null 2>&1 || { echo "account creation failed: $ACC"; exit 1; }
curl -fsS -X POST $B/api/admin/users -H "$A" -H 'Content-Type: application/json' -d '{"username":"bob","password":"BobPass12345"}' >/dev/null
UT=$(curl -fsS -X POST $B/api/auth/login -H 'Content-Type: application/json' -d '{"username":"bob","password":"BobPass12345"}' | j "['token']")
KEY=$(curl -fsS -X POST $B/api/user/keys -H "Authorization: Bearer $UT" -H 'Content-Type: application/json' -d '{"name":"b"}' | j "['key']")
for KB in 4 200 1000; do
  python3 - "$KB" "$D/body.json" <<'PY'
import json,sys
kb=int(sys.argv[1]); text="x"*1024
msgs=[]
while len(json.dumps(msgs))<kb*1024:
    msgs.append({"role":"user","content":text}); msgs.append({"role":"assistant","content":text})
msgs.append({"role":"user","content":"final question"})
json.dump({"model":"claude","max_tokens":50,"stream":True,"messages":msgs},open(sys.argv[2],"w"))
PY
  SZ=$(stat -f%z "$D/body.json")
  for target in "gateway $B/v1/messages x-api-key:$KEY" "direct http://127.0.0.1:$MPORT/v1/messages x-api-key:sk-mock"; do
    set -- $target; name=$1; url=$2; hdr=$3
    for i in $(seq 1 3); do curl -s -o /dev/null "$url" -H "${hdr/:/: }" -H 'Content-Type: application/json' --data-binary @"$D/body.json"; done
    # one line per sample: http_code time_starttransfer; non-200 samples are dropped from the median
    samples=$(for i in $(seq 1 $N); do curl -s -o /dev/null -w '%{http_code} %{time_starttransfer}\n' "$url" -H "${hdr/:/: }" -H 'Content-Type: application/json' --data-binary @"$D/body.json"; done)
    if [ -n "$RAW" ]; then echo "$samples" | sed "s/^/$((SZ/1024))KB $name /" >> "$RAW"; fi
    ok=$(echo "$samples" | awk '$1==200' | wc -l | tr -d ' ')
    med=$(echo "$samples" | awk '$1==200 {print $2}' | sort -n | awk '{a[NR]=$1} END{print a[int((NR+1)/2)]}')
    printf "%5s KB  %-7s ttfb_p50=%.1f ms  (ok=%s/%s)\n" "$((SZ/1024))" "$name" "$(echo "$med*1000" | bc -l)" "$ok" "$N"
  done
done
