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

The [bug report form](https://github.com/mitre/claude-statusline/issues/new?template=bug_report.yml) asks for each of these.

### Suggesting Enhancements

Enhancement suggestions are tracked as GitHub issues. Describe the current behavior, the improvement, and any alternatives you considered. For a new feature or config key, opening an issue before writing the code lets us agree on the shape first.

### Security Vulnerabilities

Do **not** open a public issue — see [SECURITY.md](./SECURITY.md).

## Development

You need Go (the version in [`go.mod`](go.mod)) and `make`. Fork the
repository, clone your fork, and work on a branch:

```sh
git clone git@github.com:<you>/claude-statusline.git
cd claude-statusline
git remote add upstream https://github.com/mitre/claude-statusline.git
git checkout -b feat/short-description
```

```sh
make tools   # once per clone: install the pinned tool versions (golangci-lint, govulncheck, goreleaser)
make hooks   # once per clone: install the pre-commit fast gate (runs make lint)
make check   # the one gate: lint + vuln + race + cover + build — must be green before any PR
```

The gate targets refuse a mismatched local tool version and tell you to run
`make tools` — versions are pinned once in the Makefile, and CI installs the
same way, so local green means CI green. The README's
[Develop](README.md#develop) section lists every make target.

To try your build in Claude Code, install it as the dev copy and point a
single session at it, without touching your settings:

```sh
make dev-install   # builds HEAD and installs it as ~/.claude/claude-statusline-dev
claude --settings '{"statusLine":{"type":"command","command":"~/.claude/claude-statusline-dev","padding":0}}'
```

## Project conventions

[AGENTS.md](AGENTS.md) describes the architecture in full. The rules a
change is reviewed against:

- **TDD**: no production code without a failing test first. A test exists to
  catch a defect in the code: it does not assert documentation text, and it
  fails if the behavior it covers breaks.
- **Golden tests are the display spec.** Rendered output is pinned
  byte-for-byte in `internal/render`; a display change updates the goldens
  deliberately in the same commit. A golden that fails unexpectedly means the
  code is wrong, not the golden.
- **Default output does not change.** A new option is off by default, or
  leaves the default render byte-identical, unless the change is the
  agreed purpose of the PR.
- **OS, network and subprocess access goes through the `deps` struct** in
  `run.go`, injected in tests, so `make test` needs no network.
- **Neutral fixtures**: `/Users/dev`, `~/projects/demo-app`,
  `dev@example.com`, session `0a1b2c3d-0000-4000-8000-000000000000`. Never a
  real path, email, session id or token, and nothing token-shaped (secret
  scanners flag `sk-ant-…`-like strings).
- **Sandbox `HOME`** (`t.Setenv("HOME", t.TempDir())`) in any test that
  reaches `config.Default()` or `config.Load()`; otherwise the test reads
  and writes your real cache.
- **Render state comes from arguments.** No package-level mutable state in
  the render path; a row's output depends only on what it is passed.
- **Zero linter suppressions**: fix root causes; the golangci-lint config's
  path-scoped exclusions each carry a written justification.
- **Never crash the host**: bad input renders nothing or falls back to
  defaults with a stderr message; nothing exits non-zero for data problems.
- **Conventional commits**: `feat:`, `fix:`, `docs:`, `chore:`, `test:`,
  `refactor:`.

### Adding a config key

A new key lands in one PR, in all of these places:

1. `fileSchema` in `internal/config/config.go`, with its default in
   `render.DefaultOptions()` (or `config.Default()`).
2. Tests in `internal/config/config_test.go` for the default, a valid
   value, and every kind of invalid value.
3. [`statusline.example.toml`](statusline.example.toml): the key at its
   default, with a comment saying what it does.
4. The README's [Configure](README.md#configure) table: key, default, effect.
   When the key needs more than one table row to explain (several values,
   what each part paints, an example), add a README subsection and link it
   from the table, as [Colors](README.md#colors) does.
5. If it changes a row, the README's
   [What each row means](README.md#what-each-row-means) section, with an
   example of the output.
6. `CHANGELOG.md` under `[Unreleased]`.

The docs say what the key does, its default, its valid values, and what
happens with an invalid one. An invalid value is normally a config error:
the whole file is ignored, every key falls back to its default, and the
error is printed on stderr. Where a value has one safe correction, the
key may adjust it instead and keep the rest of the file, with a stderr
note (`[usage] ttl_seconds` below 200 is raised to 200); the docs say which
applies. Check each statement against the code and a real run of the
binary.

### Changing what is displayed

- Golden tests for the new output, including the default case staying
  unchanged.
- Tests for edge values (zero, negative, over 100%, absent fields): never
  render a fabricated value such as `0%` for missing data.
- README and CHANGELOG updated in the same PR.

## Pull request process

1. **Keep "Allow edits by maintainers" checked** when you open the PR.
   `main` moves quickly, and with it on we can rebase your branch, resolve conflicts, or push a small
   review fix without asking you for another round trip. We comment on the
   PR before any force-push and describe every change we make, as separate
   commits on top of yours.
2. `make check` is green locally.
3. Fill in the [pull request template](.github/pull_request_template.md).
   One logical change per PR.
4. **CI**: until your first PR here is merged, GitHub waits for a
   maintainer to approve each CI run on your PR, so the checks can show as
   waiting. CI runs the same `make` targets as `make check`.
5. **Review**: a maintainer reviews the code, the tests and the docs, and
   may run an additional automated cross-model review. Findings are posted
   on the PR. We may fix small findings ourselves on your branch (see 1).
6. **Merge**: maintainers merge with a merge commit (never squash), so your
   commits land as you wrote them.
7. Sometimes a PR overlaps work already planned. We may then fold its idea
   into that change instead of merging the PR; we say so on the PR and
   credit it in the CHANGELOG.

Releases are cut by maintainers; see
[docs/release-pipeline.md](docs/release-pipeline.md).

## Questions

Open an [issue](https://github.com/mitre/claude-statusline/issues).
