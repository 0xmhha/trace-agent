# Extension Events API

English | [한국어](extension-api-ko.md)

This guide is for people who build plugins, MCP servers and shell scripts. It explains how to send what happens inside your tool to Trace Agent.

## 1. Purpose

A Claude Code transcript records only that a tool was called and what it returned. It does not record what the tool did inside. From the transcript alone you cannot tell at which step a deploy script stopped, or how many times an MCP server retried its backend API.

The Extension Events API fills that gap. A tool sends events, Trace Agent stores them, links them to sessions and tool calls, and shows them on the timeline.

- The only receiving address is `127.0.0.1` on this Mac. Events never leave it.
- There are two ways to send: plain HTTP, or the `trace-emit` command.
- Tracing is optional. Your tool must keep working when the receiver is off. Every example in this guide follows that rule.

## 2. Turning it on and copying the settings

The receiver is off by default.

1. In the app, open `Settings → Extension events` and turn the receiver on. The app asks you to confirm.
2. Copy the settings on the same screen. They contain the address and the credential.
3. Set the values as environment variables wherever your tool runs.

| Environment variable | Contents |
|---|---|
| `TRACE_EVENTS_URL` | Receiving address, of the form `http://127.0.0.1:<port>/v1/events`. |
| `TRACE_EVENTS_TOKEN` | Credential, sent in the `Authorization` header. |
| `TRACE_EVENTS_SOURCE` | Optional. Default source name used by `trace-emit`. |

Claude Code hooks and MCP servers inherit the environment Claude Code was started in. So the two values must be present in the shell that starts Claude Code.

The credential is a secret. Do not write it into a repository, a shared configuration file or a log. The examples in this guide never spell out the value and refer to it only as `$TRACE_EVENTS_TOKEN`.

## 3. Request format

```
POST http://127.0.0.1:<port>/v1/events
Authorization: Bearer $TRACE_EVENTS_TOKEN
Content-Type: application/json
```

The body is either one event or several events wrapped as `{"events":[...]}`.

```json
{
  "version": 1,
  "id": "<a fresh random string for each event>",
  "source": "deploy-script",
  "kind": "span",
  "name": "upload",
  "timestamp": "2026-01-01T01:00:00.000Z",
  "duration_ms": 812.5,
  "status": "error",
  "level": "error",
  "session_id": "<Claude Code session ID>",
  "tool_use_id": "<tool call ID>",
  "message": "exit status 1",
  "attributes": {"step": "3", "retries": 2},
  "value": 1.0,
  "unit": "count"
}
```

| Field | Required | Rule |
|---|---|---|
| `version` | Yes | `1`. |
| `source` | Yes | Name of the sending tool. `^[A-Za-z0-9._-]{1,64}$`. |
| `kind` | Yes | One of `span`, `log`, `metric`. |
| `name` | Yes | Name of what happened. 1 to 128 characters. |
| `id` | No | Idempotency key chosen by the sender. See section 6. |
| `timestamp` | No | RFC 3339. If absent, the time of receipt is used. |
| `status` | No | `ok`, `error`, `unknown`. Default `unknown`. |
| `level` | No | `debug`, `info`, `warn`, `error`. Default `info`. |
| `session_id`, `tool_use_id` | No | Session and tool call to link to. `^[A-Za-z0-9._:-]{1,128}$`. See section 7. |
| `trace_id`, `span_id`, `parent_span_id` | No | Trace identifiers the tool uses itself. Same rule as above. |
| `message` | No | Free text. Masked, then cut at 2,048 characters. |
| `attributes` | No | Up to 32. Keys match `^[A-Za-z0-9._-]{1,64}$`. Values are strings, numbers or booleans. They are stored as strings, masked, then cut at 256 characters. |
| `duration_ms`, `value` | No | Finite numbers, zero or greater. |
| `unit` | No | Unit of `value`. Up to 16 characters. |

Choose `kind` like this. Work with a start and an end is a `span`; send `duration_ms` with it. Something that happened at one moment is a `log`. A single number is a `metric`; send `value` and `unit` with it.

The receiver also checks the request itself. If any check fails, no event in the request is accepted.

