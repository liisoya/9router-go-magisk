#!/usr/bin/env bash
#
# Telegram release notification — format a message and post it to a group.
#
# Usage:
#   scripts/notify-telegram.sh --tag v1.9.11 --notes /tmp/notes.md
#   scripts/notify-telegram.sh --tag v1.9.11 --notes notes.md --dry-run
#
# Environment (secrets in CI):
#   TELEGRAM_BOT_TOKEN   token from @BotFather
#   TELEGRAM_CHAT_ID     target group id, e.g. -1001234567890
#
# --dry-run prints the message and exits 0 without sending, so the format can
# be checked locally before a tag ever exists.

set -euo pipefail

TAG=""
NOTES_FILE=""
DRY_RUN=0
ENTRIES=10

usage() {
  sed -n '3,16p' "$0" | sed 's/^# \{0,1\}//'
}

while [ $# -gt 0 ]; do
  case "$1" in
    --tag)     TAG="$2"; shift 2 ;;
    --notes)   NOTES_FILE="$2"; shift 2 ;;
    --entries) ENTRIES="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "notify-telegram: unknown argument $1" >&2; exit 2 ;;
  esac
done

if [ -z "$TAG" ]; then
  echo "notify-telegram: --tag is required" >&2
  exit 2
fi
if [ -z "$NOTES_FILE" ]; then
  echo "notify-telegram: --notes is required" >&2
  exit 2
fi

# urlencode percent-encodes stdin as application/x-www-form-urlencoded data.
#
# curl's --data-urlencode does this too, but only for values it receives as
# arguments, which is precisely the path that cannot carry UTF-8 on Git Bash.
# Encoding here, over a pipe, keeps the bytes out of argv entirely.
#
# Bytes outside the RFC 3986 unreserved set become %XX triplets, so the
# multibyte characters in an emoji pass through byte by byte — which is what
# Telegram's form parser expects. Newlines become %0A rather than being read as
# a field separator, and a literal `%` in a changelog entry becomes %25 rather
# than corrupting everything after it.
urlencode() {
  perl -0 -ne '
    # Read as bytes, not characters: a UTF-8 sequence must be escaped one byte
    # at a time, and letting perl decode it first would emit one triplet for
    # the whole codepoint.
    binmode STDOUT;
    s/([^A-Za-z0-9\-_.~])/sprintf("%%%02X", ord($1))/ge;
    print;
  '
}

# Telegram rejects a message over 4096 UTF-8 characters. The release notes are
# frequently longer than that on their own — one entry in this repository has
# been past 4000 characters — so the summary below is built to stay far under
# the limit rather than being cut at send time, which would slice mid-sentence
# and leave a dangling link.
MAX_LEN=4096

# Headings are the entries: `### fix(chat): …`. Bullets under them are the
# detail, and Telegram readers get that from the release page. Taking headings
# only is what keeps a 700-line changelog readable on a phone.
#
# `# Changelog` is the file title and `## [Unreleased]` / `## [v1.9.11]` are
# section markers, not entries. Matching `#{3,4}` skips the first two levels,
# which is why a bare `[Unreleased]` cannot reach the message — it was the one
# line in the sample output that carried no information at all.
summarize() {
  grep -E '^#{3,4} ' "$NOTES_FILE" \
    | sed -E 's/^#{3,4} //' \
    | head -"$ENTRIES" || true
}

entries="$(summarize)"
if [ -z "$entries" ]; then
  # A release with no headings at all (a commit-log fallback, say) still deserves
  # a notification; the first few list items are better than an empty body.
  entries="$(grep -E '^[-*] ' "$NOTES_FILE" | head -"$ENTRIES" || true)"
fi

release_url="https://github.com/luqman-v1/9router-go/releases/tag/${TAG}"
docker_image="${DOCKER_IMAGE:-luqmenul/9router-go}"

# A prerelease must not read like a stable install: it is absent from `latest`,
# so anyone pulling `latest` still gets the previous version.
if echo "$TAG" | grep -q -- '-'; then
  headline="🧪 Experimental build — not on latest, and '9router-go update' will not offer it to a stable install."
else
  headline="✅ Stable release"
fi

# No Markdown markers here on purpose: this goes out as plain text (see the
# curl call below), so a `*` or a backtick would reach the group as a literal
# asterisk rather than as emphasis.
message="🚀 9router-go ${TAG}
${headline}
"
if [ -n "$entries" ]; then
  message="${message}
