# Maintaining pxi

pxi is built on [px0](https://github.com/px0-ai/px0) (MIT) and takes its fixes and
features every week. These rules keep that cheap.

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
5. **README.md is pxi's own**: `.gitattributes` keeps it on merges (`merge=ours`).
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
