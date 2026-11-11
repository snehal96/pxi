// web/src/pxi-edit.js
// Edit (⌘E) on a file opened from the Worktrees view (@wt/<name>/<path>):
// those views are read-only, so px0's edit mode cannot load them. A file of the
// open checkout itself is opened at its real path and edited there; a file of
// another worktree gets a message instead of "Failed to fetch".
// Top-level names are prefixed pxe (Node fallback bundler).
import { showToast } from './ui.js';
import { openFile } from './tabs.js';
import { enterEditMode } from './editview.js';
import { worktreeIsMain } from './pxi-worktrees.js';

/** Returns true when it handled the edit request for d. */
export function pxiEditRedirect(d, line) {
  if (d?.path?.startsWith('@ssh/')) {
    showToast('!', 'Files on an SSH remote are read-only in pxi.', 4000);
    return true;
  }
  if (!d?.path?.startsWith('@wt/')) return false;
  const rest = d.path.slice(4);
  const cut = rest.indexOf('/');
  const name = cut < 0 ? rest : rest.slice(0, cut);
  const inner = cut < 0 ? '' : rest.slice(cut + 1);
  if (inner && worktreeIsMain(name)) {
    openFile(inner, { view: 'source', line }).then(() => enterEditMode(line));
  } else {
    showToast('!', 'Files in the ' + name + ' worktree are read-only here. Open ' + name + ' as a folder in its own window to edit them.', 5000);
  }
  return true;
}