What's new:

${entries}
"
fi
message="${message}
Release notes: ${release_url}
    docker pull ${docker_image}:${TAG#v}"

# Telegram measures length in characters and rejects anything over 4096, so the
# cap has to be enforced on characters — trimming by line count is not enough,
# because a single changelog heading in this repository has run past 400
# characters on its own. Both dimensions are applied: characters first, then a
# final line trim so the cut lands on a boundary rather than mid-word.
#
# The links are re-attached after the cut. A truncated notice whose release URL
# was chopped off is the one failure that makes the message useless, and this
# path is exactly where that would happen.
if [ "${#message}" -gt "$MAX_LEN" ]; then
  budget=$((MAX_LEN - 300))
  kept="$(printf '%s\n' "$message" | head -c "$budget")"
  kept="$(printf '%s\n' "$kept" | sed -E '$ s/[^ ]*$//')"
  message="${kept}

… more in the release notes

Release notes: ${release_url}
    docker pull ${docker_image}:${TAG#v}"
fi

if [ "$DRY_RUN" -eq 1 ]; then
  printf '%s\n' "$message"
  echo "--- (dry run, ${#message} chars, limit ${MAX_LEN}) ---" >&2
  exit 0
fi

if [ -z "${TELEGRAM_BOT_TOKEN:-}" ] || [ -z "${TELEGRAM_CHAT_ID:-}" ]; then
  echo "notify-telegram: TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID must be set" >&2
  exit 1
fi

# Plain text, no parse_mode. The notes carry emoji and backticks, and Markdown
# mode would need every one of those escaped by hand — a mis-escape silently
# mangles the message rather than failing loudly, which is the wrong trade for
# a notification nobody is watching while it runs.
#
# The payload is built into a file rather than passed as curl arguments, because
# a bash variable holding non-ASCII cannot survive argv on Git Bash: it is
# transcoded to the ANSI code page on the way into curl, and Telegram answers
# `400 strings must be encoded in UTF-8` having received nothing at all.
# Measured against the live API: a variable of pure ASCII sends fine, and the
# same variable with an em-dash in it never does — not when read from a file,
# not from printf, not written as a literal, every construction failing the
# same way. The file path behaves identically on the ubuntu-latest runner, so
# there is no platform branch to take here.
#
# urlencode percent-encodes the bytes for us. Escaping them by hand would mean
# accounting for every `&`, `=` and `%` in a changelog entry, and one miss
# there corrupts the message rather than failing visibly.
payload_file="$(mktemp)"
{
  # `&` separates the fields, not a newline. Telegram's form parser ignores a
  # newline-separated body and answers "message text is empty" while the
  # request itself looks perfectly formed — the same bytes that succeed here
  # are rejected when the separators become newlines.
  printf 'chat_id=%s&' "$(printf '%s' "$TELEGRAM_CHAT_ID" | urlencode)"
  printf 'text=%s&' "$(printf '%s' "$message" | urlencode)"
  printf 'disable_web_page_preview=true'
  # message_thread_id keeps the notice inside one topic when the group has
  # Topics enabled; without it Telegram files the message under a fresh
  # "Messages" topic that members may never open. Omitted entirely when unset,
  # because an empty value is rejected differently from an absent one.
  if [ -n "${TELEGRAM_TOPIC_ID:-}" ]; then
    printf '&message_thread_id=%s' "$(printf '%s' "$TELEGRAM_TOPIC_ID" | urlencode)"
  fi
} > "$payload_file"

# --fail is deliberately NOT used. It aborts curl on a 4xx and hands back exit
# 22 instead of the body, which loses Telegram's own error description — the
# one line that says whether the token, the chat id or the topic id was
# wrong. Reading the body and branching on it keeps that message and lets this
# script own the exit status: a failed notification must look like a failed
# notification in the job log, not like a curl transport error.
response="$(curl -sS --max-time 20 \
  -X POST "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/sendMessage" \
  --data-binary "@${payload_file}" \
  -H 'Content-Type: application/x-www-form-urlencoded')" || {
    echo "notify-telegram: could not reach the Telegram API" >&2
    exit 1
  }
rm -f "$payload_file"

echo "$response"
case "$response" in
  *'"ok":true'*) echo "notify-telegram: sent ${TAG}" >&2 ;;
  *)             echo "notify-telegram: Telegram rejected the message" >&2; exit 1 ;;
esac