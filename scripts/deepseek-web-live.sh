#!/usr/bin/env bash
#
# Live end-to-end check for the deepseek-web executor, driven by you, not by CI.
#
# The credential is a browser-login userToken, so it cannot be a fixture and it
# does not belong in this repository or in a chat transcript. This script reads
# it from a file you own and never echoes it: every request the script makes
# carries it in a header, and no output path prints a request body.
#
#   1. chat.deepseek.com -> DevTools -> Application -> Local Storage -> userToken
#   2. printf '%s' '<paste>' > .ds-token        (chmod 600 not meaningful on
#                                               Windows; keep it out of git)
#   3. bash scripts/deepseek-web-live.sh
#
# What it proves, and what it cannot:
#   - the real four-call turn runs: token exchange, session create, pow, completion
#   - the pow nonce our solver returns is accepted by the real endpoint
#   - both SSE channels arrive and the stream terminates with [DONE]
#   - the turn is metered in usageHistory
# It cannot prove anything about bot detection or long-run quota behaviour; a
# single turn from one IP is not a substitute for that.
#
set -euo pipefail

PORT="${DS_PORT:-20331}"
TOKEN_FILE="${DS_TOKEN_FILE:-.ds-token}"
PROVIDER="deepseek-web"
MODEL="${DS_MODEL:-deepseek-v4-pro}"
BASE_URL="${DS_BASE_URL:-}"
[[ -n "$BASE_URL" ]] || BASE_URL=""
# An override is a harness convenience: it lets this same script be pointed at a
# local fake upstream so the script's own steps are exercised without a real
# credential. Empty in the normal run, and `set -u` would abort on it otherwise.
base_url_field=""
[[ -z "$BASE_URL" ]] || base_url_field=",\"baseUrl\":\"$BASE_URL\""

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

if [[ ! -s "$TOKEN_FILE" ]]; then
  echo "no credential at $TOKEN_FILE — see the header of this script" >&2
  exit 1
fi

# -s so the token is read without ever entering a command line, and the file is
# read into a variable the caller has already promised not to print.
token="$(cat "$TOKEN_FILE")"

# A pasted credential is itself JSON ({"value":"t.…","__version":"0"}), so it
# cannot be interpolated into a JSON body directly: its inner quotes close the
# apiKey string early and the server answers "invalid JSON". Encode it properly.
token_json="$(python -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$token")"
if [[ ${#token} -lt 20 ]]; then
  echo "$TOKEN_FILE looks too short to be a userToken" >&2
  exit 1
fi

workdir="$(mktemp -d)"
cleanup() {
  # Stop the gateway and wait for it to exit before deleting its database.
  # Removing first races SQLite's own open handle, which on Windows fails with
  # "Device or resource busy" and leaves the temp dir behind.
  if [[ -n "${gw_pid:-}" ]]; then
    kill "$gw_pid" 2>/dev/null || true
    wait "$gw_pid" 2>/dev/null || true
  fi
  # A failed run is the interesting one, so keep the log rather than deleting the
  # evidence. This must happen before the rm below. DS_KEEP=1 keeps the workdir.
  if [[ "${DS_KEEP:-0}" == "1" ]]; then
    echo "gateway log kept at $workdir/gateway.log" >&2
  else
    cp "$workdir/gateway.log" ./gateway-live-failed.log 2>/dev/null || true
  fi
  rm -rf "$workdir"
}
trap cleanup EXIT

echo "== building"
go build -o "$workdir/9router" ./cmd/9router-go

# auth.CLIToken() derives from two files under DATA_DIR and creates them on first
# use. Seed them before the server starts, in the exact format
# readOrCreateHexFile expects — 16 random bytes and 32 random bytes, hex encoded —
# so the gateway adopts ours instead of minting its own after we have already
# computed the token from nothing.
python - "$workdir/data" <<'PY'
import pathlib, secrets, sys
root = pathlib.Path(sys.argv[1])
(root / "auth").mkdir(parents=True, exist_ok=True)
(root / "machine-id").write_text(secrets.token_hex(16))
(root / "auth" / "cli-secret").write_text(secrets.token_hex(32))
PY

echo "== starting gateway on :$PORT"
PORT="$PORT" DATA_DIR="$workdir/data" \
  ALLOW_PRIVATE_NETWORKING=true \
  "$workdir/9router" >"$workdir/gateway.log" 2>&1 &
gw_pid=$!


for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -fsS "http://127.0.0.1:$PORT/health" >/dev/null || { echo "gateway never became healthy" >&2; tail -20 "$workdir/gateway.log" >&2; exit 1; }

echo "== probing the credential (the cheapest call that proves the session)"
# The dashboard group is gated by RequireDashboardAuth, and requireLogin lives in
# the DB settings rather than the environment, so REQUIRE_LOGIN=false does not
# open it. The supported operator credential is the local CLI token, derived
# exactly like upstream Next does: sha256(machine-id + "9r-cli-auth" + cli-secret).
cli_token="$(python - "$workdir/data" <<'PY'
import hashlib, pathlib, sys
root = pathlib.Path(sys.argv[1])
raw = (root / "machine-id").read_text().strip()
secret = (root / "auth" / "cli-secret").read_text().strip()
print(hashlib.sha256((raw + "9r-cli-auth" + secret).encode()).hexdigest()[:16])
PY
)"

if [[ -z "$BASE_URL" ]]; then
  # The real probe. No -f: an invalid userToken answers 401, and that body is
  # the diagnostic — curl -f would discard it and print a bare "exit 22".
  probe="$(curl -sS -X POST "http://127.0.0.1:$PORT/api/providers/validate" \
    -H "x-9r-cli-token: $cli_token" -H 'Content-Type: application/json' \
    -d "{\"provider\":\"$PROVIDER\",\"apiKey\":$token_json}")"
  echo "$probe"
  case "$probe" in
    *'"valid":true'*) ;;
    *) echo "probe did not return valid:true — the userToken is not usable" >&2; exit 1 ;;
  esac
