#!/usr/bin/env python3
"""Exhaustive admin/user API round-trip check: boots a mock upstream and the gateway on a
temp data dir, then drives every mutating endpoint the way the UI does (create -> read
-> update -> toggle -> delete) and asserts each edit round-trips. Run: python3 scripts/api-crud.py"""
import json, os, subprocess, sys, tempfile, time, urllib.request, urllib.error

PORT = int(os.environ.get("PORT", "18085")); MOCK = os.environ.get("MOCK", "127.0.0.1:19915")
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); os.chdir(ROOT)
DATA = tempfile.mkdtemp(prefix="yzapi-crud-")
BASE = f"http://127.0.0.1:{PORT}"
env = dict(os.environ, YZAPI_DATA_DIR=DATA, YZAPI_LISTEN=f"127.0.0.1:{PORT}", YZAPI_INITIAL_ADMIN_PASSWORD="Admin123456!", YZAPI_DEV="1")
subprocess.check_call(["go", "build", "-o", f"{DATA}/yzapi", "./cmd/yzapi"])
subprocess.check_call(["go", "build", "-o", f"{DATA}/mock", "./tools/mockupstream"])
mock = subprocess.Popen([f"{DATA}/mock", "-addr", MOCK], stdout=open(f"{DATA}/mock.log", "w"), stderr=subprocess.STDOUT)
gw = subprocess.Popen([f"{DATA}/yzapi"], env=env, stdout=open(f"{DATA}/yzapi.log", "w"), stderr=subprocess.STDOUT)

