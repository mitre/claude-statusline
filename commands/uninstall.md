---
description: Remove the claude-statusline binary and restore your settings.json backup, with consent
allowed-tools: [Bash, Read, Edit, AskUserQuestion]
---

# claude-statusline uninstall

You are reversing what `/claude-statusline:setup` did. Nothing is removed
without the user's explicit consent.

## Step 1: Ask consent

Use AskUserQuestion:

> Remove the installed binary (`~/.claude/claude-statusline`) and restore
> your `settings.json` from the setup-time backup? Timestamped backups of
> the binary are left in place for manual rollback.

If declined: stop, change nothing.

## Step 2: Restore settings

- List `~/.claude/settings.json.backup-*`. If backups exist, show the newest
  one's `statusLine` section and restore that file over
  `~/.claude/settings.json` ONLY after confirming with the user that the
  newest backup is the right one.
- If no backup exists, Read `~/.claude/settings.json` and remove just the
  `statusLine` key, leaving everything else byte-for-byte untouched.

## Step 3: Remove the binary

```bash
rm -f ~/.claude/claude-statusline
```

Leave the `~/.claude/claude-statusline.backup-*` files — tell the user they
exist for manual rollback and can be deleted whenever they choose.

## Step 4: Finish

Tell the user to run `/plugin uninstall claude-statusline` to remove the
plugin itself (this command only reverses the binary + settings side).
