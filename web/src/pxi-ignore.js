// web/src/pxi-ignore.js
// pxi: the explorer's right-click menu ignores a file or folder, shared through
// .gitignore or only on this Mac through the repository's info/exclude, or
// stops ignoring it. A rule wider than the item (tmp/, *.log) is not edited:
// pxi names it and opens it, since deleting it would un-ignore other files.
// Server side: fork_gitignore.go. Adds no DOM until the menu opens.
import { S, apiPostJson } from './state.js';
import { showToast } from './ui.js';
import { openFile } from './tabs.js';
import { refreshTree } from './tree.js';
import { triggerRefresh } from './gitstream.js';

export function ignMenuItems(row) {
  if (!S.meta?.git) return [];
  if (row.classList.contains('ignored')) {
    return [{ action: 'pxi-unignore', label: 'Stop Ignoring' }];
  }
  return [
    { action: 'pxi-ignore', label: 'Add to .gitignore' },
    { action: 'pxi-ignore-local', label: 'Ignore on This Mac Only' },
  ];
}

const ignAction = { 'pxi-ignore': 'add', 'pxi-ignore-local': 'local', 'pxi-unignore': 'remove' };

function ignWhere(rule) {
  if (rule.local) return 'on this Mac only (.git/info/exclude)';
  if (rule.outside) return 'by your global git excludes file';
  return 'by "' + rule.pattern + '" in ' + rule.file + ', line ' + rule.line;
}

// Handles one of the actions above; false for any other.
export function ignMenuRun(act, target) {
  const action = ignAction[act];
  if (!action) return false;
  ignDo(action, target);
  return true;
}

async function ignDo(action, { path, name }) {
  let r;
  try {
    r = await apiPostJson('/api/pxi/gitignore', { path, action });
  } catch (e) {
    showToast('!', e.message || 'Could not change the ignore rules', 4000);
    return;
  }
  if (r.changed) {
    await refreshTree();
    triggerRefresh();
  }
  if (action === 'remove') {
    if (!r.ignored) {
      showToast('✓', name + ' is no longer ignored');
    } else if (!r.changed && r.rule) {
      showToast('!', name + ' is ignored ' + ignWhere(r.rule) + '. That rule covers other files too, so edit it there.', 7000);
      if (r.rule.file) openFile(r.rule.file, { line: r.rule.line });
    } else if (r.rule) {
      showToast('!', 'Still ignored ' + ignWhere(r.rule), 6000);
    }
    return;
  }
  const where = action === 'local' ? 'on this Mac only' : 'in .gitignore';
  showToast('✓', r.tracked
    ? 'Ignored ' + where + '. ' + name + ' is already committed, so git keeps tracking it until you remove it from the repository.'
    : name + ' ignored ' + where, r.tracked ? 7000 : 2200);
}