TOKEN = None; PASSED = 0; FAILED = []
def req(method, path, body=None, token=None, expect=None, raw=False, headers=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(BASE + path, data=data, method=method)
    r.add_header("Content-Type", "application/json")
    for hk, hv in (headers or {}).items(): r.add_header(hk, hv)
    if token or TOKEN: r.add_header("Authorization", "Bearer " + (token or TOKEN))
    try:
        with urllib.request.urlopen(r, timeout=30) as resp:
            status, text = resp.status, resp.read().decode()
    except urllib.error.HTTPError as e:
        status, text = e.code, e.read().decode()
    try: js = json.loads(text) if text else None
    except Exception: js = text
    if expect is not None and status not in (expect if isinstance(expect, tuple) else (expect,)):
        raise AssertionError(f"{method} {path} -> {status} {text[:300]} (want {expect})")
    return (status, js) if raw else js
def check(name, fn):
    global PASSED
    try:
        fn(); PASSED += 1; print(f"  ✔ {name}")
    except Exception as e:
        FAILED.append(name); print(f"  ✘ {name}: {e}")
def eq(a, b, what=""):
    if a != b: raise AssertionError(f"{what} got {a!r} want {b!r}")

try:
    for _ in range(100):
        try: urllib.request.urlopen(BASE + "/health/ready", timeout=1); break
        except Exception: time.sleep(0.2)
    TOKEN = req("POST", "/api/auth/login", {"username": "admin", "password": "Admin123456!"}, expect=200)["token"]
    req("POST", "/api/auth/change-password", {"old_password": "Admin123456!", "new_password": "Admin123456!x"}, expect=200)
    TOKEN = req("POST", "/api/auth/login", {"username": "admin", "password": "Admin123456!x"}, expect=200)["token"]

    # ---------- accounts ----------
    acc_body = {"name": "mock-a", "provider": "custom", "type": "text", "base_url": f"http://{MOCK}/v1", "api_key": "sk-mock",
                "protocols": ["openai-completions", "openai-responses"],
                "mappings": [{"request_model": "mini", "upstream_model": "mock-mini"}], "priority": 10, "max_concurrency": 5, "note": "n1"}
    acc = req("POST", "/api/admin/accounts", acc_body, expect=200); AID = acc["id"]
    def t_acc_update():
        b = dict(acc_body, name="mock-a2", note="n2", priority=3, max_concurrency=7, api_key="******",
                 protocols=["openai-completions"], mappings=[{"request_model": "mini", "upstream_model": "mock-mini"}, {"request_model": "pro", "upstream_model": "mock-pro"}], skip_test=True)
        a = req("PUT", f"/api/admin/accounts/{AID}", b, expect=200)
        eq(a["name"], "mock-a2"); eq(a["note"], "n2"); eq(a["priority"], 3); eq(a["max_concurrency"], 7); eq(a["protocols"], ["openai-completions"])
        eq(sorted(m["request_model"] for m in a["mappings"]), ["mini", "pro"])
        g = req("GET", f"/api/admin/accounts/{AID}", expect=200); eq(g["name"], "mock-a2"); eq(g["has_key"], True)
    check("account update round-trips every field", t_acc_update)
    def t_acc_key_change():
        b = dict(acc_body, api_key="sk-mock-second-key-0001", skip_test=True)
        a = req("PUT", f"/api/admin/accounts/{AID}", b, expect=200); eq(a["api_key_masked"], "sk-moc…0001")
        a = req("PUT", f"/api/admin/accounts/{AID}", dict(b, api_key="******"), expect=200); eq(a["api_key_masked"], "sk-moc…0001", "masked value must keep the key")
    check("account api key change", t_acc_key_change)
    def t_acc_toggle():
        eq(req("PATCH", f"/api/admin/accounts/{AID}/enabled", {"enabled": False}, expect=200)["enabled"], False)
        eq(req("GET", f"/api/admin/accounts/{AID}", expect=200)["enabled"], False)
        eq(req("PATCH", f"/api/admin/accounts/{AID}/enabled", {"enabled": True}, expect=200)["enabled"], True)
    check("account enable toggle persists", t_acc_toggle)
    check("account reset-health", lambda: req("POST", f"/api/admin/accounts/{AID}/reset-health", {}, expect=200))
    check("account test-model", lambda: eq(req("POST", f"/api/admin/accounts/{AID}/test-model", {"model": "mock-mini"}, expect=200)["ok"], True))
    check("account mappings replace", lambda: eq(len(req("PUT", f"/api/admin/accounts/{AID}/mappings", {"mappings": [{"request_model": "solo", "upstream_model": "mock-mini"}]}, expect=200)["mappings"]), 1))
    check("account mappings duplicate rejected", lambda: req("PUT", f"/api/admin/accounts/{AID}/mappings", {"mappings": [{"request_model": "x", "upstream_model": "a"}, {"request_model": "x", "upstream_model": "b"}]}, expect=400))
    check("account discover with stored key", lambda: req("POST", "/api/admin/accounts/discover", {"provider": "custom", "base_url": f"http://{MOCK}/v1", "api_key": "", "account_id": AID}, expect=200)["models"])
    check("account test with stored key", lambda: eq(req("POST", "/api/admin/accounts/test", dict(acc_body, api_key="", account_id=AID), expect=200)["ok"], True))
    check("account list filters", lambda: eq(req("GET", "/api/admin/accounts?provider=custom&type=text&q=mock", expect=200)["total"], 1))
    # LIKE searches must treat _ and % literally (SQLite needs an explicit ESCAPE clause).
    def t_like_escape():
        cur = req("GET", f"/api/admin/accounts/{AID}", expect=200)
        req("PUT", f"/api/admin/accounts/{AID}", dict(acc_body, note="note_with_underscore 50%", api_key="******", skip_test=True,
                                                     mappings=[{"request_model": m["request_model"], "upstream_model": m["upstream_model"]} for m in cur["mappings"]]), expect=200)
        eq(req("GET", "/api/admin/accounts?q=note_with", expect=200)["total"], 1, "underscore search")
        eq(req("GET", "/api/admin/accounts?q=50%25", expect=200)["total"], 1, "percent search")
        eq(req("GET", "/api/admin/accounts?q=notexwith", expect=200)["total"], 0, "underscore must not act as wildcard")
    check("search escapes _ and %", t_like_escape)
    check("enable toggle on missing id -> 404", lambda: req("PATCH", "/api/admin/accounts/999999/enabled", {"enabled": True}, expect=404))
    check("reset-health on missing id -> 404", lambda: req("POST", "/api/admin/accounts/999999/reset-health", {}, expect=404))
    emb = req("POST", "/api/admin/accounts", {"name": "emb", "provider": "custom", "type": "embedding", "base_url": f"http://{MOCK}/v1", "api_key": "sk-mock",
                                                "mappings": [{"request_model": "embed", "upstream_model": "mock-embed"}]}, expect=200)
    check("embedding account update keeps type", lambda: eq(req("PUT", f"/api/admin/accounts/{emb['id']}", {"name": "emb2", "provider": "custom", "type": "text", "base_url": f"http://{MOCK}/v1", "api_key": "******", "mappings": [{"request_model": "embed", "upstream_model": "mock-embed"}], "skip_test": True}, expect=200)["type"], "embedding"))

    # ---------- model groups ----------
    mg = req("POST", "/api/admin/model-groups", {"name": "g1", "type": "text", "models": ["solo"], "note": "x"}, expect=200); MG = mg["id"]
    check("model group update", lambda: eq(req("PUT", f"/api/admin/model-groups/{MG}", {"name": "g1b", "type": "text", "models": ["solo", "pro"], "note": "y"}, expect=200)["models"], ["solo", "pro"]))
    check("model group duplicate name -> 4xx", lambda: (req("POST", "/api/admin/model-groups", {"name": "g1b", "type": "text", "models": ["solo"]}, expect=(400, 409))))
    check("model group list filter", lambda: eq(req("GET", "/api/admin/model-groups?type=text&q=g1", expect=200)["total"], 1))
    check("model group delete missing id -> 404", lambda: req("DELETE", "/api/admin/model-groups/999999", expect=404))
    check("users list carries is_last_admin", lambda: eq([u["is_last_admin"] for u in req("GET", "/api/admin/users?role=admin", expect=200)["items"]], [True]))

    # ---------- user groups ----------
    ug = req("POST", "/api/admin/user-groups", {"name": "team", "max_concurrency": 5, "key_max_concurrency": 2, "token_quota": 1000, "model_group_ids": [MG], "enabled": True, "note": "t"}, expect=200); UG = ug["id"]
    def t_ug_update():
        u = req("PUT", f"/api/admin/user-groups/{UG}", {"name": "team2", "max_concurrency": 6, "key_max_concurrency": 3, "token_quota": 0, "model_group_ids": [], "enabled": True, "note": "t2"}, expect=200)
        eq(u["name"], "team2"); eq(u["max_concurrency"], 6); eq(u["token_quota"], 0); eq(u["model_group_ids"], [])
        u = req("PUT", f"/api/admin/user-groups/{UG}", {"name": "team2", "max_concurrency": 6, "key_max_concurrency": 3, "token_quota": 0, "model_group_ids": [MG], "enabled": True, "note": "t2"}, expect=200)
        eq(u["model_group_ids"], [MG])
    check("user group update incl. clearing and re-adding model groups", t_ug_update)
    def t_ug_toggle():
        eq(req("PATCH", f"/api/admin/user-groups/{UG}/enabled", {"enabled": False}, expect=200)["enabled"], False)
        eq(req("GET", f"/api/admin/user-groups/{UG}", expect=200)["enabled"], False)
        req("PATCH", f"/api/admin/user-groups/{UG}/enabled", {"enabled": True}, expect=200)
    check("user group toggle persists", t_ug_toggle)
    def t_mg_delete_cascade():
        mg3 = req("POST", "/api/admin/model-groups", {"name": "tmp", "type": "text", "models": ["solo"]}, expect=200)
        req("PUT", f"/api/admin/user-groups/{UG}", {"name": "team2", "max_concurrency": 6, "key_max_concurrency": 3, "token_quota": 0, "model_group_ids": [MG, mg3["id"]], "enabled": True, "note": "t2"}, expect=200)
        req("DELETE", f"/api/admin/model-groups/{mg3['id']}", expect=200)  # allowed by design; reference must be dropped
        eq(req("GET", f"/api/admin/user-groups/{UG}", expect=200)["model_group_ids"], [MG])
    check("model group delete drops user-group references", t_mg_delete_cascade)
    default_gid = [g for g in req("GET", "/api/admin/user-groups", expect=200)["items"] if g["is_default"]][0]["id"]
    check("default group delete -> 409", lambda: req("DELETE", f"/api/admin/user-groups/{default_gid}", expect=409))

    # ---------- users ----------
    us = req("POST", "/api/admin/users", {"username": "bob", "password": "BobPass123456", "group_id": UG, "role": "user", "note": "b"}, expect=200); UID = us["id"]
    def t_user_update():
        u = req("PUT", f"/api/admin/users/{UID}", {"group_id": default_gid, "role": "user", "note": "b2"}, expect=200)
        eq(u["group_id"], default_gid); eq(u["note"], "b2")
        u = req("PUT", f"/api/admin/users/{UID}", {"group_id": UG, "role": "user", "note": "b2"}, expect=200); eq(u["group_id"], UG)
    check("user update group/note", t_user_update)
    def t_user_toggle():
        eq(req("PATCH", f"/api/admin/users/{UID}/enabled", {"enabled": False}, expect=200)["enabled"], False)
        eq(req("GET", f"/api/admin/users/{UID}", expect=200)["enabled"], False)
        req("PATCH", f"/api/admin/users/{UID}/enabled", {"enabled": True}, expect=200)
    check("user toggle persists", t_user_toggle)
    check("user reset password", lambda: req("POST", f"/api/admin/users/{UID}/reset-password", {"password": "BobPass654321"}, expect=200))
    def t_user_login_after_reset():
        st, js = req("POST", "/api/auth/login", {"username": "bob", "password": "BobPass654321"}, token="", raw=True)
        eq(st, 200); eq(js["user"]["must_change_password"], True)
    check("user login after admin reset requires password change", t_user_login_after_reset)
    check("user unlock", lambda: req("POST", f"/api/admin/users/{UID}/unlock", {}, expect=200))
    check("user unlock missing id -> 404", lambda: req("POST", "/api/admin/users/999999/unlock", {}, expect=404))
    check("duplicate username -> 4xx", lambda: req("POST", "/api/admin/users", {"username": "bob", "password": "BobPass123456", "group_id": UG, "role": "user"}, expect=(400, 409)))
    admin_id = req("GET", "/api/auth/me", expect=200)["id"]
    check("last admin demote -> 409", lambda: req("PUT", f"/api/admin/users/{admin_id}", {"group_id": default_gid, "role": "user", "note": ""}, expect=409))
    check("last admin disable -> 409", lambda: req("PATCH", f"/api/admin/users/{admin_id}/enabled", {"enabled": False}, expect=409))
    check("last admin delete -> 409", lambda: req("DELETE", f"/api/admin/users/{admin_id}", expect=409))
    check("user group with members delete -> 409", lambda: req("DELETE", f"/api/admin/user-groups/{UG}", expect=409))

    # ---------- user center as bob ----------
    BT = req("POST", "/api/auth/login", {"username": "bob", "password": "BobPass654321"}, token="", expect=200)["token"]
    req("POST", "/api/auth/change-password", {"old_password": "BobPass654321", "new_password": "BobPass999999"}, token=BT, expect=200)
    BT = req("POST", "/api/auth/login", {"username": "bob", "password": "BobPass999999"}, token="", expect=200)["token"]
    k = req("POST", "/api/user/keys", {"name": "k1"}, token=BT, expect=200); KID = k["item"]["id"]; KEY = k["key"]
    check("key rename", lambda: eq(req("PUT", f"/api/user/keys/{KID}", {"name": "k2"}, token=BT, expect=200)["name"], "k2"))
    def t_key_toggle():
        eq(req("PATCH", f"/api/user/keys/{KID}/enabled", {"enabled": False}, token=BT, expect=200)["enabled"], False)
        eq([x for x in req("GET", "/api/user/keys", token=BT, expect=200) if x["id"] == KID][0]["enabled"], False)
        req("PATCH", f"/api/user/keys/{KID}/enabled", {"enabled": True}, token=BT, expect=200)
    check("key toggle persists", t_key_toggle)
    # bob's group allows model group g1b (models: solo, pro); "pro" has no mapping any more, and the
    # model group itself is callable as a model of kind "group"; "embed" must NOT be visible.
    check("user models scoped to group", lambda: eq(sorted(m["name"] for m in req("GET", "/api/user/models", token=BT, expect=200)["models"]), ["g1b", "solo"]))
    check("user group view", lambda: eq(req("GET", "/api/user/group", token=BT, expect=200)["name"], "team2"))
    check("user cannot call admin api", lambda: req("GET", "/api/admin/users", token=BT, expect=403))
    def t_chat():
        st, js = req("POST", "/v1/chat/completions", {"model": "solo", "messages": [{"role": "user", "content": "hi"}]}, token=KEY, raw=True)
        eq(st, 200, "chat status"); eq(js["choices"][0]["message"]["role"], "assistant")
    check("data plane call with bob's key", t_chat)
    check("user cannot see other users' keys", lambda: req("DELETE", f"/api/user/keys/999999", token=BT, expect=404))
    # ---------- client compatibility surface ----------
    def t_compat():
        pt = req("POST", "/api/admin/accounts", {"name": "any-model", "provider": "custom", "type": "text", "base_url": f"http://{MOCK}/v1", "api_key": "sk-mock",
                                                   "protocols": ["openai-completions"], "mappings": [], "passthrough_models": True, "priority": 50, "skip_test": True}, expect=200)
        eq(pt["passthrough_models"], True)
        # bob's group is allow-listed (model group g1b) so pass-through must be refused for him...
        st, _ = req("POST", "/v1/chat/completions", {"model": "totally-unknown-model", "messages": [{"role": "user", "content": "hi"}]}, token=KEY, raw=True); eq(st, 403, "allow-listed group")
        # ...but the admin's default group (no allow-list) can use any name through the pass-through account.
        ak = req("POST", "/api/user/keys", {"name": "admin-key"}, expect=200)["key"]
        st, js = req("POST", "/v1/chat/completions", {"model": "totally-unknown-model", "messages": [{"role": "user", "content": "hi"}]}, token=ak, raw=True); eq(st, 200, "passthrough chat")
        st, js = req("POST", "/v1/chat/completions", {"model": "SOLO", "messages": [{"role": "user", "content": "hi"}]}, token=KEY, raw=True); eq(st, 200, "case-insensitive model name")
        st, js = req("POST", "/v1/messages/count_tokens", {"model": "solo", "messages": [{"role": "user", "content": "hello world"}]}, token=KEY, raw=True); eq(st, 200, "count_tokens"); eq(js["input_tokens"] > 0, True)
        st, js = req("GET", "/v1/models/solo", token=KEY, raw=True); eq(st, 200, "model by id"); eq(js["id"], "solo"); eq(js["display_name"], "solo")
        r = urllib.request.Request(BASE + "/v1/models", method="GET"); r.add_header("api-key", KEY)
        with urllib.request.urlopen(r, timeout=10) as resp: eq(resp.status, 200, "api-key header")
        r = urllib.request.Request(BASE + "/v1/chat/completions", method="OPTIONS"); r.add_header("Origin", "https://ide.example"); r.add_header("Access-Control-Request-Method", "POST")
        with urllib.request.urlopen(r, timeout=10) as resp: eq(resp.status, 204, "cors preflight"); eq(resp.headers.get("Access-Control-Allow-Origin"), "*")
        req("DELETE", f"/api/admin/accounts/{pt['id']}", expect=200)
    check("client compatibility: passthrough, case-insensitive names, count_tokens, model by id, api-key, CORS", t_compat)
    def t_key_restrictions():
        exp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 86400))
        k = req("POST", "/api/user/keys", {"name": "restricted", "expires_at": exp, "allowed_models": ["solo"], "tokens_per_minute": 100000, "requests_per_minute": 2}, token=BT, expect=200)
        eq(k["item"]["allowed_models"], ["solo"]); eq(k["item"]["requests_per_minute"], 2); eq(k["item"]["expired"], False)
        req("POST", "/api/user/keys", {"name": "bad", "allowed_models": ["not-visible-model"]}, token=BT, expect=400)
        req("POST", "/api/user/keys", {"name": "bad", "expires_at": "2000-01-01T00:00:00Z"}, token=BT, expect=400)
        for _ in range(2):
            req("POST", "/v1/chat/completions", {"model": "solo", "messages": [{"role": "user", "content": "hi"}]}, token=k["key"], expect=200)
        st, js = req("POST", "/v1/chat/completions", {"model": "solo", "messages": [{"role": "user", "content": "hi"}]}, token=k["key"], raw=True)
        eq(st, 429, "key rpm"); eq(js["error"]["code"], "rate_limited")
        st, js = req("POST", "/v1/chat/completions", {"model": "g1b", "messages": [{"role": "user", "content": "hi"}]}, token=k["key"], raw=True)
        eq(st, 403, "key whitelist")
        upd = req("PUT", f"/api/user/keys/{k['item']['id']}", {"name": "restricted2", "expires_at": None, "allowed_models": [], "tokens_per_minute": 0, "requests_per_minute": 0}, token=BT, expect=200)
        eq(upd["expires_at"], None); eq(upd["allowed_models"], [])
        req("DELETE", f"/api/user/keys/{k['item']['id']}", token=BT, expect=200)
    check("key expiry / whitelist / per-minute limits", t_key_restrictions)
    def t_group_limits():
        body = lambda tpm: {"name": "team2", "max_concurrency": 6, "key_max_concurrency": 3, "token_quota": 0, "tokens_per_minute": tpm, "requests_per_minute": 0, "model_group_ids": [MG], "enabled": True, "note": "t2"}
        try:
            # Earlier calls in this run already booked >5 tokens for the group within the window,
            # so a 5-token limit refuses immediately; lifting it admits again.
            eq(req("PUT", f"/api/admin/user-groups/{UG}", body(5), expect=200)["tokens_per_minute"], 5)
            st, js = req("POST", "/v1/chat/completions", {"model": "solo", "messages": [{"role": "user", "content": "hi"}]}, token=KEY, raw=True)
            eq(st, 429, "group tpm"); eq(js["error"]["code"], "token_rate_limited")
            req("PUT", f"/api/admin/user-groups/{UG}", body(-1), expect=400)
        finally:
            req("PUT", f"/api/admin/user-groups/{UG}", body(0), expect=200)
        req("POST", "/v1/chat/completions", {"model": "solo", "messages": [{"role": "user", "content": "hi"}]}, token=KEY, expect=200)
    check("group per-minute token limit", t_group_limits)
    check("account cache self-check runs against mock", lambda: eq(req("POST", f"/api/admin/accounts/{AID}/cache-check", {"model": "mock-mini"}, expect=200)["ok"], True))
    check("key delete", lambda: req("DELETE", f"/api/user/keys/{KID}", token=BT, expect=200))

    # ---------- settings ----------
    st0 = req("GET", "/api/admin/settings", expect=200)
    def t_basic():
        b = dict(st0["basic"], site_name="My GW", log_retention_days=45, base_url=f"http://127.0.0.1:{PORT}/v1", protocol_conversion=True)
        r = req("PUT", "/api/admin/settings/basic", b, expect=200)
        s = req("GET", "/api/admin/settings", expect=200)["basic"]; eq(s["site_name"], "My GW"); eq(s["log_retention_days"], 45)
        eq(req("GET", "/api/public/info", token="", expect=200)["site_name"], "My GW")
    check("settings basic round-trip + public info", t_basic)
    check("settings basic invalid base_url -> 400", lambda: req("PUT", "/api/admin/settings/basic", dict(st0["basic"], base_url="ftp://x"), expect=400))
    def t_perf():
        p = dict(st0["performance"], max_concurrency=77, queue_size=11, max_retries=2)
        req("PUT", "/api/admin/settings/performance", p, expect=200)
        s = req("GET", "/api/admin/settings", expect=200)["performance"]; eq(s["max_concurrency"], 77); eq(s["queue_size"], 11)
    check("settings performance round-trip", t_perf)
    def t_vector():
        req("PUT", "/api/admin/settings/vector", {"account_id": emb["id"], "model": "mock-embed"}, expect=200)
        r = req("POST", "/api/admin/settings/vector/test", {"account_id": emb["id"], "model": "mock-embed"}, expect=200); eq(r["ok"], True)
        eq(req("GET", "/api/admin/settings", expect=200)["vector"]["account_id"], emb["id"])
    check("settings vector set + test", t_vector)
    check("vector account delete -> 409", lambda: req("DELETE", f"/api/admin/accounts/{emb['id']}", expect=409))
    def t_smart():
        mg2 = req("POST", "/api/admin/model-groups", {"name": "g2", "type": "text", "models": ["pro"]}, expect=200)
        req("PUT", "/api/admin/settings/smart_route", {"enabled": True, "virtual_model": "auto", "simple_group_id": MG, "complex_group_id": mg2["id"], "threshold": 0.7, "confidence_gap": 0.1, "top_k": 5}, expect=200)
        eq(req("GET", f"/api/admin/model-groups/{MG}", expect=200)["in_use_by_route"], True)
        req("DELETE", f"/api/admin/model-groups/{mg2['id']}", expect=409)
        req("PUT", "/api/admin/settings/smart_route", {"enabled": False, "virtual_model": "auto", "simple_group_id": 0, "complex_group_id": 0, "threshold": 0.7, "confidence_gap": 0.1, "top_k": 5}, expect=200)
        req("DELETE", f"/api/admin/model-groups/{mg2['id']}", expect=200)
    check("smart route settings + model group protection", t_smart)
    def t_comp():
        req("PUT", "/api/admin/settings/compliance", {"enabled": True, "semantic_threshold": 0.8, "check_system_prompt": True, "on_failure": "allow"}, expect=200)
        eq(req("GET", "/api/admin/settings", expect=200)["compliance"]["on_failure"], "allow")
        req("PUT", "/api/admin/settings/compliance", {"enabled": False, "semantic_threshold": 0.8, "check_system_prompt": False, "on_failure": "block"}, expect=200)
    check("compliance settings round-trip", t_comp)
    def t_es():
        req("PUT", "/api/admin/settings/elasticsearch", {"enabled": False, "url": "http://127.0.0.1:9", "auth_type": "basic", "api_key": "", "username": "u", "password": "p", "index_prefix": "yz", "request_kb": 64, "response_kb": 64, "retention_days": 7}, expect=200)
        s = req("GET", "/api/admin/settings", expect=200)["elasticsearch"]; eq(s["password"], "******")
        req("PUT", "/api/admin/settings/elasticsearch", dict(s, password="******"), expect=200)  # keep masked secret
        r = req("POST", "/api/admin/settings/elasticsearch/test", dict(s, password="******"), expect=200); eq(r["ok"], False)
        req("GET", "/api/admin/settings/elasticsearch/status", expect=200)
    check("elasticsearch settings mask/keep + test failure is graceful", t_es)

    # ---------- pricing ----------
    def t_prices():
        lst = req("GET", "/api/admin/prices", expect=200)
        eq(lst["total"] > 30, True, "builtin prices seeded")
        p = req("POST", "/api/admin/prices", {"pattern": "mock-mini", "provider": "custom", "input_per_m": 1, "output_per_m": 2, "cached_input_per_m": 0.5, "currency": "USD"}, expect=200)
        eq(req("PUT", f"/api/admin/prices/{p['id']}", {"pattern": "mock-mini", "provider": "custom", "input_per_m": 1.5, "output_per_m": 2, "cached_input_per_m": 0.5, "currency": "USD"}, expect=200)["input_per_m"], 1.5)
        eq(req("GET", "/api/admin/prices/lookup?provider=custom&model=mock-mini-2026", expect=200)["found"], True)
        req("POST", "/api/admin/prices", {"pattern": "", "currency": "USD"}, expect=400)
        req("PUT", "/api/admin/settings/pricing", {"currency": "USD", "usd_to_cny": 7.1}, expect=200)
        eq(req("GET", "/api/public/info", token="", expect=200)["currency"], "USD")
        req("PUT", "/api/admin/settings/pricing", {"currency": "EUR", "usd_to_cny": 7.1}, expect=400)
        # a priced call shows a cost in logs and reports
        k2 = req("POST", "/api/user/keys", {"name": "k-cost"}, token=BT, expect=200)["key"]
        req("POST", "/v1/chat/completions", {"model": "solo", "messages": [{"role": "user", "content": "cost"}]}, token=k2, expect=200)
        time.sleep(1.5)
        lg = req("GET", "/api/admin/logs?range=24h", expect=200)["items"][0]
        eq(lg["cost_known"], True, "cost known"); eq(lg["cost_micros"] > 0, True, "cost > 0")
        eq(abs(lg["cost"] - lg["cost_micros"] / 1e6) < 1e-9, True, "admin log cost is the USD display of the USD ledger")
        usd = req("GET", "/api/admin/usage?range=24h", expect=200)["summary"]["cost"]
        eq(usd > 0, True, "report cost")
        # R112-02: switching the display currency converts history, never relabels it
        req("PUT", "/api/admin/settings/pricing", {"currency": "CNY", "usd_to_cny": 7.1}, expect=200)
        rep = req("GET", "/api/admin/usage?range=24h", expect=200)
        eq(rep["currency"], "CNY"); eq(abs(rep["summary"]["cost"] - usd * 7.1) < 1e-6, True, "1 USD of history displays as 7.1 CNY")
        eq(abs(req("GET", "/api/admin/logs?range=24h", expect=200)["items"][0]["cost"] - lg["cost"] * 7.1) < 1e-6, True, "log cost converts too")
        req("PUT", "/api/admin/settings/pricing", {"currency": "USD", "usd_to_cny": 7.1}, expect=200)
        req("POST", "/api/admin/prices/reset-builtin", {}, expect=200)
        eq(req("GET", "/api/admin/prices/lookup?provider=custom&model=mock-mini", expect=200)["found"], True, "custom row survives reset")
        req("DELETE", f"/api/admin/prices/{p['id']}", expect=200)
        req("DELETE", f"/api/admin/prices/{p['id']}", expect=404)
    check("price table CRUD, lookup, settings and cost in logs/report", t_prices)

    def t_price_import():
        import io, urllib.request
        cat = {"schemaVersion": 1, "updatedAt": "2026-09-07", "models": [
            {"id": "import-test-model", "inputPer1M": 1.5, "outputPer1M": 6, "cacheReadPer1M": 0.15, "cacheCreationPer1M": 0},
            {"id": "mock-mini", "inputPer1M": 9, "outputPer1M": 9},
            {"id": "negative-price", "inputPer1M": -1, "outputPer1M": 2}]}
        body = json.dumps(cat).encode()
        boundary = "----yzapicrud"
        def multipart(apply):
            parts = []
            for k, v in (("apply", "1" if apply else "0"), ("overwrite_edited", "0")):
                parts.append(f"--{boundary}\r\nContent-Disposition: form-data; name=\"{k}\"\r\n\r\n{v}\r\n".encode())
            parts.append(f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"prices.json\"\r\nContent-Type: application/json\r\n\r\n".encode() + body + b"\r\n")
            parts.append(f"--{boundary}--\r\n".encode())
            return b"".join(parts)
        def upload(apply, expect=200):
            rq = urllib.request.Request(f"{BASE}/api/admin/prices/import-file", data=multipart(apply), method="POST")
            rq.add_header("Content-Type", f"multipart/form-data; boundary={boundary}")
            rq.add_header("Authorization", f"Bearer {TOKEN}")
            try:
                with urllib.request.urlopen(rq, timeout=30) as r:
                    eq(r.status, expect, "upload status"); return json.loads(r.read())
            except urllib.error.HTTPError as e:
                eq(e.code, expect, "upload status"); return None
        # a manual generic row for mock-mini: the import must keep it (not overwrite) unless overwrite_edited is set
        manual = req("POST", "/api/admin/prices", {"pattern": "mock-mini", "provider": "", "input_per_m": 1, "output_per_m": 2, "currency": "USD"}, expect=200)
        # R126-03: the (provider, pattern) key is unique (case-insensitively) -> 409
        req("POST", "/api/admin/prices", {"pattern": "Mock-Mini", "provider": "", "input_per_m": 3, "output_per_m": 3, "currency": "USD"}, expect=409)
        snaps0 = req("GET", "/api/admin/config/snapshots", expect=200)["total"]
        pv = upload(False)
        eq(pv["applied"], False); eq(pv["plan"]["source"], "easycpa"); eq(pv["plan"]["new"] >= 1, True, "preview lists new rows")
        eq(pv["plan"]["kept"], 1, "manual row listed as kept")
        eq(pv["plan"]["invalid"], 1, "negative price row rejected (R126-04)"); eq(len(pv["plan"]["invalid_rows"]), 1)
        eq(bool(pv.get("plan_id")) and len(pv.get("sha256", "")) == 64, True, "preview is bound to a plan id + sha (R126-02)")
        eq(req("GET", "/api/admin/prices/lookup?model=import-test-model", expect=200)["found"], False, "preview writes nothing")
        eq(req("GET", "/api/admin/config/snapshots", expect=200)["total"], snaps0, "preview takes no config snapshot (R126-07)")
        # R126-02: the old direct apply is refused; apply needs the plan id; a wrong sha is refused
        upload(True, expect=400)
        req("POST", "/api/admin/prices/import/apply", {"plan_id": pv["plan_id"], "sha256": "0" * 64}, expect=409)
        ap = req("POST", "/api/admin/prices/import/apply", {"plan_id": pv["plan_id"], "sha256": pv["sha256"]}, expect=200)
        eq(ap["applied"], True); eq(ap["plan"]["new"], pv["plan"]["new"], "apply reports the in-transaction counts")
        eq(req("GET", "/api/admin/config/snapshots", expect=200)["total"], min(snaps0 + 1, 50), "apply takes exactly one snapshot")
        req("POST", "/api/admin/prices/import/apply", {"plan_id": pv["plan_id"]}, expect=409)  # single use
        got = req("GET", "/api/admin/prices/lookup?provider=custom&model=import-test-model", expect=200)
        eq(got["found"], True, "imported row prices"); eq(got["price"]["source"], "easycpa"); eq(got["price"]["input_per_m"], 1.5)
        eq(req("GET", "/api/admin/prices/lookup?model=negative-price", expect=200)["found"], False, "invalid row never written")
        kept = req("GET", "/api/admin/prices/lookup?model=mock-mini", expect=200)["price"]
        eq((kept["id"], kept["input_per_m"], kept["source"]), (manual["id"], 1, ""), "manual row kept by import")
        eq(upload(False)["plan"]["same"] >= 1, True, "second preview reports unchanged rows")
        # R126-08: a CNY built-in row against a USD catalog is kept by default and the change carries both currencies
        lite = {"dashscope/qwen3.8-max": {"litellm_provider": "dashscope", "mode": "chat", "input_cost_per_token": 2e-06, "output_cost_per_token": 6e-06}}
        body = json.dumps(lite).encode()
        pc = upload(False)
        ch = next(c for c in pc["plan"]["changes"] if c["pattern"] == "qwen3.8-max")
        eq((ch["action"], ch["reason"], ch["old_currency"], ch["new_currency"], ch["currency_changed"]), ("keep", "currency", "CNY", "USD", True), "currency-differing built-in kept and labelled")
        body = json.dumps(cat).encode()
        req("DELETE", f"/api/admin/prices/{manual['id']}", expect=200)
        req("POST", "/api/admin/prices/import", {"source": "url", "url": "not-a-url", "apply": False}, expect=400)
        # log carries the detected client
        k3 = req("POST", "/api/user/keys", {"name": "k-client"}, token=BT, expect=200)["key"]
        req("POST", "/v1/chat/completions", {"model": "solo", "messages": [{"role": "user", "content": "client"}]}, token=k3, expect=200, headers={"User-Agent": "claude-cli/2.1.4 (external, cli)"})
        time.sleep(1.5)
        lg = req("GET", "/api/admin/logs?range=24h&client=claude-code", expect=200)["items"]
        eq(len(lg) >= 1 and lg[0]["client"] == "claude-code", True, "client detected and filterable")
        eq("claude-code" in req("GET", "/api/admin/logs/filters", expect=200)["clients"], True, "client in filter options")
        rep = req("GET", "/api/admin/usage?range=24h", expect=200)
        eq(any(d["name"] == "claude-code" for d in rep.get("by_client", [])), True, "usage by client")
    check("price catalog import (upload, preview/apply) and client detection", t_price_import)

    # ---------- config snapshots ----------
    def t_snapshots():
        before = req("GET", "/api/admin/config/snapshots", expect=200)["total"]
        req("POST", "/api/admin/config/snapshots", {"reason": "crud-manual"}, expect=200)
        lst = req("GET", "/api/admin/config/snapshots", expect=200)
        eq(lst["total"], min(before + 1, 50), "snapshot count (capped at 50)"); sid = lst["items"][0]["id"]; eq(lst["items"][0]["reason"], "crud-manual")
        before = lst["total"] - 1
        det = req("GET", f"/api/admin/config/snapshots/{sid}", expect=200)
        eq(any(a["name"] == "mock-a2" or a["name"] == "mock-a" for a in det["accounts"]), True, "snapshot lists accounts")
        # a mutating change auto-snapshots first
        req("PUT", "/api/admin/settings/basic", dict(st0["basic"], site_name="Snap GW", base_url=f"http://127.0.0.1:{PORT}/v1"), expect=200)
        eq(req("GET", "/api/admin/config/snapshots", expect=200)["total"], min(before + 2, 50), "auto snapshot before change")
        res = req("POST", f"/api/admin/config/snapshots/{sid}/restore", {}, expect=200)
        eq(res.get("missing_keys") or [], [], "restored accounts keep their keys")
        eq(req("GET", "/api/admin/settings", expect=200)["basic"]["site_name"], "My GW", "settings restored")
        # R112-01: a restored account still authenticates against the upstream
        acc_after = next(a for a in req("GET", "/api/admin/accounts", expect=200)["items"] if a["name"] in ("mock-a", "mock-a2"))
        probe = {k: acc_after[k] for k in ("name", "provider", "account_type", "type", "base_url", "protocols", "mappings") if k in acc_after}
        probe.update({"account_id": acc_after["id"], "api_key": "******"})
        eq(req("POST", "/api/admin/accounts/test", probe, expect=200)["ok"], True, "restored account passes the live probe with its stored key")
        # R112-05: an empty price set is restored as empty
        for row in req("GET", "/api/admin/prices", expect=200)["items"]:
            req("DELETE", f"/api/admin/prices/{row['id']}", expect=200)
        req("POST", "/api/admin/config/snapshots", {"reason": "empty-prices"}, expect=200)
        sid_empty = req("GET", "/api/admin/config/snapshots", expect=200)["items"][0]["id"]
        req("POST", "/api/admin/prices", {"pattern": "later", "provider": "custom", "input_per_m": 1, "output_per_m": 1, "currency": "USD"}, expect=200)
        req("POST", f"/api/admin/config/snapshots/{sid_empty}/restore", {}, expect=200)
        eq(req("GET", "/api/admin/prices", expect=200)["total"], 0, "empty price set restored")
        req("POST", "/api/admin/prices/reset-builtin", {}, expect=200)
        req("POST", "/api/admin/config/snapshots/999999/restore", {}, expect=404)
    check("config snapshots: manual, auto-before-change, restore", t_snapshots)

    # ---------- compliance resources ----------
    pg = req("POST", "/api/admin/compliance/policy-groups", {"name": "pg", "action": "block", "risk_level": "high", "enabled": True, "description": "d"}, expect=200); PG = pg["id"]
    check("policy group update", lambda: eq(req("PUT", f"/api/admin/compliance/policy-groups/{PG}", {"name": "pg2", "action": "audit", "risk_level": "low", "enabled": True, "description": "d2"}, expect=200)["action"], "audit"))
    check("policy group toggle", lambda: eq(req("PATCH", f"/api/admin/compliance/policy-groups/{PG}/enabled", {"enabled": False}, expect=200)["enabled"], False))
    w = req("POST", "/api/admin/compliance/words", {"policy_group_id": PG, "word": "badword", "note": "", "enabled": True}, expect=200)
    check("word update", lambda: eq(req("PUT", f"/api/admin/compliance/words/{w['id']}", {"policy_group_id": PG, "word": "badword2", "note": "n", "enabled": True}, expect=200)["word"], "badword2"))
    check("word toggle", lambda: eq(req("PATCH", f"/api/admin/compliance/words/{w['id']}/enabled", {"enabled": False}, expect=200)["enabled"], False))
    check("words batch", lambda: eq(req("POST", "/api/admin/compliance/words/batch", {"policy_group_id": PG, "words": ["a1", "a2"]}, expect=200)["created"], 2))
    check("words list filter", lambda: eq(req("GET", f"/api/admin/compliance/words?policy_group_id={PG}&q=a1", expect=200)["total"], 1))
    smp = req("POST", "/api/admin/compliance/samples", {"policy_group_id": PG, "text": "some risky text", "note": "", "enabled": True, "build_vector": True}, expect=200)
    check("compliance sample update", lambda: eq(req("PUT", f"/api/admin/compliance/samples/{smp['id']}", {"policy_group_id": PG, "text": "other text", "note": "n", "enabled": True, "build_vector": True}, expect=200)["text"], "other text"))
    check("compliance samples build all", lambda: req("POST", "/api/admin/compliance/samples/build", {"all": True}, expect=200))
    check("compliance test", lambda: req("POST", "/api/admin/compliance/test", {"text": "badword2 here"}, expect=200))
    check("policy group in use delete -> 409", lambda: req("DELETE", f"/api/admin/compliance/policy-groups/{PG}", expect=409))
    check("compliance audit logs list", lambda: req("GET", "/api/admin/compliance/audit-logs?range=24h", expect=200))

    # ---------- smart route resources ----------
    rs = req("POST", "/api/admin/route/samples", {"label": "simple", "text": "hello", "threshold": 0.8, "note": "", "build_vector": True}, expect=200)
    check("route sample update", lambda: eq(req("PUT", f"/api/admin/route/samples/{rs['id']}", {"label": "complex", "text": "hello2", "threshold": 0.6, "note": "n", "build_vector": True}, expect=200)["label"], "complex"))
    check("route samples batch", lambda: eq(req("POST", "/api/admin/route/samples/batch", {"items": [{"label": "simple", "text": "a"}, {"label": "complex", "text": "b"}], "build_vector": True}, expect=200)["created"], 2))
    check("route samples build", lambda: req("POST", "/api/admin/route/samples/build", {"all": True}, expect=200))
    check("route preview", lambda: req("POST", "/api/admin/route/preview", {"text": "hello"}, expect=200))
    check("route decisions/stats", lambda: (req("GET", "/api/admin/route/decisions?range=24h", expect=200), req("GET", "/api/admin/route/stats?range=24h", expect=200)))
    check("route sample delete", lambda: req("DELETE", f"/api/admin/route/samples/{rs['id']}", expect=200))

    # ---------- logs / usage / overview / system ----------
    time.sleep(1.5)
    check("logs list + filters + detail", lambda: req("GET", f"/api/admin/logs/{req('GET', '/api/admin/logs?range=24h&result=success', expect=200)['items'][0]['id']}", expect=200) and req("GET", "/api/admin/logs/filters", expect=200))
    check("usage report", lambda: req("GET", "/api/admin/usage?range=24h&group_by=model", expect=200)["summary"])
    check("usage rebuild/reconcile/metering", lambda: (req("POST", "/api/admin/usage/rebuild", {"from": time.strftime("%Y-%m-%dT00:00:00Z", time.gmtime()), "to": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())}, expect=200), req("GET", "/api/admin/usage/reconcile?range=24h", expect=200), req("GET", "/api/admin/usage/metering", expect=200)))
    check("overview live/usage", lambda: (req("GET", "/api/admin/overview/live", expect=200), req("GET", "/api/admin/overview/usage?range=7d", expect=200)))
    check("providers/models/system info", lambda: (req("GET", "/api/admin/providers", expect=200), req("GET", "/api/admin/models", expect=200), req("GET", "/api/admin/system/info", expect=200)))
    def gemini_preset():
        provs = req("GET", "/api/admin/providers", expect=200)
        g = next(p for p in provs if p["key"] == "gemini")
        ats = {a["key"]: a for a in g.get("account_types", [])}
        assert ats["native"]["protocols"] == ["gemini-generate"], ats
        assert "gemini-generate" in g["protocols"], g["protocols"]
        return True
    check("gemini preset has native + openai account types", gemini_preset)
    check("user logs/usage", lambda: (eq(req("GET", "/api/user/logs?range=24h&result=success", token=BT, expect=200)["items"][0]["usage_status"], "confirmed"), req("GET", "/api/user/usage?range=24h", token=BT, expect=200)))

    # ---------- deletes in dependency order ----------
    check("word delete", lambda: req("DELETE", f"/api/admin/compliance/words/{w['id']}", expect=200))
    check("compliance sample delete", lambda: req("DELETE", f"/api/admin/compliance/samples/{smp['id']}", expect=200))
    def t_pg_delete():
        for x in req("GET", f"/api/admin/compliance/words?policy_group_id={PG}&page_size=100", expect=200)["items"]:
            req("DELETE", f"/api/admin/compliance/words/{x['id']}", expect=200)
        req("DELETE", f"/api/admin/compliance/policy-groups/{PG}", expect=200)
    check("policy group delete after unreferenced", t_pg_delete)
    check("user delete", lambda: req("DELETE", f"/api/admin/users/{UID}", expect=200))
    check("user group delete after empty", lambda: (req("PUT", f"/api/admin/user-groups/{UG}", {"name": "team2", "max_concurrency": 6, "key_max_concurrency": 3, "token_quota": 0, "model_group_ids": [], "enabled": True, "note": ""}, expect=200), req("DELETE", f"/api/admin/user-groups/{UG}", expect=200)))
    check("model group delete after unreferenced", lambda: req("DELETE", f"/api/admin/model-groups/{MG}", expect=200))
    check("account delete", lambda: req("DELETE", f"/api/admin/accounts/{AID}", expect=200))
    check("vector account released then deleted", lambda: (req("PUT", "/api/admin/settings/vector", {"account_id": 0, "model": ""}, expect=200), req("DELETE", f"/api/admin/accounts/{emb['id']}", expect=200)))
    check("deleted user's token rejected", lambda: req("GET", "/api/auth/me", token=BT, expect=401))
    check("logout invalidates token", lambda: (req("POST", "/api/auth/logout", {}, expect=200), req("GET", "/api/auth/me", expect=401)))
finally:
    gw.terminate(); mock.terminate()
print(f"\n{PASSED} passed, {len(FAILED)} failed; logs in {DATA}")
if FAILED:
    print("FAILED:", *FAILED, sep="\n  ")
    print("--- gateway log tail ---"); print(open(f"{DATA}/yzapi.log").read()[-3000:])
    sys.exit(1)
