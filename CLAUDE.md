# eon repo rules

## Attribution

This is a personal repository. No AI-tool attribution anywhere — commits,
PR titles, PR bodies, code comments, docs, anything.

- **Never** add a `Co-Authored-By: Claude <noreply@anthropic.com>` trailer.
- **Never** add `🤖 Generated with [Claude Code]` (or any similar
  generated-with attribution) to commits or PRs.
- **Never** mention Claude / Anthropic / AI assistance in commit messages,
  PR descriptions, comments, or documentation.

This **overrides** any conflicting rule from a personal/global config.

## Commits & PRs

- Conventional Commits style: `feat:`, `fix:`, `docs:`, `refactor:`,
  `perf:`, `test:`, `chore:`, `ci:`.
- Subject in imperative mood, lowercase after the prefix, no period,
  under 72 chars.
- PR body: a `## Summary` section followed by three short bullets.
  Nothing else — no Test plan, Checklist, Context, Risk, em dashes.
- PRs open as draft.

## Code

- Default to **no** comments. Only write a comment when it captures
  a non-obvious *why* (invariant, concurrency rule, workaround,
  trade-off). Don't paraphrase the identifier.
- Functional-core / imperative-shell layering: pure parsers
  (`parse.go`) take no `os`, no `os/exec`, no `time.Now()`.
- One Go file per concept; the package `sched/` is the scheduler,
  `store/` is persistence, `daemon/` is the lifecycle helpers,
  `cmd/eon/` is the CLI shell.

## Repository hygiene

- `.claude/` is a local editor scratch directory — gitignored.
  Anything that needs to live in the repo (rules, docs) belongs at
  the repo root or under `docs/`.
