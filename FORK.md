# pxi

pxi is a fork of [px0](https://github.com/px0-ai/px0) (MIT, © Arpit Bhayani) that
stays in step with upstream. It adds what our team needs on top: reviewing every git
worktree of a repository in one window, a terminal, Claude Code sessions, and a Mac app.

## Rules that keep upstream merges easy

1. **New code goes in new files or folders** (`macos/` holds the Mac app) (`fork_*.go`, `worktrees.go`, new `web/src/*.js`
   modules). Upstream files get one-line hooks only, each marked `// fork:`.
   `git grep -n "fork:"` lists every hook.
2. **Merge upstream weekly.** `.github/workflows/sync-upstream.yml` does it every Monday:
   it merges `px0-ai/px0` into `sync/upstream`, builds and tests it, and opens a pull
   request. A conflicting merge opens an issue with the files instead.
3. **Prefer upstream's fix.** When upstream fixes something the fork patches, drop the
   fork's version in the sync pull request.

## Security fixes carried by the fork (`fork_security.go`)

| Problem | Upstream | Fork fix |
|---|---|---|
| DNS rebinding: a website could read files, settings and tokens through GET endpoints | issue #241, PR #244 (open) | `hostGuard` accepts only `localhost` or an IP as Host |
| Cross-site writes: any website could create or overwrite workspace files through `/api/file/save` and `/api/fs/new`, e.g. a `.git/hooks` script | not reported | `hostGuard` refuses non-GET requests from another origin |
| Symlink escape: a link inside the workspace could expose files outside it | issue #186, PR #187 (open) | `safePath` checks the real path, including paths that do not exist yet and dangling links |

Behind a reverse proxy, send px0 `Host: localhost:<port>` (or the upstream IP and port).

## Upstream sync setup (one time, in the fork's GitHub settings)

1. **Actions** tab: enable workflows for the fork.
2. **Settings → Actions → General → Workflow permissions**: "Read and write permissions",
   and tick "Allow GitHub Actions to create and approve pull requests".
3. Run it once by hand: **Actions → Sync upstream px0 → Run workflow**.
