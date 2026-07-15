---
description: Install the claude-statusline binary (checksum-verified) and wire it into settings.json with your consent
allowed-tools: [Bash, Read, Edit, Write, AskUserQuestion]
---

# claude-statusline setup

You are installing the claude-statusline binary and, only with the user's
explicit consent, pointing Claude Code's `statusLine` setting at it.

Execute the steps in order. Do not skip, combine, or improvise around a
failure — a failed step ends the setup with a clear report.

## Step 1: Install the verified release binary

Run exactly:

```bash
VER=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json" | head -1) && sh "${CLAUDE_PLUGIN_ROOT}/scripts/install-binary.sh" "$VER"
```

The script downloads the release archive for this OS/arch, verifies its
sha256 against the release's `checksums.txt`, refuses on any mismatch, backs
up any existing binary beside itself (timestamped `.backup-*`), and installs
atomically to `~/.claude/claude-statusline`.

- On failure: STOP. Show the error verbatim. Never retry with verification
  weakened, never install an unverified download.
- If the output mentions a `dev` version being replaced, tell the user their
  previous binary was a development build and name its backup file.

## Step 2: Ask consent before touching settings

Use AskUserQuestion, exactly this decision:

> Point Claude Code's statusLine at the installed binary? This edits
> `~/.claude/settings.json` — a timestamped backup of the file is created
> first. (Choosing No leaves settings untouched; the binary stays installed
> and the README documents the manual snippet.)

If the user declines: report where the binary lives, show the manual
settings snippet from the README, and finish. Do NOT edit settings.

## Step 3: Back up, then write the setting

1. If `~/.claude/settings.json` exists, back it up first:

```bash
cp -p ~/.claude/settings.json ~/.claude/settings.json.backup-$(date +%Y%m%d-%H%M%S)
```

2. Read `~/.claude/settings.json` (create `{}` if absent) and set ONLY the
   `statusLine` key, leaving every other key byte-for-byte untouched:

```json
"statusLine": {
  "type": "command",
  "command": "~/.claude/claude-statusline",
  "padding": 0
}
```

## Step 4: Verify and report

1. Prove the binary answers: `~/.claude/claude-statusline --version`
2. Tell the user: the status line appears on the next render; the plugin's
   SessionStart hook keeps the binary version-pinned to the plugin from now
   on (it never touches `dev` builds); config lives at
   `~/.claude/statusline.toml` (see the repo's `statusline.example.toml`);
   `/claude-statusline:uninstall` reverses everything.

Never print credential material. Never edit any settings key other than
`statusLine`.
