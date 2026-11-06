# Maintaining pxi

pxi is built on [px0](https://github.com/px0-ai/px0) (MIT) and takes its fixes and
features every week. These rules keep that cheap.

## Rule zero: never more memory than px0

pxi exists because editors grew heavy. **No feature may make pxi use more memory than
the px0 it is built on.** CI enforces it (`.github/workflows/budget.yml` runs
`scripts/pxi-budget.js`): it builds px0 at the merge-base and pxi, runs both on the
same workspace, and fails when pxi exceeds px0 on server memory (+10% or +1 MB,
whichever is larger: noise on a ~7 MB process),
page JS heap (+10%), DOM elements at rest (+120: the activity bar) or after
opening and leaving pxi's views (+200). UI bundle size warns above +12% gzipped
and fails above +25%: code size is not memory (page JS heap covers that), but growth
should stay visible; past the warning, load big features on first use.
Run it locally with `bun scripts/pxi-budget.js <px0-bin> <pxi-bin> <workspace>`.

How features stay inside it:
- **Nothing until used.** Build a view's DOM the first time it opens; load
  heavy code on demand (like px0's Mermaid). No new always-running process.
- **Free on close.** A closed view drops its rows (`replaceChildren()`) and its
  caches; the next open redraws from the server.
- **No background work for hidden views.** Poll only while visible; anything
  else (a badge) at most once a minute. Prefer server events to polling.
- **Server work is per request.** No new long-lived caches or indexes; reuse
  git and px0's index. Bound every list (history 50, rows 300).
- **New dependencies need a measured reason.** No frameworks, no new runtime.

Measured costs of opt-in features (page side, after use):
- Terminal: xterm.js stays loaded after first use (~0.9 MB of script); the panel,
  screens and event stream are freed on close (`web/src/pxi-term.js`). xterm 5.5 leaves
  a window resize listener per terminal, which pxi removes itself.

## Rules

1. **New code goes in new files or folders**: `fork_*.go`, `worktrees.go`, new
   `web/src/*.js` modules, `macos/`. Upstream files get one-line hooks only, each
   marked `// fork:`. `git grep -n "fork:"` lists every hook.
2. **Branding is a script, not hand edits.** `scripts/pxi-brand.py` renames what users
   see (messages, page text, logos, `~/.pxi`, the `pxi` binary) and leaves code
   names, comments and the Go module as `px0`. Run it after every upstream merge;
   `--check` fails if anything is left to brand.
3. **Merge upstream weekly.** `.github/workflows/sync-upstream.yml` runs every Monday:
   merges px0 into `sync/upstream`, re-runs the brand script, builds, tests, and opens
   a pull request. A conflicting merge opens an issue listing the files.
4. **Prefer upstream's fix.** When upstream fixes something pxi patches, drop pxi's
   version in the sync pull request.
5. **Bun builds the web assets** (`bun scripts/build-web.js`, `bun test web/src/`).
   Bun's bundler keeps modules apart, but upstream's Node fallback shares one scope:
   keep top-level names in pxi's modules unique (prefix them) and put no comment after
   an `import` line, so the fallback still works if someone builds without Bun.
6. **README.md is pxi's own**: `.gitattributes` keeps it on merges (`merge=ours`).
   Locally, enable that once: `git config merge.ours.driver true`.

## Security fixes pxi carries (`fork_security.go`)

| Problem | Upstream | pxi fix |
|---|---|---|
| DNS rebinding: a website could read files, settings and tokens through GET endpoints | px0 issue #241, PR #244 (open) | `hostGuard` accepts only `localhost` or an IP as Host |
| Cross-site writes: any website could create or overwrite workspace files through `/api/file/save` and `/api/fs/new`, e.g. a `.git/hooks` script | not reported | `hostGuard` refuses non-GET requests from another origin |
| Symlink escape: a link inside the workspace could expose files outside it | px0 issue #186, PR #187 (open) | `safePath` checks the real path, including paths that do not exist yet and dangling links |

Behind a reverse proxy, send the server `Host: localhost:<port>` (or the IP and port).

## Upstream sync setup (one time, in GitHub settings)

1. **Actions** tab: enable workflows.
2. **Settings → Actions → General → Workflow permissions**: "Read and write
   permissions", and tick "Allow GitHub Actions to create and approve pull requests".
3. Run it once: **Actions → Sync upstream px0 → Run workflow**.