- The method is `POST` and the path is `/v1/events`, with no query string.
- `Host` must match the receiving address.
- There must be no `Origin` header. This blocks requests that a web page sends through a browser.
- `Content-Type` is `application/json`.
- There is exactly one `Authorization` header.

## 4. Limits

| Item | Limit |
|---|---|
| Request body | 256 KiB |
| Events per request | 100 |
| `name` | 128 characters |
| `message` | 2,048 characters. The rest is cut. |
| `attributes` | 32. Values are cut at 256 characters. |
| `unit` | 16 characters |
| Concurrent requests | 4 |
| Request rate | 50 per second. Beyond that the receiver answers `429`. |

When you emit many events, batch them in `{"events":[...]}` instead of sending them one by one.

## 5. Responses and rejection reasons

A request that passes the checks is answered with `202`.

```json
{"accepted": 2, "rejected": [{"index": 1, "reason": "..."}]}
```

- `accepted` is the number of events accepted.
- `rejected` lists the events that were not accepted. `index` is the position of that event in the request.
- Valid events are accepted even if others are wrong. So do not assume that `202` means everything got in; check that `rejected` is empty.
- `reason` names the rule that was broken. It is meant for people to read. Code must not depend on its wording.

Events are rejected for the rules in the table in section 3: a required field is missing, `version` is not `1`, `kind`, `status` or `level` is not one of the allowed values, a name or identifier does not match its pattern or length, a number is negative or not finite, or there are more than 32 attributes.

Rejected input does not disappear silently. The app counts rejections and shows them on the Extension events screen. Requests with a wrong credential are not counted.

Any response other than `202` means the whole request was refused: it failed the request checks in section 3, the credential was wrong, the body was too large, or requests came too often.

## 6. Sending the same event twice safely

Sometimes you cannot tell whether a send failed or succeeded, for example when the timeout expires before the response arrives. To resend without storing the event twice, include an `id`.

- With an `id`, the same `id` from the same `source` is stored only once. Use the original `id` when you resend.
- Without an `id` but with a `timestamp`, events whose contents are all identical are treated as duplicates. Two different things that happened at the same time with the same contents are merged into one.
- With neither, every send is stored as a new event.

A fresh random string per event is enough for `id`. `trace-emit` writes 16 random bytes in hexadecimal.

## 7. Linking to sessions and tool calls

If an event carries `session_id` and `tool_use_id`, the app links it to that session and tool call.

- An event with `tool_use_id` appears in the session timeline right after that tool call.
- An event with only `session_id` appears at the place that matches its time.
- An event with neither is "unlinked". It appears only in the per-source list on the Extension events screen.

Both values are in the JSON that Claude Code passes to hooks on standard input.

```json
{"hook_event_name": "PostToolUse", "session_id": "...", "tool_use_id": "...", "tool_input": {}}
```

`trace-emit --from-hook` takes only these two values from standard input. It discards tool arguments and tool output without reading them. If you implement this yourself, we recommend taking only these two values as well.

A tool that does not receive hook input cannot know the two values. That is usually the case for MCP servers. Leave them empty then. Made-up values attach the event to some other session.

## 8. Masking

Before storing, the receiver replaces parts that look like secrets with the mask token `[가림]`.

- `name` and `message` are scanned as whole text and masked.
- For attributes the key name is checked first. If it contains a word such as `password`, `token`, `secret` or `api_key`, the whole value is masked. Keys that mean a count or a name, such as `token_count`, are exceptions. For other keys the value is scanned as text and masked.
- Masking targets known shapes: keys and tokens of cloud and hosting services, `Bearer` and `Basic` authorization values, passwords embedded in URLs, command-line arguments such as `--password`, assignments such as `password=...`, and private key blocks.

Masking is best effort. It is not guaranteed to catch every secret. E-mail addresses, IP addresses, host names and hexadecimal strings without a prefix are not masked. The surest approach is to never put secrets into events at all. Do not put whole command lines, environment variable listings, or request and response bodies into `message`.

## 9. Privacy and security

