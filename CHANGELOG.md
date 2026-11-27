# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
The project has no tagged releases yet; every change so far is listed under
Unreleased.

## [Unreleased]

### Added

- Usage analytics: after a one-time consent, the app scans Claude Code
  transcripts under `~/.claude/projects` (the last 30 days first, then new
  sessions as they appear) and stores numbers and names only.
- Usage pages: dashboard, sessions, session detail, tools and extensions,
  insights, extension events and collection settings.
- Session debugging: the session timeline is read from the original transcript
  on demand, and secret-looking values are masked before display.
- Local insight rules that turn usage aggregates into findings and
  recommendations without network calls.
- Extension events: a loopback `/v1/events` endpoint that lets plugins, MCP
  servers and scripts report what happens inside a tool, linked to sessions and
  tool calls. See the [Extension Events API](docs/extension-api.md).
- CLI tools: `trace-emit` sends extension events from shells, scripts and hooks;
  `trace-usage` prints usage aggregates as JSON without the app.
- Localization: the interface is English by default and can be switched to
  Korean. Insight text and desktop dialogs follow the selected language.
- Open-source project files: English README with a Korean copy, contributing
  guide, security policy, code of conduct, issue and pull request templates,
  Dependabot configuration and a CI workflow.
- Apache License 2.0 (`LICENSE`).

### Changed

- Collector source kinds are driven from a single registry.
- The frontend shell is split into one file per page.
- English is now the default language of the documentation. Korean copies use
  a `-ko` suffix.

### Security

- Hardened endpoint state, project exclusion, masking, CLI guards and input
  bounds after a security review.
- The extension events receiver binds to `127.0.0.1` only and rejects requests that carry an
  `Origin` header, a mismatched `Host` or more than one `Authorization` header.

[Unreleased]: https://github.com/0xmhha/trace-agent/commits/main
