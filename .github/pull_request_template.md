## What and why

<!-- What this PR changes and the problem it solves. Link the issue: "Closes #123". -->

## How it was tested

<!-- Tests added or changed, and how you ran the binary to see the change
(command and output, or a screenshot of the status line). -->

## Checklist

- [ ] "Allow edits by maintainers" is checked (see [CONTRIBUTING.md](../CONTRIBUTING.md#pull-request-process))
- [ ] `make check` is green locally
- [ ] Tests were written first and fail without the change
- [ ] Display change: golden tests updated deliberately; default output unchanged unless that is the point of the PR
- [ ] New config key: `fileSchema`, config tests (default, valid, invalid), `statusline.example.toml`, README table (and a README section if it needs more than a row)
- [ ] README and `CHANGELOG.md` (`[Unreleased]`) updated for any user-visible change
- [ ] Fixtures use the neutral identity (`/Users/dev`, `dev@example.com`); no real paths, emails or tokens
- [ ] Tests that reach `config.Default()` or `config.Load()` sandbox `HOME`
