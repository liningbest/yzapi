#!/usr/bin/env python3
"""Per-chunk arrival timeline of one streaming chat request.

Point it at the gateway's local port and at the public domain with the same key and
model (ideally a mock upstream with a fixed cadence, e.g. tools/mockupstream -delay 50ms
added as an account) and compare where the gaps appear.

  python3 scripts/stream-timeline.py http://127.0.0.1:8089/v1 sk-... mini
  python3 scripts/stream-timeline.py https://gw.example.com/v1 sk-... mini
"""
import json, statistics, sys, time, urllib.request

base, key, model = sys.argv[1].rstrip("/"), sys.argv[2], sys.argv[3]
prompt = sys.argv[4] if len(sys.argv) > 4 else "Write 20 numbered lines about streaming."
body = json.dumps({"model": model, "stream": True, "messages": [{"role": "user", "content": prompt}]}).encode()
req = urllib.request.Request(base + "/chat/completions", data=body, method="POST")
req.add_header("Authorization", "Bearer " + key)
req.add_header("Content-Type", "application/json")
t0 = time.perf_counter()
resp = urllib.request.urlopen(req, timeout=120)
ttfb = time.perf_counter() - t0
arrivals, first_token, chunks = [], None, 0
for raw in resp:
    now = time.perf_counter() - t0
    line = raw.decode("utf-8", "replace").strip()
    if not line.startswith("data:"):
        continue
    data = line[5:].strip()
    if data == "[DONE]":
        break
    chunks += 1
    arrivals.append(now)
    if first_token is None and '"content"' in data:
        first_token = now
total = time.perf_counter() - t0
gaps = [b - a for a, b in zip(arrivals, arrivals[1:])]
print(f"target      {base}")
print(f"headers     {ttfb*1000:.0f} ms   first token {(first_token or 0)*1000:.0f} ms   total {total*1000:.0f} ms   chunks {chunks}")
if gaps:
    print(f"gap p50     {statistics.median(gaps)*1000:.1f} ms   p90 {sorted(gaps)[int(len(gaps)*0.9)-1]*1000:.1f} ms   max {max(gaps)*1000:.1f} ms")
    burst = sum(1 for g in gaps if g < 0.002)
    print(f"bursty      {burst}/{len(gaps)} chunks arrived <2 ms after the previous one (batching upstream/proxy if high)")
