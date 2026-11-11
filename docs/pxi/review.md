# Reviewing agent changes

## Diffs

Any changed file opens as a diff (split or unified, `⌘D` to switch). Each change block
has **↕ Expand** (15 more lines above and below), **Stage**, **Unstage** and
**Discard**. `[c` / `]c` step through change blocks, `[f` / `]f` through changed files,
and **✓ Viewed** marks a file done and moves to the next.

## Review checkpoint

For "what did the agent do since I last looked?":

1. `⌘⇧P` → **Review: Set Checkpoint**.
2. Let the agents work.
3. Every file added (green dot), changed (blue dot) or deleted since then is marked
   in the explorer, with its folders. The status bar shows the count
   (`3 since 14:02`); click it to open each changed file in turn.
4. **Review: Clear Checkpoint** when you are done, or set it again to move it to now.

Changes are compared by content, so a file saved without changes is not marked, and a
change that was committed stays marked. pxi stores one git snapshot of your working
tree (including untracked files, excluding ignored ones) and never touches your
staging area or branch. The checkpoint lasts until pxi restarts.

## Walkthrough

**AI Review** (left activity bar) runs your agent through review stages and shows a
walkthrough: what the change does, file by file, with the definitions and removed
checks that matter. ⚡ on a worktree card does the same for that worktree.

## Review → agent

Select lines in any file or diff (or put the caret on one) and press `⌥K`, or
right-click → **Send to Claude**. Type your comment; the quoted lines, file and line
numbers go to the Claude session of the worktree the file belongs to.

## Pull requests

`pxi https://github.com/owner/repo/pull/123`, or paste a PR link on the start page.
GitHub, GitLab, Gitea/Forgejo and Bitbucket Cloud are supported; see px0's
[pull request guide](../features/github-pr-review.md).
