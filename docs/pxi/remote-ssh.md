# Remote (SSH)

Browse a folder on another machine and read its files, without installing anything
there.

## Connect

1. Click the **Remote** icon (monitor) in the left activity bar.
2. **Host**: a name from your `~/.ssh/config` (suggested as you type) or `user@host`.
3. **Folder to open**: for example `~/code/project`. Leave it empty for your home
   folder on that machine.
4. **Connect**.

Expand folders and click a file to open it read-only, with syntax highlighting.

## Change the folder

- Right-click any folder → **Open as Remote Folder**
- ↑ on the host row: the parent folder
- ✎ on the host row: type another folder

Each replaces the saved remote rather than adding another.

## Staying connected

Remotes are saved (`remotes.json` next to `~/.pxi/settings.json`) and come back after
a restart. All requests to a host share one ssh connection, kept open for 30 minutes
after last use and checked every 15 seconds; if it drops, the next click reconnects.

## Limits

- Your SSH keys or ssh-agent only. Password-only logins are refused.
- A host you have never connected to: run `ssh <host>` once in a terminal to accept
  its key. pxi does not skip host key checking.
- Read-only: no editing, search, git or diffs for remote files.
- Files over 2 MB and binary files do not open; folders list up to 3,000 entries.
