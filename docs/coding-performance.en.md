> English version of [coding-performance.md](coding-performance.md). If the two differ, the Chinese file is authoritative.

# Performance recommendations for coding scenarios

Date: 2026-09-11 (revised per independent review comments). Aimed at Claude Code / Codex / Cline / Gemini CLI: what matters is time to first token and smooth streaming, not load-test throughput.

## Conclusion

For coding tools, protocol completeness, reliable metering and the first-token / streaming experience come first. Start by using the upstream protocol that matches the client's actual entry point, remove external calls the business does not need, and then locate bottlenecks with the uniformly defined per-stage timings. Reusing the request body on same-protocol requests and skipping unused extraction are candidate optimizations, but their benefit must be verified on real-sized requests under representative concurrency. Response-header time is not the client's first token, write time alone cannot establish reverse-proxy buffering, and tok/s by itself cannot prove that forwarding is faster. Proxy, retry and fsync are chosen according to reachability, success rate and durability requirements; they must not be uniformly disabled or lowered for the sake of benchmark numbers.

## How much the gateway itself adds (measured 2026-09-11)

Same-protocol Anthropic streaming against a directly connected zero-latency mock; median time to curl's first response byte (20 runs, local machine, proxy variables cleared):

| Request body | Direct to mock | Through gateway | Added by gateway |
|---|---|---|---|
| 4 KB | 0.6 ms | 0.9 ms | 0.3 ms |
| 200 KB | 1.7 ms | 4.6 ms | 2.9 ms |
| 1 MB | 6.1 ms | 19.0 ms | 12.9 ms |

The added part is mainly reading the whole body, JSON scanning, re-encoding after the model name rewrite, and text extraction for compliance / routing. With a real model's first token taking 500 ms to several seconds, the 3 ms on a 200 KB conversation body is below 1%; on a 1 MB context it is about 13 ms. This is the basis for "do not change the hot path first"; if real conversation bodies are commonly above 1 MB, same-protocol pass-through of the request body is worth doing, but A/B it against this set of numbers.

## What you can do now (configuration level)

| Priority | What to do | Rationale |
|---|---|---|
| 1 | Give each client an account on the upstream's native protocol: Claude Code → Anthropic-compatible entry (`anthropic-messages`); Codex → OpenAI (`openai-responses`); OpenAI-compatible tools → `openai-completions`; Gemini CLI → native Gemini (`gemini-generate`) | Same protocol only rewrites the model name (Chat streaming also injects `stream_options`) and then forwards; cross-protocol converts the whole body and encodes / decodes streams event by event, and Anthropic ↔ Responses / Gemini goes through two chained hops |
| 2 | Keep "Content compliance (内容合规)" off unless the business needs it; do not send coding requests through the virtual model `yz-auto` | Semantic moderation and smart routing call the vector service; whether they are actually triggered is shown by the routing decision records and the vector call count, do not infer it from the request body size |
| 3 | Set "Maximum attempts (最多尝试次数)" as needed, and note that it is the **total count including the first attempt** | Setting it to 1 means no account switch after a failure; when adjusting it, compare success rate, retry count and failure reasons, not just the average latency of successful requests |
| 4 | Decide the outbound proxy by measured reachability; keep `YZAPI_JOURNAL_FSYNC` at the established durability requirement; turn off body auditing if not needed | A forced proxy adds latency to every hop, but whether direct connection is reachable must be measured; fsync is not a performance switch, changing it changes the loss boundary |
| 5 | Only when a specific provider's stream stalls, try `YZAPI_UPSTREAM_HTTP2=0` and compare provider by provider | This is a diagnostic switch; first confirm that the link actually negotiated HTTP/2 |

## Locating bottlenecks with logs (field definitions since 1.0.20)

| Field | Meaning |
|---|---|
| `queue_wait_ms` | Time waiting for a concurrency slot; non-zero means queuing, check upstream slowness or account cooldown first, do not enlarge the queue first |
| `first_byte_ms` | Arrival of the response headers of the successful upstream attempt; includes queuing and the earlier failed attempts |
| `first_content_ms` | The first real content / thinking / tool-call delta written to the client; this is the first token as perceived by the user |
| `client_write_ms` | Accumulated time blocked while writing to the client; if high, check the write path (downstream reception, network) and locate it together with the client's event timeline, do not blame the reverse proxy directly |
| attempts | A timeout / 5xx before the success means retries are eating into the first token; look at the specific failure type |

Large `first_content_ms − first_byte_ms`: the upstream starts emitting text only after the response headers (common with reasoning models), unrelated to the gateway. `first_byte_ms − queue_wait_ms` clearly larger than a direct connection to that provider: then look at the request body size and whether the request is cross-protocol.

## Hot-path changes worth making later (evidence first)

- When the protocol is the same, the model name is already the upstream name and no fields need to be injected, pass `req.body` through directly; keep full parsing and authorization checks.
- Skip text extraction when neither compliance nor routing uses it; confirm that auditing, routing and message counts have no other dependency on it.
- Only after that comes fully streamed forwarding of the request body; it touches authentication, model authorization, retries, moderation and the memory budget, so the change is large.
- Temporary buffers on SSE writes and per-token encoding on the conversion path are micro-optimizations and go last.

Editing JSON via string replacement is not recommended, nor is batching tokens to save a flush.

## Do not optimize these yet

gin wrapping, concurrency slots, quota read locks and the snapshot atomic pointer are negligible next to large bodies and upstream latency. The throughput numbers in the README come from a zero-latency mock with small requests and do not represent large-context streaming. MaxConcurrency 512 / queue 1024 is enough for a few IDE sessions.
