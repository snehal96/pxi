// web/src/pxi-harness.js
// px0's harness menus (Threads, AI edits, commit messages, PR merge) list one
// "claude". pxi lists each logged-in Claude account instead, named after the
// shell alias that runs it (fork_claude_harness.go finds and checks them).
// Every entry keeps the value "claude", so px0 still runs its claude harness;
// picking one also tells the server which account to run it as.
// Top-level names are prefixed pxh (Node fallback bundler).
import { api, apiPostJson } from './state.js';

let pxhAccounts = null; // [{id, label, email, org, plan}]
let pxhSelected = '';
let pxhLoading = null;

function pxhLoad() {
  pxhLoading ||= api('/api/pxi/claude/harness-accounts')
    .then(j => { pxhAccounts = j.accounts || []; pxhSelected = j.selected || ''; })
    .catch(() => { pxhAccounts = []; });
  return pxhLoading;
}

function pxhOnChange(e) {
  const opt = e.target.selectedOptions[0];
  if (!opt?.dataset.acct) return;
  pxhSelected = opt.dataset.acct;
  apiPostJson('/api/pxi/claude/harness-accounts', { account: pxhSelected }).catch(() => {});
  for (const o of document.querySelectorAll('select option[data-acct]')) {
    if (o.parentElement !== e.target && o.parentElement.value === 'claude') o.selected = o.dataset.acct === pxhSelected;
  }
}

function pxhApply() {
  if (!pxhAccounts?.length) return;
  for (const opt of document.querySelectorAll('select option[value="claude"]:not([data-acct])')) {
    const sel = opt.parentElement;
    const was = opt.selected;
    opt.replaceWith(...pxhAccounts.map(a => {
      const o = document.createElement('option');
      o.value = 'claude';
      o.dataset.acct = a.id;
      o.textContent = a.label;
      o.title = [a.email, a.org, a.plan].filter(Boolean).join(' · ');
      o.selected = was && a.id === pxhSelected;
      return o;
    }));
    if (!sel.dataset.pxh) {
      sel.dataset.pxh = '1';
      sel.addEventListener('change', pxhOnChange);
    }
  }
}

/** Called after px0 rebuilds its harness menus (agent.js applyAgentMeta). */
export function pxiClaudeAccountOptions() {
  if (pxhAccounts) pxhApply();
  else pxhLoad().then(pxhApply);
}