- The receiver binds to `127.0.0.1` only. Other devices cannot reach it.
- Events go only into the local database on this Mac. The app sends them nowhere.
- Events are deleted after 30 days. When more than 200,000 are stored, the oldest are deleted first.
- Excluding a project in the app also deletes events linked to that project's sessions. Erasing data deletes all events.
- Senders have duties too. Do not send if the address is not of the form `http://127.0.0.1:<port>/v1/events`. Do not follow redirects. Do not go through a proxy. All three keep the credential from leaving this Mac.

## 10. `trace-emit`

A sending command for shells and scripts. It reads the address and the credential from `TRACE_EVENTS_URL` and `TRACE_EVENTS_TOKEN`.

```
trace-emit [flags]                             send one event
trace-emit run [flags] -- <command> [args...]  run a command and send a span
```

### Sending one event

```sh
trace-emit --name build --status ok --duration-ms 812 --attr step=3
```

`--name` is required. Without `--kind` the kind is `log`.

### Wrapping a command

```sh
trace-emit run --name build -- make all
```

- Runs the command after `--`. Standard input and output stay connected.
- When the command ends, one `span` is sent. `timestamp` is the start time and `duration_ms` the elapsed time. `status` is `ok` if the exit code is 0 and `error` otherwise. The exit code goes into the attribute `exit_code`.
- `trace-emit` exits with the command's exit code. If the command cannot be started, it exits with 127. If the command was ended by a signal, it exits with 128 plus the signal number.
- Received signals (`INT`, `TERM`, `HUP`, `QUIT`, `USR1`, `USR2`) are forwarded to the command.
- The command inherits the environment of `trace-emit` except `TRACE_EVENTS_TOKEN`. Processes the command starts in turn, such as build scripts or package install hooks, do not get the credential either. `TRACE_EVENTS_URL` remains. If the wrapped command must send events itself, give it the credential separately.
- Without `--name` the name is the command's file name. Without `--kind` the kind is `span`.
- `--status`, `--duration-ms` and `--attr exit_code=...` are not allowed. `run` measures those values itself.

### Flags

| Flag | Meaning |
|---|---|
| `--name` | Event name |
| `--kind` | `span`, `log`, `metric` |
| `--status` | `ok`, `error`, `unknown` |
| `--level` | `debug`, `info`, `warn`, `error` |
| `--duration-ms` | Elapsed time in milliseconds |
| `--value`, `--unit` | Number and unit |
| `--message` | Free text |
| `--attr key=value` | Attribute. Can be repeated. If a key is repeated, the last value wins. |
| `--source` | Source name. Falls back to `TRACE_EVENTS_SOURCE`, then to `trace-emit`. |
| `--session-id`, `--tool-use-id` | Session and tool call to link to |
| `--trace-id`, `--span-id`, `--parent-span-id` | Trace identifiers |
| `--from-hook` | Reads `session_id` and `tool_use_id` from the hook JSON on standard input. Values given as flags take precedence. |
| `--verbose` | Writes why an event could not be sent to standard error. |

A flag with an empty value counts as not given. `--session-id "$SID"` is not an error when the variable is empty.

`--from-hook` reads at most 256 KiB of standard input. When sending a single event it waits 500 ms; if the input has not ended by then, it sends without a link.

`trace-emit` adds an `id` and a `timestamp` to every send. It first cuts `--message` at 8,192 characters and attribute values at 1,024 characters. The receiver cuts again after masking.

### Exit codes and output

A tool must not break because tracing is off. So failures related to sending never change the exit code.

| Situation | Sending one event | `run` |
|---|---|---|
| Sent | 0 | Command's exit code |
| Environment variables missing | 0. Nothing is sent. | Command's exit code |
| Address not of the form `http://127.0.0.1:<port>/v1/events` | 0. Nothing is sent. | Command's exit code |
| Receiver off, or no answer within 500 ms | 0 | Command's exit code |
| Receiver rejected the event | 0 | Command's exit code |
| Invalid flags | 2. Nothing is sent. | 2. The command is not run. |

- Only invalid flags end with 2. That covers unknown flags, flags without a value, a missing `--name`, an `--attr` not of the form `key=value`, and values that break the rules in section 3. The reason is written to standard error even without `--verbose`, because the user has to fix it.
- Values from the environment are not flags. An invalid `TRACE_EVENTS_SOURCE`, or hook input that cannot be read, never ends with 2. If the hook input cannot be used, the event is sent without a link.
- Nothing is written to standard output, so it does not clash with tools that parse a hook's standard output. `--help` is the only exception.
- Error messages never contain flag values, the address or the credential.

