# Contributing to Trace Agent

Thank you for your interest in improving Trace Agent. This guide covers the development setup, the checks a change must pass, and what we look for in a pull request.

By taking part you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md). To report a security problem, do not open an issue; follow [SECURITY.md](SECURITY.md) instead.

## Development setup

The desktop app is macOS only. The root Go module (the usage pipeline, the receivers and the CLI tools) also builds and tests on Linux.

You need:

- macOS with the Xcode Command Line Tools (for the desktop app and code signing)
- Go 1.25 or later
- Node.js (current LTS) and npm
- Google Chrome, only for the browser tests
- [golangci-lint](https://golangci-lint.run/) v2, for linting

The repository has two Go modules:

| Module | Path | Contents |
|---|---|---|
| `github.com/0xmhha/trace-agent` | `/` | Pipelines, receivers, stores and the CLI tools in `cmd/` |
| `github.com/0xmhha/trace-agent/desktop` | `desktop/` | Wails desktop app; uses the root module through a `replace` directive |

The desktop module embeds `desktop/frontend/dist`, so build the frontend before you build, vet or test anything under `desktop/`.

## Build

```sh
# Frontend (runs npm ci and npm run build)
make -C desktop frontend

# Desktop app bundle
make -C desktop package

# CLI tools: desktop/build/bin/trace-emit and trace-usage
make -C desktop tools
```

## Test

Run these before opening a pull request:

```sh
node projects/trace-agent/tests/verify.mjs
go vet ./...
go test -race -count=1 ./...
make -C desktop check
```

`make -C desktop check` builds the frontend and runs the desktop module's tests. Tests that need a real browser, an installed client or local history skip themselves unless an environment variable turns them on.

The browser tests open the production UI in Chrome:

```sh
(cd desktop && TRACE_IA_BROWSER=1 go test -count=1 -run '^TestDesktop(IA|UsageIA)$' .)
```

Notes on restricted environments:

- Many tests listen on a loopback port (`127.0.0.1:0`). In a sandbox that blocks local port binding they fail with `bind: operation not permitted`. That is the environment, not the code; run them where local ports are allowed.
- If the default Go build cache is not writable, point `GOCACHE` at a writable directory.

## Code style

- **Go**: format with `gofmt` (CI rejects unformatted code), keep `go vet` clean and run `golangci-lint run ./...` in both modules. Wrap errors with `%w`, pass `context.Context` first, give every I/O a timeout, and inject clocks instead of calling `time.Now()` in logic. Exported identifiers have doc comments that start with the identifier's name. Code comments are in English.
- **TypeScript and React**: strict mode. `npm run build` runs `tsc --noEmit` and must pass. Do not use `any`, `dangerouslySetInnerHTML` or `innerHTML`; transcript text is always rendered as plain text.
- **Privacy**: test data is synthetic. Never commit real transcript content, home-directory paths, e-mail addresses or secrets. Build secret-shaped test inputs at run time from fragments so that secret scanners do not flag them.
- **Dependencies**: do not add new dependencies without discussing it in an issue first.
- Formatting rules for editors are in [.editorconfig](.editorconfig).

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/): `type(scope): summary` in the imperative mood, in English, for example:

```
feat(usage): add cache hit rate to the dashboard
fix(emit): keep the exit code when the receiver is off
docs: explain hook linking in the extension API guide
```

Common types are `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `build`, `ci` and `chore`. Keep the body short and explain why the change is needed.

## Pull requests

1. Open an issue first for larger changes so the approach can be agreed on.
2. Keep each pull request focused on one change.
3. Fill in the pull request template. Before asking for review, check that:
   - `go vet ./...` and `go test -race -count=1 ./...` pass in the root module;
   - `npm run build` passes in `desktop/frontend` and `go test ./...` passes in `desktop/`;
   - `gofmt -l .` prints nothing and `golangci-lint run ./...` is clean;
   - new behaviour has tests, including the failure paths;
   - user-visible changes are documented in `README.md`, `README-ko.md` or `docs/`, and listed under `Unreleased` in [CHANGELOG.md](CHANGELOG.md);
   - no real user data, personal paths or secrets were added.

## Documentation languages

English is the default language of the project. Korean copies of the main documents use a `-ko` suffix (`README-ko.md`, `docs/extension-api-ko.md`). When you change one language, update the other or say in the pull request that a translation is still needed.

## Adding a user interface language

The interface is English by default and can be switched to Korean. The localization design and the steps for adding a language are in [docs/design/i18n.md](docs/design/i18n.md). In short:

1. Add a row to `LOCALES` in `desktop/frontend/src/i18n/core.ts`.
2. Add the language to every file in `desktop/frontend/src/i18n/messages/`; the TypeScript compiler lists every missing key.
3. Add the code to `usage.Locales()` and to the Go message tables (insight rules and desktop dialogs).

## License

By contributing, you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE), the license of this project.
