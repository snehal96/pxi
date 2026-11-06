# pxi

A fast code review workbench for the age of coding agents. pxi runs as a native Mac
app (or in any browser), opens huge repositories instantly, and keeps the loop of
reading, reviewing and pushing agent-written changes in one place.

- **Native Mac app**: a small Swift shell around a single Go binary. No Electron.
- **Fast**: starts in milliseconds; files with 400,000+ lines scroll smoothly.
- **Review workbench**: open any folder or pull request (GitHub, GitLab,
  Gitea/Forgejo, Bitbucket Cloud), draft review comments, merge.
- **Git built in**: diffs, staging, commit, push and pull, unpushed-commit review.
- **Agents**: send a selection or a batch of comments to Claude Code, Codex,
  Cursor Agent and others, and follow the conversation in threads.
- **Navigation**: fuzzy file search (`Cmd+P`), symbols (`Cmd+Shift+O`), workspace
  search (`Cmd+Shift+F`), optional language servers.

### Coming next

- Every git worktree of a repository in one window, with each one's changes, so
  parallel agents working on different tickets are easy to review.
- A built-in terminal and Claude Code sessions inside pxi.
- See and stop the processes an agent session started.
- A new look.

## Install (Mac app)

Needs Go, Bun and the Xcode command line tools.

```bash
git clone git@github.com:snehal96/pxi.git && cd pxi
macos/build.sh --install      # builds pxi.app into /Applications
```

Open folders from **File → Open Folder…** (`Cmd+O`), by dropping a folder on the
Dock icon, or with `open -a pxi ~/code/project`. See [macos/README.md](macos/README.md).

## Install (command line)

```bash
make build                     # builds ./pxi
install -d ~/.local/bin && install pxi ~/.local/bin/
pxi                            # opens the workbench in your browser
pxi ~/code/project             # opens one folder
pxi https://github.com/owner/repo/pull/123
```

Settings live in `~/.pxi/settings.json`.

## Security

pxi listens on `127.0.0.1` only. It refuses requests addressed to another host name
(DNS rebinding), refuses writes from other websites, and never follows a symlink out
of the folder you opened.

## Acknowledgements

pxi includes [px0](https://github.com/px0-ai/px0) by Arpit Bhayani, used under the
MIT License; see [LICENSE](LICENSE).
