# eon repo rules

## Attribution

This is a personal repository. Forbidden everywhere (commits, PR titles,
PR bodies, code comments, docs):

- The `Co-Authored-By: Claude <noreply@anthropic.com>` trailer.
- The `🤖 Generated with [Claude Code]` line, or any similar
  "generated with" attribution.
- Any mention of Claude, Anthropic, or AI assistance.

This **overrides** any conflicting rule from a personal or global config.

## Commits and PRs

- Conventional Commits style: `feat:`, `fix:`, `docs:`, `refactor:`,
  `perf:`, `test:`, `chore:`, `ci:`.
- Subject in imperative mood, lowercase after the prefix, no period,
  under 72 chars.
- PR body: a `## Summary` section followed by three short bullets.
  Nothing else. No Test plan, Checklist, Context, Risk, or em dashes.
- PRs open as draft.

## Code

- Default to **no** comments. Write one only when it captures a
  non-obvious *why*: an invariant, a concurrency rule, a workaround,
  or a trade-off. Don't paraphrase the identifier.
- Functional-core / imperative-shell layering. The pure parsers in
  `parse.go` take no `os`, no `os/exec`, no `time.Now()`.
- Packages:
  - `sched/`: scheduler loop.
  - `store/`: persistence.
  - `daemon/`: lifecycle helpers (data dir, flock, signals).
  - `cmd/eon/`: CLI shell.

## Repository hygiene

`.claude/` is a local editor scratch directory and is gitignored.
Anything that needs to live in the repo (rules, docs) belongs at the
repo root or under `docs/`.
