# Statusline stdin payload — verified field inventory

Captured from a live Claude Code session (v2.1.210, 2026-07-15) via a
transient tee wrapper on the installed statusline; 9 renders, identical
shape. All values below are neutralized to the repo's fixture identity —
the raw capture is never committed. "Parsed" = consumed by
`internal/input` today. "Docs" = listed on code.claude.com/docs/en/statusline.md
at capture time (fields marked *live-only* were observed in the real payload
but not in the published schema — treat as undocumented and subject to change).

| Field | Type | Neutralized sample | Parsed | Docs |
|---|---|---|---|---|
| `session_id` | str | `0a1b2c3d-0000-4000-8000-000000000000` | yes | yes |
| `session_name` | str | `demo-session-name` | no | live-only |
| `prompt_id` | str | `11112222-0000-4000-8000-000000000000` | no | live-only |
| `transcript_path` | str | `/Users/dev/.claude/projects/demo/transcript.jsonl` | no | yes |
| `version` | str | `2.1.210` | no | yes |
| `cwd` | str | `/Users/dev/projects/demo-app` | fallback only | yes |
| `workspace.current_dir` | str | `/Users/dev/projects/demo-app` | yes | yes |
| `workspace.project_dir` | str | `/Users/dev/projects/demo-app` | no | yes |
| `workspace.added_dirs` | list | `[]` | no | live-only |
| `workspace.repo.host` | str | `github.com` | no | live-only |
| `workspace.repo.owner` | str | `demo-org` | no | live-only |
| `workspace.repo.name` | str | `demo-app` | no | live-only |
| `model.id` | str | `claude-fable-5` | no | yes |
| `model.display_name` | str | `Fable 5` | yes | yes |
| `output_style.name` | str | `default` | no | yes |
| `effort.level` | str | `xhigh` | no | live-only |
| `fast_mode` | bool | `false` | no | live-only |
| `thinking.enabled` | bool | `true` | no | live-only |
| `exceeds_200k_tokens` | bool | `true` | no | yes |
| `context_window.context_window_size` | num | `1000000` | yes | yes |
| `context_window.used_percentage` | num | `48` | yes | yes |
| `context_window.remaining_percentage` | num | `52` | no | live-only |
| `context_window.total_input_tokens` | num | `481579` | no | live-only |
| `context_window.total_output_tokens` | num | `318` | no | live-only |
| `context_window.current_usage.input_tokens` | num | `2` | no | live-only |
| `context_window.current_usage.output_tokens` | num | `318` | no | live-only |
| `context_window.current_usage.cache_read_input_tokens` | num | `480949` | no | live-only |
| `context_window.current_usage.cache_creation_input_tokens` | num | `628` | no | live-only |
| `cost.total_cost_usd` | num | `87.3046…` | no | yes |
| `cost.total_duration_ms` | num | `695036912` | yes | yes |
| `cost.total_api_duration_ms` | num | `4661888` | no | yes |
| `cost.total_lines_added` | num | `481` | yes | yes |
| `cost.total_lines_removed` | num | `36` | yes | yes |
| `rate_limits.five_hour.used_percentage` | num | `19` | no | live-only |
| `rate_limits.five_hour.resets_at` | num (epoch s) | `1784134800` | no | live-only |
| `rate_limits.seven_day.used_percentage` | num | `45` | no | live-only |
| `rate_limits.seven_day.resets_at` | num (epoch s) | `1784556000` | no | live-only |

## Confirmed / refuted (2026-07-15 what's-new review claims)

- `effort.level` — **CONFIRMED shipped** (review called it "pending"): string level in every render.
- `fast_mode` — **CONFIRMED shipped**: boolean.
- `thinking.enabled` — present (not on the review's radar at all).
- `rate_limits` (5h/7d) — present with percentages and epoch resets: the two
  all-model meters no longer *require* the OAuth usage endpoint. The endpoint
  remains the only source for: account email, per-model windows, extra-usage
  flag, spend, and active-limit percents.
- Subagent count/status — **ABSENT**. Account email — **ABSENT**. Per-model /
  Fable weekly windows — **ABSENT**. All three stay on the watch card
  (claude-statusline-60g.20).
- `cost.total_cost_usd` — present with a real accumulating value even under
  subscription auth.

## Notes

- `resets_at` here is epoch seconds; the OAuth endpoint serves RFC3339 —
  a stdin-sourced meter needs its own reset-label formatting path.
- `exceeds_200k_tokens` was `true` while `used_percentage` read 48 on a 1M
  window — it flags absolute tokens past 200k, not window pressure.
