#!/usr/bin/env python3
import os, sys, time, json, sqlite3, tempfile, subprocess, urllib.request
from concurrent.futures import ThreadPoolExecutor

MOCK_PORT = 20199
PROXY_PORT = 20135

def setup_db(tmpdir):
    db_dir = os.path.join(tmpdir, "db")
    os.makedirs(db_dir, exist_ok=True)
    db_path = os.path.join(db_dir, "data.sqlite")
    con = sqlite3.connect(db_path)
    cur = con.cursor()
    cur.executescript("""
    CREATE TABLE IF NOT EXISTS apiKeys (
        id TEXT PRIMARY KEY, key TEXT, name TEXT, machineId TEXT, isActive INTEGER DEFAULT 1, createdAt TEXT
    );
    INSERT INTO apiKeys (id, key, name, isActive, createdAt) VALUES
        ('bench-key', 'sk-benchmark-test-key', 'benchmark', 1, datetime('now'));

    CREATE TABLE IF NOT EXISTS providerConnections (
        id TEXT PRIMARY KEY, provider TEXT, authType TEXT, name TEXT, email TEXT,
        priority INTEGER, isActive INTEGER DEFAULT 1, data TEXT, createdAt TEXT, updatedAt TEXT
    );
    INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
        ('bench-conn', 'openai-compatible-chat-bench', 'apikey', 'mock', 1, 1,
         '{"apiKey":"sk-mock-key"}', datetime('now'), datetime('now'));

    CREATE TABLE IF NOT EXISTS providerNodes (
        id TEXT PRIMARY KEY, type TEXT, name TEXT, data TEXT, createdAt TEXT, updatedAt TEXT
    );
    INSERT INTO providerNodes (id, type, name, data, createdAt, updatedAt) VALUES
        ('openai-compatible-chat-bench', 'openai-compatible', 'mock-provider',
         '{"prefix":"mock","apiType":"chat","baseUrl":"http://127.0.0.1:20199"}',
         datetime('now'), datetime('now'));

    CREATE TABLE IF NOT EXISTS combos (
        id TEXT PRIMARY KEY, name TEXT, kind TEXT, models TEXT, createdAt TEXT, updatedAt TEXT
    );
    INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES
        ('bench-combo', 'bench-combo', 'fallback',
         '["mock/mock-model"]', datetime('now'), datetime('now'));

    CREATE TABLE IF NOT EXISTS kv (scope TEXT, key TEXT, value TEXT, PRIMARY KEY(scope, key));
    CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT);
    CREATE TABLE IF NOT EXISTS modelAliases (alias TEXT PRIMARY KEY, target TEXT);
    """)
    con.commit()
    con.close()
    return db_path

def wait_for_url(url, timeout=5.0):
    start = time.time()
    while time.time() - start < timeout:
        try:
            with urllib.request.urlopen(url, timeout=0.5) as r:
                if r.status == 200:
                    return True
        except Exception:
            pass
        time.sleep(0.05)
    return False

