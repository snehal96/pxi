# Worktrees

The **Worktrees** view (left activity bar) lists every git worktree of the open
repository, the main checkout first. Agents tend to run one per worktree; this view
is how you see all of them at once.

## What a card shows

- the branch, the commits it made since branching off its base (↑), and uncommitted changes
- every changed file with its status (added, modified, deleted)
- **Overlaps** (top of the view, collapsible): files changed in more than one
  worktree, which will conflict when both merge
- an **emptied** warning when a worktree's folder is missing most of its tracked
  files (an aborted checkout), instead of reporting hundreds of fake overlaps

Clicking a file opens its diff read-only, as `@wt/<worktree>/<path>`. Files of the
main checkout open at their real path and can be edited (`⌘E`).

## Actions

| Action | Where |
|---|---|
| Walkthrough of the change | ⚡ on the card |
| New worktree with Claude started on a task | `+` in the view header |
| Ship: commit, push, open a PR | the ship button on the card (asks that worktree's Claude) |
| Hide a worktree you don't care about | right-click → Hide (Show again from the same menu) |
| Copy its path | right-click → Copy Path |
| Delete a worktree | right-click → Delete Worktree… |

Delete refuses the main checkout and locked worktrees, and asks again when the
worktree has uncommitted changes that would be lost. A worktree whose folder was
already removed by hand is pruned. The branch is kept.

Hidden worktrees are remembered per repository in this browser profile.

## Terminals and Claude per worktree

The terminal (`⌃\``) and Claude sessions can start in any worktree, so commands and
agents run where the change lives.