Claude Code treats exit code 2 from a hook as a blocking error. Run the command once by hand before putting it in a hook, to make sure the flags are right. If you put values that change on every run into `--name` or the identifier flags, append `|| true` to the command.

## 11. Examples

### curl

```sh
curl --silent --show-error --max-time 1 --noproxy 127.0.0.1 \
  --request POST "$TRACE_EVENTS_URL" \
  --header "Authorization: Bearer $TRACE_EVENTS_TOKEN" \
  --header "Content-Type: application/json" \
  --data '{"version":1,"source":"deploy-script","kind":"log","name":"deploy-started","attributes":{"target":"staging"}}' \
  || true
```

`--max-time` and `|| true` keep the script from hanging or failing when the receiver is off.

### Claude Code hook

A `PostToolUse` hook that sends an event every time the Bash tool finishes. Put it under `hooks` in the Claude Code settings file.

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "trace-emit --from-hook --source my-hooks --name bash-finished"
          }
        ]
      }
    ]
  }
}
```

`--from-hook` reads `session_id` and `tool_use_id` from the hook input. So this event appears in the session timeline right after the matching Bash call.

### Wrapping a script

```sh
trace-emit run --name nightly-build --attr target=release -- make all
```

If `make all` fails, this line fails with the same exit code. With tracing turned off, `make all` still runs as before.

You can also wrap a script that a hook calls.

```sh
trace-emit run --from-hook --name lint -- ./scripts/lint.sh
```

`trace-emit` reads the hook input first and passes what it read on to the script's standard input. The script receives the same input as before it was wrapped. In this case `trace-emit` waits up to 0.5 seconds for standard input to end before starting the script.

If standard input does not end within 0.5 seconds, because it is a terminal or a pipe that stays open, `trace-emit` stops waiting and starts the script. What it has read so far and anything that arrives later are passed to the script unchanged. In this case, however, the span is not linked to the session.

### MCP server (pseudocode)

Wrap the tool handler so that each call sends one `span`.

```
function call_tool(name, arguments):
    started = now()
    status = "ok"
    try:
        return run_tool(name, arguments)
    except error:
        status = "error"
        raise error
    finally:
        send_event({
            "version": 1,
            "id": random_hex(16),
            "source": "my-mcp-server",
            "kind": "span",
            "name": name,
            "timestamp": rfc3339(started),
            "duration_ms": milliseconds(now() - started),
            "status": status
        })

function send_event(event):
    url = env("TRACE_EVENTS_URL")
    token = env("TRACE_EVENTS_TOKEN")
    if url is empty or token is empty:
        return                                  # tracing is off
    if not matches(url, "^http://127\.0\.0\.1:[0-9]+/v1/events$"):
        return                                  # never send outside this Mac
    try:
        http_post(url,
            headers = {"Authorization": "Bearer " + token,
                       "Content-Type": "application/json"},
            body = json(event),
            timeout = 0.5 seconds,
            follow_redirects = false,
            proxy = none)
    except any error:
        return                                  # a failed send must not break the tool
```

- Do not put the tool's arguments or results into events. Send only names, elapsed time, success and counts.
- Do not let sending delay the tool's response. Keep the timeout short, or send after the response.
- If `session_id` and `tool_use_id` are unknown, leave them empty. The event appears as unlinked on the source screen.

## 12. Known limitations

- All tools share one credential. There are no per-source credentials.
- `source`, `session_id` and `tool_use_id` are chosen by the sender. A tool can use another tool's source name or attach events to another session. Read source names on screen as what the sender claimed.
- While the app is not running, another process that opens the same port could receive the credential.
- There is no way to issue a new credential yet.
- Masking does not catch every secret. See section 8.
- If you press Ctrl-C while using `trace-emit run` in a terminal, the wrapped command may receive `INT` twice: once from the terminal and once forwarded by `trace-emit`.
- `trace-emit` does not resend or queue events it failed to send. Events from the time the receiver was off are not kept.
