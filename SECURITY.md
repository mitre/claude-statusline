# Security Policy

## Reporting Security Issues

The MITRE SAF team takes security seriously. If you discover a security
vulnerability in claude-statusline, please report it responsibly.

### Contact Information

- **Email**: [saf-security@mitre.org](mailto:saf-security@mitre.org)
- **GitHub**: use the repository's [Security tab](https://github.com/mitre/claude-statusline/security) to report privately

### What to Include

1. **Description** of the vulnerability
2. **Steps to reproduce** the issue
3. **Potential impact** assessment
4. **Suggested fix** (if you have one)

### Response Timeline

- **Acknowledgment**: within 48 hours
- **Initial assessment**: within 7 days
- **Fix timeline**: varies by severity

## Security Model

- **Untrusted input**: the session JSON on stdin and the workspace path are
  untrusted — unparseable JSON renders nothing, and the path is only ever
  passed as a directory argument to direct `exec` (no shell anywhere; every
  subprocess argument is a compile-time constant, except the macOS keychain
  service and account names, which are derived and must match their exact
  expected pattern before `security` runs)
- **Credentials**: the OAuth token is resolved per fetch the way Claude Code
  resolves it — the macOS keychain item first, then the plaintext
  `.credentials.json` in Claude Code's storage directory (the README's
  "Security posture" design note gives the exact names and directories; a
  relative directory is never read) — sent only in the `Authorization`
  header, and never logged or cached
- **Bounded network**: the single outbound request (usage endpoint) has a 2 s
  timeout and a 1 MB response cap; error payloads are never cached
- **Cache hygiene**: cache files are written atomically (temp file + rename)
  with 0600 permissions

## Security Testing

All wired into `make check` and CI:

```sh
make vuln   # govulncheck against the module
make lint   # includes gosec (SAST)
make race   # full suite under the race detector
```

## Supported Versions

Pre-1.0: only the latest `main` / most recent 0.x release is supported.
