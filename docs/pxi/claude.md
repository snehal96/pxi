# Claude in pxi

pxi uses the `claude` command you already have and your own Claude subscription. No
API key is needed.

## Claude sessions (`⌘Esc`)

The **Claude** icon (left activity bar) opens Claude Code in an editor tab, for the
repository or any of its worktrees. Edits show as diffs to accept or reject; your
open file and selection go along with each message. `claude` typed in pxi's terminal
connects to pxi the way it connects to VS Code.

The **Processes** list shows what each session and terminal started (dev servers,
test runs) with ports, CPU and memory, and stops them.

When a session needs you or finishes, pxi shows a Mac notification and a Dock badge.

## Threads (`⌥T`)

Threads (right activity bar) are conversations with a headless coding agent that can
read and change files, with a diff per turn and the option to revert a turn. Pick the
agent in the menu; **claude** runs Claude Code on your subscription.

## Several Claude accounts

If you use more than one Claude account, pxi lists each logged-in one as its own
entry in the agent menu. It finds them from:

- aliases in your shell files, such as
  `alias claude-work='CLAUDE_CONFIG_DIR=~/.claude-work claude'`
  (the entry is named after the alias)
- `~/.claude-*` folders
- the default `~/.claude`

Each is checked once with `claude auth status`, which reads the login without starting
a session or using your plan; an account that is logged out is not listed. The choice
applies to every repository, and a thread keeps the account it started with.
