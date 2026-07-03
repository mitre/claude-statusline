# Contributing to claude-statusline

Thank you for considering a contribution!

## Code of Conduct

By participating in this project, you are expected to uphold our [Code of Conduct](./CODE_OF_CONDUCT.md):

- Use welcoming and inclusive language
- Be respectful of differing viewpoints and experiences
- Gracefully accept constructive criticism
- Focus on what is best for the community
- Show empathy towards other community members

## How Can I Contribute?

### Reporting Bugs

Before creating bug reports, please check existing issues. When you create one, include as many details as possible:

- **Use a clear and descriptive title**
- **Describe the exact steps to reproduce** — for rendering bugs, the session JSON fed on stdin is the reproducer (redact tokens and private paths before posting)
- **Describe the behavior you observed** and what you expected instead
- **Include your environment** (OS, Go version, terminal emulator)

### Suggesting Enhancements

Enhancement suggestions are tracked as GitHub issues. Describe the current behavior, the improvement, and any alternatives you considered.

### Security Vulnerabilities

Do **not** open a public issue — see [SECURITY.md](./SECURITY.md).

## Development

```sh
make check   # the one gate: lint + vuln + race + cover + build — must be green before any PR
```

- **TDD**: no production code without a failing test first. Rendered output is
  pinned byte-for-byte by golden tests — display changes update goldens
  deliberately, never accidentally.
- **Zero linter suppressions**: fix root causes; the golangci-lint config's
  path-scoped exclusions each carry a written justification.
- **Conventional commits**: `feat:`, `fix:`, `docs:`, `chore:`, `test:`.

## Pull Request Process

1. `make check` green (CI runs the same targets — no drift).
2. Update README and CHANGELOG in the same PR when behavior or commands change.
3. One logical change per PR.
