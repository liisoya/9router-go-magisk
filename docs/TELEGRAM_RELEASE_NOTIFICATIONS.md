# Telegram release notifications

Every tagged release posts a short summary to a Telegram group.

## Setup

### 1. Create the bot

Message [@BotFather](https://t.me/BotFather) → `/newbot` → follow the prompts.
It replies with a token that looks like `123456789:AAH...`.

### 2. Add the bot to the group

Add it to the target group as a member. **This step is not optional**: a bot
cannot post into a group it has not been added to, and the API answers with
`403 Forbidden` rather than explaining that.

If the group has discussion enabled, either disable it or promote the bot to
allow posting — Telegram restricts new members from posting in some groups.

### 3. Find the chat ID

Add the bot to the group, send any message there (or forward one into the
group), then open:

```
https://api.telegram.org/bot<TOKEN>/getUpdates
```

Find the entry whose `chat.title` matches your group and copy `chat.id`.
Supergroup IDs are negative (`-1001234567890`); channels are negative too.

Verifying before wiring the secret catches a wrong ID at the console instead
of in a failed release run:

```bash
curl -s "https://api.telegram.org/bot<TOKEN>/getUpdates"
curl -s -X POST "https://api.telegram.org/bot<TOKEN>/sendMessage" \
  -d chat_id="-1001234567890" -d text="test"
```

### 4. Store both as repository secrets

```bash
gh secret set TELEGRAM_BOT_TOKEN
gh secret set TELEGRAM_CHAT_ID
```

`gh secret set` reads from stdin, so do not paste the values into the command
line — shell history is retained. Prefer a prompt:

```bash
gh secret set TELEGRAM_BOT_TOKEN --body "123456789:AAH..."
```

Or use the web UI: **Settings → Secrets and variables → Actions → New
repository secret**.

## Checking the format without a release

```bash
bash scripts/notify-telegram.sh --tag v1.9.11 --notes CHANGELOG.md --dry-run
```

`--dry-run` prints the message and exits without sending. Useful after
changing the summary format — no tag required.

To test the real send path against the group before the next release:

```bash
TELEGRAM_BOT_TOKEN=... TELEGRAM_CHAT_ID=... \
  bash scripts/notify-telegram.sh --tag v9.9.9-test --notes CHANGELOG.md
```

## What the message looks like

```
🚀 9router-go v1.9.11
✅ Stable release

What's new:

✨ feat(dashboard): satukan enam baris kontrol menjadi satu menu
🐛 fix(chat): error model-gated tidak mengunci seluruh akun
… up to 10 entries

Release notes: https://github.com/luqman-v1/9router-go/releases/tag/v1.9.11
    docker pull luqmenul/9router-go:1.9.11
```

Only headings are listed, not their bullets: the full notes live on the
release page, and a 700-line changelog is unreadable on a phone. A prerelease
tag gets an experimental banner instead, because it is absent from `latest`.

## Why it lives in `release.yml`

The `notify` job declares `needs: [binaries, docker]`, so it runs after the
release page and the Docker image both exist. A separate workflow triggered by
the tag push would run in parallel with the build and routinely publish a
dead link.

`continue-on-error: true` is deliberate: a Telegram outage must not mark a
successful release as failed.

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| `401 Unauthorized` | Wrong or revoked token. Re-create it via BotFather. |
| `403 Forbidden` | Bot is not in the group, or posting is restricted. |
| `400 Bad Request: chat not found` | Wrong `chat.id`, usually a missing `-100` prefix. |
| Job yellow, message missing | `continue-on-error` swallowed a failure — open the job log for the response body. |
| Message has literal `*` or backticks | Sent as plain text by design; that is expected. |
| `400 strings must be encoded in UTF-8` | Non-ASCII passed through argv on Git Bash. See the section below. |
| `400 message text is empty` | Form fields separated by newlines instead of `&`. |
| `400 TOPIC_CLOSED` | `TELEGRAM_TOPIC_ID` points at a deleted topic. |

The script prints Telegram's raw JSON response, so the exact API error is in
the job log.

## Sending to a specific topic

The group has Topics enabled (`is_forum: true`). Without a thread id, Telegram
files each notice under a fresh "Messages" topic that members may never open.
To pin the notice to one topic, add its id — the number from the topic URL, or
the last component of `t.me/c/<chat>/<topic>`:

```bash
echo 1234 | gh secret set TELEGRAM_TOPIC_ID --repo luqman-v1/9router-go
```

The script omits the field entirely when the variable is unset, so leaving it
out is a valid configuration rather than a broken one.

## Encoding: why the payload goes through a file

The message is URL-encoded into a temporary file and posted with
`--data-binary @file`, rather than passed as a curl argument. On Git Bash
(Windows) a bash variable holding non-ASCII is transcoded to the ANSI code
page on the way into an argument, and Telegram answers:

```
400 Bad Request: strings must be encoded in UTF-8
```

Measured behaviour: a variable of pure ASCII sends fine, while the same
variable containing an em-dash never does — whether it came from a file, from
`printf`, or was written as a literal. Emoji alone survive, so the failure only
surfaces once an entry contains a dash, which entries here always do.

The ubuntu-latest runner is unaffected, so this never fires in CI. It matters
when running the script locally to check a format, which is the main reason
the script exists.

Two related details that cost time while building this:

- The form fields must be separated by `&`. A newline-separated body is
  accepted by curl and rejected by Telegram with `message text is empty`.
- `curl --fail-with-body` turns a rejected send into exit 22 and discards
  Telegram's error description, so the job log shows a transport error instead
  of "chat not found". The script reads the body and exits 1 itself.