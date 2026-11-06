# pxi: instructions for coding agents

pxi is a fast review IDE built on px0 (see README.md and MAINTAINING.md).

## Rule zero: never more memory than px0

No change may make pxi use more memory than the px0 it is built on. That is the
reason pxi exists. Before you finish a feature:

- Build UI on first use and free it when closed (`replaceChildren()`, drop caches).
- No polling or background work for hidden views; no new always-running process.
- Bound every list; no new long-lived server caches.
- Run `bun scripts/pxi-budget.js <px0-bin> <pxi-bin> <workspace>` (CI runs it as
  `.github/workflows/budget.yml`) and say what it printed.

## Working on this repo

- New code in new files (`fork_*.go`, `worktrees.go`, `web/src/pxi-*.js`, `web/pxi/`,
  `macos/`); upstream files get one-line hooks marked `// fork:`.
- User-visible px0 → pxi naming, theme defaults and Bun tooling are applied by
  `python3 scripts/pxi-brand.py`; run `--check` before committing.
- Build with Bun: `bun scripts/build-web.js`. Keep top-level names in pxi's JS
  modules unique and no comment after an `import` line (the Node fallback bundler
  shares one scope).
- Tests: `go test ./...`. Three upstream tests fail on macOS on unmodified px0 too
  (TestPRReviewerChangesInIndex, TestGitStagedFilesStatAndDiff, TestDeletedFileHandling).
