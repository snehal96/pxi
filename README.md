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
- **Worktrees**: every git worktree of a repository in one window, with what each one
  changed and the files more than one of them touches.
- **Source Control**: history, branches, stashes, remotes and tags.
- **Terminal** (`Ctrl+\``): shells in the workspace or any worktree, loaded only when
  opened.
- **Claude** (`Cmd+Esc`): Claude Code sessions in an editor tab, for the repo and each
  worktree, using your own `claude` login (pick the account per repo). Edits show as
  diffs to accept or reject; your open file and selection go along. `claude` typed in
  pxi's terminal connects to pxi like it does to VS Code: its edits open in the Claude
  tab to accept or reject, and it sees your selection.
- **Processes**: what each Claude session and terminal started (dev servers, test runs),
  with ports, CPU and memory, and Stop.
- **New worktree with Claude** (Worktrees `+`): a branch and worktree, with Claude started
  on the task. **Ship** on a worktree asks its Claude to commit, push and open a PR.
- **Notifications**: a Mac notification and Dock badge when a session needs you or
  finishes. **Chrome** switch: Claude can use your Chrome (Claude in Chrome).
- **Review → agent** (`Alt+K`, or right-click **Send to Claude**): quote lines from any
  file or worktree diff into that worktree's Claude session with your comment.

### Coming next

- A new look.

## Install (Mac app)

One command installs everything it needs (Xcode command line tools, Homebrew,
Go, Bun), builds pxi.app into `/Applications` and opens it:

```bash
bash -c "$(curl -fsSL https://raw.githubusercontent.com/snehal96/pxi/master/macos/install.sh)"
```

Run the same command again to update, then **pxi → Restart pxi**. The source lives
in `~/pxi`; from a clone, `macos/install.sh` does the same.

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
