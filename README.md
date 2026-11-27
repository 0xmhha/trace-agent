# Trace Agent

English | [한국어](README-ko.md)

Trace Agent is a macOS app that shows how you use Claude Code. It reads your local session transcripts and turns them into a dashboard of tokens, models, tools, skills, plugins, MCP servers and hooks. You can open any session and follow its conversation and tool calls step by step to debug it, and events sent by third-party tools appear on the same timeline. Everything stays on your Mac.

The original evidence-tracing features for Codex and Claude Code are still available under the **Trace** menu. From a task you can follow subagents, tool calls and their results, and the evidence behind each judgement. Pinning a view lets you return to the tasks and statistics of the same revision later.

## Features

| Menu | What it shows |
|---|---|
| Dashboard | Sessions, prompts, tokens, cache hit rate, cost, daily trend, usage by model, most used tools and projects, activity by weekday and hour, and an insight summary |
| Sessions | Sessions in the selected period, sortable by tokens, failures and cost, searchable by title and path |
| Session detail | Conversation timeline (prompts, responses, tool calls and results, hooks, extension events), per-tool totals, token flow and subagents |
| Tools and extensions | Calls, failure rate and latency per tool, MCP server, skill, plugin, hook, subagent and Bash program |
| Insights | Problems found in your usage habits, with recommendations. Rules that fired, rules that passed and rules skipped for lack of data are shown separately |
| Extension events | Events sent by third-party tools, status per source, and how to connect |
| Collection settings | Turn collection on and off, set the period, exclude projects, rescan and erase |
| Trace | Live, overview, tasks, modules, sources and settings (the original screens) |

The interface is in English by default and can be switched to Korean.

## Privacy model

On first launch the app asks once whether it may read `~/.claude/projects`. If you agree, it reads the session transcripts of the last 30 days and then follows new sessions automatically.

What is stored:

- The app's database holds numbers and names only: token counts, model IDs, names of tools, skills, plugins, MCP servers and hooks, project paths, session titles, branch names, and the program names of Bash commands.

What is not stored or sent:

- Conversation text and tool inputs and outputs are not stored. Session detail reads them directly from the original transcript when you open it. If the original file is deleted, only the numbers remain.
- Nothing the app reads leaves your Mac. Insights and recommendations are produced by local rules.
- Cost is taken only from the values Claude Code writes into the transcript. It is never estimated from tokens.

Limits you should know about:

- Strings that look like secrets are masked before they are shown. Masking is best effort and is not guaranteed to catch every secret. E-mail addresses, IP addresses and host names are not masked.
- When numbers may be incomplete, the screen says so. Unreadable files, skipped lines and ranges outside the collection period are marked as a "partial" total.

The design and the reasoning behind these choices are in the [usage analytics design](docs/design/usage-refactor.md) (Korean).

## Requirements

- macOS with the Xcode Command Line Tools
- Go 1.25 or later
- Node.js and npm

## Build and run

Run from the repository root.

```sh
make -C desktop package
open "desktop/build/bin/Trace Agent.app"
```

Turn on usage analytics with **Start collecting** on the first screen.

Sources for the trace features are chosen separately: open **Trace → Sources and settings → Choose source**. For Codex, choose the project history or a rollout file. For Claude Code, choose a project session folder or a JSONL file. To see Claude child sessions as well, choose a session folder that contains both the root and child transcripts. Collection scope and retention can be set per source. The default scope is metadata.

Codex and Claude Code OTel records can be received by adding a **local OTel receiver** in the same window. Copy the terminal settings from the receiver details and apply them to a new CLI session. The Keychain holds the sending credentials, and supported body fields are sanitized only for sources you consented to. Participating MCP servers and Claude hooks are connected from **Additional observation sources** of an existing client source. See [Claude OTel](projects/trace-agent/otlp-compat.md), [Codex CLI and OTel](projects/trace-agent/codex-compat.md) and [hook and MCP wire format](projects/trace-agent/observer-sdd.md) (all in Korean).

## CLI tools

