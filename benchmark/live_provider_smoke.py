#!/usr/bin/env python3
"""Live provider smoke test.

Boots a 9router-go build against a COPY of the real user DB and issues one
real /v1/chat/completions request per provider, recording the HTTP status,
negotiated protocol, and response shape. Run against two binaries to prove
PGO / transport-pool changes do not alter provider behavior.
"""
import json
import os
import shutil
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

PROXY_PORT = int(os.environ.get("SMOKE_PORT", "20177"))
REAL_DB = os.path.expanduser("~/.9router/db/data.sqlite")
API_KEY = os.environ["SMOKE_API_KEY"]

# (label, model) pairs. Model IDs are taken from this gateway's own /v1/models
# catalog so the probe reaches the real upstream rather than 400ing on a typo.
TARGETS = [
    ("groq", "groq/llama-3.3-70b-versatile"),
    ("openrouter", "openrouter/openrouter/free"),
    ("nvidia", "nvidia/nemotron-3-ultra-550b-a55b"),
    ("atria", "atria/Atria-Dawn-Preview"),
    ("antigravity", "ag/gemini-3.8-flash"),
    ("codex", "cx/gpt-5.6-sol"),
    ("clinepass", "clinepass/cline-pass/glm-5.2"),
    ("commandcode", "cmc/deepseek/deepseek-v4-flash"),
    ("codebuddy-intl", "cbai/glm-5.2"),
    ("qoder", "qd/ultimate"),
    ("kiro", "kr/claude-sonnet-4.5"),
    ("grok-cli", "gcli/grok-4.7"),
    ("trae/opencode", "tr/openai/gpt-5.4-nano"),
]


def wait_ready(port, timeout=25.0):
    start = time.time()
    while time.time() - start < timeout:
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{port}/health", timeout=1) as r:
                if r.status == 200:
                    return True
        except Exception:
            time.sleep(0.1)
    return False


def probe(label, model, port):
    """Issue one real chat completion; return a behavior fingerprint."""
    url = f"http://127.0.0.1:{port}/v1/chat/completions"
    payload = json.dumps({
        "model": model,
        "messages": [{"role": "user", "content": "Reply with the single word: pong"}],
        "stream": False,
        "max_tokens": 16,
    }).encode()
    req = urllib.request.Request(url, data=payload, headers={
        "Content-Type": "application/json",
        "Authorization": f"Bearer {API_KEY}",
    })
    t0 = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=90) as resp:
            body = resp.read()
            ms = (time.perf_counter() - t0) * 1000
            doc = json.loads(body)
            # Fingerprint: outcome + response envelope, ignoring volatile values.
            return {
                "label": label,
                "model": model,
                "status": resp.status,
                "ok": resp.status == 200,
                "envelope": sorted(doc.keys()),
                "has_choices": bool(doc.get("choices")),
                "content_len": len(json.dumps(doc.get("choices", [{}])[0].get("message", {}).get("content", "")))
                if doc.get("choices") else 0,
                "ms": round(ms),
            }
    except urllib.error.HTTPError as e:
        ms = (time.perf_counter() - t0) * 1000
        raw = e.read().decode(errors="replace")[:300]
        err_type = ""
        try:
            err_type = json.loads(raw).get("error", {}).get("type", "")
        except Exception:
            pass
        return {"label": label, "model": model, "status": e.code, "ok": False,
                "err_type": err_type, "err_body": raw[:160], "ms": round(ms)}
    except Exception as e:
        ms = (time.perf_counter() - t0) * 1000
        return {"label": label, "model": model, "status": 0, "ok": False,
                "err_type": type(e).__name__, "err_body": str(e)[:160], "ms": round(ms)}


def run(binary, tag, port):
    tmpdir = tempfile.mkdtemp()
    os.makedirs(os.path.join(tmpdir, "db"), exist_ok=True)
    shutil.copy2(REAL_DB, os.path.join(tmpdir, "db", "data.sqlite"))
    # Strip any WAL sidecars so the copy is self-contained.
    for suffix in ("-wal", "-shm"):
        side = os.path.join(tmpdir, "db", "data.sqlite" + suffix)
        if os.path.exists(side):
            os.remove(side)

    proc = subprocess.Popen(
        [binary],
        env=dict(os.environ, PORT=str(port), DATA_DIR=tmpdir, LOG_LEVEL="warn"),
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    results = []
    try:
        if not wait_ready(port):
            print(f"[!] {tag}: proxy failed to start")
            return results
        for label, model in TARGETS:
            r = probe(label, model, port)
            results.append(r)
            status = "OK " if r["ok"] else "ERR"
            detail = (r.get("envelope") or r.get("err_type", ""))if not r["ok"] else r["envelope"]
            print(f"  [{status}] {label:<18} http={r['status']:<4} {r['ms']:>6}ms  {detail}")
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
        shutil.rmtree(tmpdir, ignore_errors=True)
    return results


if __name__ == "__main__":
    mode = sys.argv[1]                      # "pgo" | "nopgo"
    binary = sys.argv[2]
    outfile = sys.argv[3]
    port = PROXY_PORT + (0 if mode == "pgo" else 1)
    print(f"=== live provider smoke: {mode} ({binary}) ===")
    res = run(binary, mode, port)
    with open(outfile, "w") as f:
        json.dump(res, f, indent=2)
    ok = sum(1 for r in res if r["ok"])
    print(f"=== {mode}: {ok}/{len(res)} providers returned HTTP 200 ===")