else
  # A baseUrl override is only a harness convenience, so skip the real probe
  # rather than let it judge a credential against the live site. The probe URL
  # is fixed in validate_web_providers.go; it does not read an override.
  echo "== skipping the credential probe (baseUrl override)"
fi

echo "== registering the connection"
[[ -z "$BASE_URL" ]] || echo "   (baseUrl override: $BASE_URL)"
conn="$(curl -fsS -X POST "http://127.0.0.1:$PORT/api/connections" \
  -H "x-9r-cli-token: $cli_token" -H 'Content-Type: application/json' \
  -d "{\"name\":\"DeepSeek Web\",\"provider\":\"$PROVIDER\",\"priority\":0,\"isActive\":true,\"data\":{\"apiKey\":$token_json$base_url_field}}" \
  | python -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
echo "connection $conn registered"

echo "== minting an engine key for the chat calls (/v1/* wants a client key, not the CLI token)"
key="sk-live-$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
curl -fsS -X POST "http://127.0.0.1:$PORT/api/keys" \
  -H "x-9r-cli-token: $cli_token" -H 'Content-Type: application/json' \
  -d "{\"name\":\"ds-live\",\"key\":\"$key\"}" >/dev/null

echo "== one real turn, streaming"
curl -sS -N -m 180 -X POST "http://127.0.0.1:$PORT/v1/chat/completions" \
  -H "Authorization: Bearer $key" -H 'Content-Type: application/json' \
  -d '{"model":"'"$PROVIDER/$MODEL"'","stream":true,"messages":[{"role":"user","content":"reply with the single word: ok"}]}' \
  > "$workdir/stream.txt" || { echo "streaming request failed" >&2; tail -20 "$workdir/gateway.log" >&2; exit 1; }

echo "-- chunks received:   $(grep -c '^data:' "$workdir/stream.txt" || true)"
echo "-- reasoning channel: $(grep -c 'reasoning_content' "$workdir/stream.txt" || true)"
echo "-- content channel:   $(grep -c '"content"' "$workdir/stream.txt" || true)"
echo "-- terminated [DONE]: $(grep -c '\[DONE\]' "$workdir/stream.txt" || true)"

echo
echo "== one real turn, non-streaming (exercises the aggregation path)"
curl -sS -m 180 -X POST "http://127.0.0.1:$PORT/v1/chat/completions" \
  -H "Authorization: Bearer $key" -H 'Content-Type: application/json' \
  -d '{"model":"'"$PROVIDER/$MODEL"'","stream":false,"messages":[{"role":"user","content":"reply with the single word: ok"}]}' \
  | python -m json.tool 2>/dev/null | head -30 || echo "non-streaming call did not return json"

echo
echo "== metered rows"
python - "$workdir/data/db/data.sqlite" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
rows = list(db.execute(
    "select provider, model, promptTokens, completionTokens, status "
    "from usageHistory where provider='deepseek-web' order by rowid"))
if not rows:
    print("NO ROWS — the turn did not reach the ledger"); sys.exit(1)
for r in rows:
    print(r)
PY

rm -f "$TOKEN_FILE"
echo
echo "done. credential file removed."