Build both command-line tools with:

```sh
make -C desktop tools
```

### `trace-emit`

Sends an extension event to the app from a shell, a script or a hook.

```sh
desktop/build/bin/trace-emit --name build --status ok --duration-ms 812
desktop/build/bin/trace-emit run --name deploy -- ./deploy.sh
```

### `trace-usage`

Runs the same usage pipeline in the terminal and prints JSON. It uses its own temporary database and never touches the app's database.

```sh
desktop/build/bin/trace-usage --days 30 --pretty
```

## Extension events

A transcript only records that Claude Code called a tool. What happened inside the tool has to be reported by the tool itself. The app offers a receiving endpoint (`/v1/events`) that listens on this Mac only. When plugins, MCP servers or scripts send events to it, the app links them to sessions and tool calls and shows them on the timeline.

Turn the receiver on and copy its settings on the **Extension events** screen. From a shell, use `trace-emit` as shown above.

The wire format, how to connect hooks, and the limits are described in the [Extension Events API](docs/extension-api.md) ([한국어](docs/extension-api-ko.md)).

## Verification

```sh
node projects/trace-agent/tests/verify.mjs
go test -race -count=1 ./...
make -C desktop check
(cd desktop && TRACE_IA_BROWSER=1 go test -count=1 -run '^TestDesktop(IA|UsageIA)$' .)
```

The last command opens the trace screens and the usage screens in Chrome. On the usage side it reads synthetic transcripts through the real pipeline and checks the numbers shown on screen.

| Design stage | Verification record (Korean) |
|---|---|
| 1–2. Sources and adapters | [Support per source](projects/trace-agent/source-support.md) · [Data contract](projects/trace-agent/stage-1-sdd.md) · [Collection and resume](projects/trace-agent/stage-2-sdd.md) |
| 3–4. SQLite and fact queries | [Storage, deletion and recovery](projects/trace-agent/stage-3-sdd.md) · [Facts and statistics](projects/trace-agent/stage-4-sdd.md) |
| 5–6. Transport and sequence | [GraphQL and WebSocket](projects/trace-agent/stage-5-sdd.md) · [Sequence at a fixed point](projects/trace-agent/stage-6-sdd.md) |
| 7–8. Screens and package | [Screen acceptance tests](projects/trace-agent/stage-7-sdd.md) · [Package and performance](projects/trace-agent/stage-8-sdd.md) |

## Project layout

| Path | Contents |
|---|---|
| `cmd/trace-emit` | Command that sends extension events |
| `cmd/trace-usage` | Command that prints usage aggregates without the app |
| `cmd/trace-hook` | Forwards minimal Claude Code hook metadata to the local trace endpoint |
| `internal/usage` | Usage analytics pipeline: `transcript` (parsing), `db` (storage and aggregates), `timeline` (session detail), `insight` (local rules), `service` (scheduling and requests) |
| `internal/extension` | Loopback receiver for extension events |
| `internal/collect`, `internal/ingress`, `internal/observer` | Trace sources: local files, OTLP receiver, hook and MCP observers |
| `internal/store`, `internal/readmodel`, `internal/transport` | Trace evidence store, read model and the GraphQL transport used by the desktop client |
| `desktop` | Wails desktop app (Go) and its React frontend in `desktop/frontend` |
| `docs` | API guide and design documents |
| `projects/trace-agent` | Stage specifications, fixtures and verification scripts of the trace features |

## Documentation

- [Extension Events API](docs/extension-api.md) ([한국어](docs/extension-api-ko.md))
- [Usage analytics design](docs/design/usage-refactor.md) (Korean)
- [Localization](docs/design/i18n.md)
- [Support per source](projects/trace-agent/source-support.md) (Korean)

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, tests and pull request checklist, and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for community expectations. Notable changes are listed in [CHANGELOG.md](CHANGELOG.md).

## Security

Please do not report vulnerabilities in public issues. See [SECURITY.md](SECURITY.md) for how to report one privately.

## License

See [LICENSE](LICENSE).