def benchmark_run(binary_path, title, capture_profile=False, profile_out=None):
    # Kill any existing listeners
    subprocess.run("lsof -ti :20199,20135 | xargs kill -9 2>/dev/null || true", shell=True)
    time.sleep(0.5)

    tmpdir = tempfile.mkdtemp()
    setup_db(tmpdir)

    mock_proc = subprocess.Popen(["go", "run", "benchmark/mock_upstream.go"],
                                 env=dict(os.environ, MOCK_PORT=str(MOCK_PORT)),
                                 stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if not wait_for_url(f"http://127.0.0.1:{MOCK_PORT}/v1/chat/completions"):
        print("Failed to start mock upstream")
        mock_proc.kill()
        return None

    proxy_proc = subprocess.Popen([binary_path],
                                  env=dict(os.environ, PORT=str(PROXY_PORT), DATA_DIR=tmpdir, PPROF_ENABLED="true", LOG_LEVEL="warn"),
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if not wait_for_url(f"http://127.0.0.1:{PROXY_PORT}/health"):
        print("Failed to start proxy")
        proxy_proc.kill()
        mock_proc.kill()
        return None

    url = f"http://127.0.0.1:{PROXY_PORT}/v1/chat/completions"
    payload = json.dumps({
        "model": "mock/mock-model",
        "messages": [{"role": "user", "content": "Benchmark request"}],
        "stream": False,
        "max_tokens": 10
    }).encode()
    headers = {
        "Content-Type": "application/json",
        "Authorization": "Bearer sk-benchmark-test-key"
    }

    def single_req():
        req = urllib.request.Request(url, data=payload, headers=headers)
        t0 = time.perf_counter()
        try:
            with urllib.request.urlopen(req, timeout=5.0) as resp:
                _ = resp.read()
                dur = (time.perf_counter() - t0) * 1000.0  # ms
                return True, dur
        except Exception as e:
            dur = (time.perf_counter() - t0) * 1000.0
            return False, dur

    # Warmup
    for _ in range(50):
        single_req()

    if capture_profile and profile_out:
        print(f"[*] Capturing pprof CPU profile into {profile_out}...")
        # Start background profile collection for 10 seconds
        prof_cmd = f"curl -s -o '{profile_out}' 'http://127.0.0.1:{PROXY_PORT}/debug/pprof/profile?seconds=8'"
        prof_proc = subprocess.Popen(prof_cmd, shell=True)
        # Drive load for 8 seconds
        end_time = time.time() + 8.5
        with ThreadPoolExecutor(max_workers=30) as ex:
            while time.time() < end_time:
                futs = [ex.submit(single_req) for _ in range(30)]
                for f in futs: f.result()
        prof_proc.wait()
        print(f"[✓] Profile captured ({os.path.getsize(profile_out)} bytes)")

    concurrencies = [1, 10, 25, 50, 100]
    total_reqs_per_level = 500
    results = []

    print(f"\n--- {title} ---")
    print(f"{'Concurrency':>12} | {'Reqs':>6} | {'RPS':>9} | {'Avg (ms)':>9} | {'p50 (ms)':>9} | {'p95 (ms)':>9} | {'p99 (ms)':>9} | {'Max (ms)':>9}")
    print("-" * 88)

    for c in concurrencies:
        reqs = total_reqs_per_level
        latencies = []
        success = 0
        failed = 0

        t_start = time.perf_counter()
        with ThreadPoolExecutor(max_workers=c) as executor:
            futures = [executor.submit(single_req) for _ in range(reqs)]
            for fut in futures:
                ok, lat = fut.result()
                latencies.append(lat)
                if ok:
                    success += 1
                else:
                    failed += 1
        t_total = time.perf_counter() - t_start

        latencies.sort()
        avg_lat = sum(latencies) / len(latencies)
        p50 = latencies[int(len(latencies) * 0.50)]
        p95 = latencies[int(len(latencies) * 0.95)]
        p99 = latencies[int(len(latencies) * 0.99)]
        max_lat = latencies[-1]
        rps = (success + failed) / t_total

        results.append({
            "concurrency": c,
            "reqs": reqs,
            "success": success,
            "failed": failed,
            "rps": rps,
            "avg": avg_lat,
            "p50": p50,
            "p95": p95,
            "p99": p99,
            "max": max_lat,
        })

        print(f"{c:>12} | {reqs:>6} | {rps:>9.1f} | {avg_lat:>9.2f} | {p50:>9.2f} | {p95:>9.2f} | {p99:>9.2f} | {max_lat:>9.2f}")

    proxy_proc.terminate()
    mock_proc.terminate()
    proxy_proc.wait()
    mock_proc.wait()

    return results

if __name__ == "__main__":
    mode = sys.argv[1] if len(sys.argv) > 1 else "bench"
    bin_path = sys.argv[2] if len(sys.argv) > 2 else "/tmp/9router-bench"
    title = sys.argv[3] if len(sys.argv) > 3 else "Benchmark"
    prof_out = sys.argv[4] if len(sys.argv) > 4 else None

    if mode == "profile":
        benchmark_run(bin_path, title, capture_profile=True, profile_out=prof_out)
    else:
        res = benchmark_run(bin_path, title)
        if len(sys.argv) > 4:
            with open(sys.argv[4], "w") as f:
                json.dump(res, f, indent=2)
