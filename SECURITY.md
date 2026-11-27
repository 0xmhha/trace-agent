# Security Policy

## Supported versions

Trace Agent has no tagged releases yet. Security fixes are made on the `main` branch only.

| Version | Supported |
|---|---|
| `main` | Yes |
| Anything else | No |

## Reporting a vulnerability

Please do not report security problems in public issues, discussions or pull requests.

Report them privately through GitHub's private vulnerability reporting: open the repository's **Security** tab and choose **Report a vulnerability**. Only the maintainers can see the report.

Please include:

- what is affected and what an attacker could do;
- steps or a small proof of concept to reproduce it;
- the commit you tested and your macOS, Go and Node.js versions.

Do not include real transcript content, credentials or other personal data in the report. Synthetic examples are enough.

The maintainers will acknowledge the report, keep you informed while it is investigated, and credit you in the advisory if you wish. Please give us reasonable time to release a fix before disclosing the problem publicly.

## Scope

Trace Agent reads local data and listens only on loopback. Reports in these areas are especially welcome:

- **Loopback endpoints**: the extension events receiver (`/v1/events`) and the other local receivers bound to `127.0.0.1`. For example authentication bypass, credential leaks, request smuggling from a web page, missing limits or crashes caused by crafted input.
- **The desktop bridge**: the interface between the web frontend and the Go process of the desktop app. For example script injection into the UI, or a frontend call that reads or deletes data it should not.
- **Transcript handling**: reading `~/.claude/projects` and showing sessions. For example reading files outside the transcript root, following symbolic links, storing conversation text that should not be stored, or secrets that are shown or stored although a documented masking rule should have caught them.

Out of scope:

- Secrets that masking does not claim to catch. Masking is best effort, and the documentation lists what it does not mask (for example e-mail addresses, IP addresses and host names).
- Attacks that require an attacker who already controls your user account or your Mac.
- Problems in third-party tools that send events to Trace Agent.
- Known limitations listed in the [Extension Events API](docs/extension-api.md#12-known-limitations), such as the shared credential.
