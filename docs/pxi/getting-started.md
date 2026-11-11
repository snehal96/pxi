# Getting started

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/snehal96/pxi/master/macos/get.sh | bash
```

This puts the latest pxi.app in `/Applications` and opens it. Run it again to update,
then **pxi → Restart pxi** (`⇧⌥⌘R`). To build from source instead, see the
[README](../../README.md#install).

pxi finds `git`, `gh` and `claude` the way your terminal does: it starts with your
login shell's environment.

## Open a folder

- **File → Open Folder…** (`⌘O`), or the **Open Folder…** button on the start page
- drop a folder on the Dock icon
- `open -a pxi ~/code/project` from a terminal

Recent and pinned folders stay on the start page (**Back to workbench**, `⌘⇧H`).

## The layout

pxi follows VS Code's layout:

- **Left activity bar:** Explorer, Source Control, Worktrees, Remote (SSH), Claude, and
  AI Review. Click the active icon again to hide the sidebar.
- **Right activity bar:** Threads, References, Symbols, Calls, Search.
- **Status bar:** branch, terminal, review checkpoint, CPU and memory.
- **Command palette:** `⌘⇧P`. Quick Open: `⌘P` (current and recent files first).

## A first review

1. Open the repository your agents work in.
2. Open **Worktrees**: one card per worktree with its changed files; red marks a file
   that more than one worktree changed.
3. Click ⚡ on a card for a walkthrough of that change, or click a file for its diff.
4. Mark files viewed (`✓ Viewed`), and send feedback to that worktree's Claude with
   `⌥K` on the lines you are commenting on.
5. When it is right, **Ship** asks that worktree's Claude to commit, push and open a
   pull request.

Next: [Reviewing agent changes](review.md).
