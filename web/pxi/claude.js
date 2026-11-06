// web/pxi/claude.js: pxi's Claude panel, loaded on first use by
// web/src/pxi-claude.js (which passes `ctx`). Server: fork_claude.go.
//
// Left: the repo's places (workspace + worktrees) and their sessions, read
// from the chosen account's transcripts. Right: one session's conversation,
// streamed, with permission requests (edit diffs, commands, plans, questions)
// answered inline. Everything here is dropped on unmount.
(() => {
  'use strict';

  const MODES = [
    ['default', 'Ask before edits'],
    ['acceptEdits', 'Edit automatically'],
    ['plan', 'Plan mode'],
    ['auto', 'Auto'],
  ];
  const MODELS = [['', 'Default model'], ['opus', 'Opus'], ['sonnet', 'Sonnet'], ['haiku', 'Haiku']];
  const PREF = 'pxi.claude.';
  const pref = (k, d) => { try { return localStorage.getItem(PREF + k) ?? d; } catch { return d; } };
  const setPref = (k, v) => { try { localStorage.setItem(PREF + k, v); } catch {} };

  const I = {
    plus: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"><path d="M8 3v10M3 8h10"/></svg>',
    branch: '<svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.3"><circle cx="4.5" cy="3.5" r="1.5"/><circle cx="4.5" cy="12.5" r="1.5"/><circle cx="11.5" cy="5.5" r="1.5"/><path d="M4.5 5v6M11.5 7c0 3-4 3-7 4.2"/></svg>',
    repo: '<svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.3"><path d="M3 2.5h8.5v11H4.5A1.5 1.5 0 0 1 3 12z"/><path d="M3 12a1.5 1.5 0 0 1 1.5-1.5h7"/></svg>',
    send: '<svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor"><path d="M2 2.5l12 5.5-12 5.5 1.8-5.5z"/></svg>',
    stop: '<svg viewBox="0 0 16 16" width="12" height="12" fill="currentColor"><rect x="3.5" y="3.5" width="9" height="9" rx="1.5"/></svg>',
    file: '<svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.3"><path d="M4 1.8h5.2L12.5 5v9.2H4z"/><path d="M9 1.8V5h3.5"/></svg>',
    x: '<svg viewBox="0 0 10 10" width="9" height="9" fill="none" stroke="currentColor" stroke-width="1.4"><path d="M2 2l6 6M8 2l-6 6"/></svg>',
    chev: '<svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 4l4 4-4 4"/></svg>',
    term: '<svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><rect x="1.5" y="2.5" width="13" height="11" rx="1.5"/><path d="M4.5 6l2 2-2 2M8.5 10.5h3"/></svg>',
    side: '<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.3"><rect x="1.5" y="2.5" width="13" height="11" rx="1.5"/><path d="M6 2.5v11"/></svg>',
  };
  const TOOL_VERB = { Read: 'Read', Edit: 'Edit', MultiEdit: 'Edit', Write: 'Write', Bash: 'Run', Grep: 'Search', Glob: 'Find files', WebFetch: 'Fetch', WebSearch: 'Web search', Task: 'Agent', Agent: 'Agent', TodoWrite: 'To-dos', NotebookEdit: 'Edit notebook' };

  let ctx = null, root = null, els = {};
  let accounts = [], account = '', places = [];
  let placesLoad = null; // the first session list: paths in events map through it
  // The open conversation.
  let cur = null; // { where, session, live, busy, mode, model, es }
  let tools = new Map();   // tool_use id -> row element
  let asks = new Map();    // request id -> card element
  let stream = null, streamText = '';
  let mention = null;      // @-mention popup state
  let attach = true;       // send the editor context with the next message

  const esc = s => ctx.esc(String(s ?? ''));
  const h = (html) => { const t = document.createElement('template'); t.innerHTML = html.trim(); return t.content.firstChild; };
  const ago = ms => {
    const s = Math.max(1, Math.round((Date.now() - ms) / 1000));
    if (s < 60) return 'now';
    if (s < 3600) return Math.round(s / 60) + 'm';
    if (s < 86400) return Math.round(s / 3600) + 'h';
    return Math.round(s / 86400) + 'd';
  };
  const placeOf = where => places.find(p => p.where === (where || '')) || null;
  const sessionsOf = p => (p && p.sessions) || [];
  const placeName = where => (where === '@terminal' ? 'a terminal' : where ? where : (places[0]?.name || 'workspace'));

  // An absolute path from Claude, as a path pxi can open.
  function toPxiPath(abs) {
    if (!abs || abs[0] !== '/') return abs;
    let best = null;
    for (const p of places) if (p.path && (abs === p.path || abs.startsWith(p.path + '/')) && (!best || p.path.length > best.path.length)) best = p;
    if (!best) return null;
    const rel = abs.slice(best.path.length + 1);
    return best.where ? '@wt/' + best.where + '/' + rel : rel;
  }
  // A pxi path as Claude should see it (absolute).
  function toAbs(p) {
    const m = /^@wt\/([^/]+)\/(.*)$/.exec(p || '');
    if (m) { const pl = placeOf(m[1]); return pl ? pl.path + '/' + m[2] : p; }
    return (ctx.root() || '') + '/' + p;
  }
  const shortPath = abs => { const p = toPxiPath(abs); return p ? p.replace(/^@wt\/[^/]+\//, '') : abs; };

  // ------------------------------------------------------------ layout

  function mount(el, c) {
    ctx = c; root = el;
    root.innerHTML =
      '<div class="cl-wrap' + (pref('side', '1') === '1' ? '' : ' cl-noside') + '">' +
      '<aside class="cl-side">' +
      '  <div class="cl-side-head"><span class="cl-title">CLAUDE</span><span class="grow"></span>' +
      '    <button class="cl-ib" data-act="new" title="New session">' + I.plus + '</button></div>' +
      '  <label class="cl-account"><span>Account</span><select data-act="account"></select></label>' +
      '  <div class="cl-places"></div>' +
      '</aside>' +
      '<section class="cl-main">' +
      '  <header class="cl-head"><button class="cl-ib" data-act="side" title="Show or hide sessions">' + I.side + '</button>' +
      '    <span class="cl-htitle"></span><span class="cl-where"></span><span class="grow"></span><span class="cl-state"></span>' +
      '    <button class="cl-btn cl-ghost" data-act="stop" title="End this session\'s process and everything it started" hidden>End session</button></header>' +
      '  <div class="cl-log" tabindex="-1"></div>' +
      '  <div class="cl-compose">' +
      '    <div class="cl-ctx" hidden></div>' +
      '    <textarea rows="1" placeholder="Ask Claude…  (@ to mention a file, Enter to send, Shift+Enter for a new line)"></textarea>' +
      '    <div class="cl-mention" hidden></div>' +
      '    <div class="cl-bar"><select data-act="mode" title="Permission mode"></select><select data-act="model" title="Model"></select>' +
      '      <span class="grow"></span><span class="cl-hint"></span>' +
      '      <button class="cl-send" data-act="send" title="Send (Enter)">' + I.send + '</button>' +
      '      <button class="cl-send cl-interrupt" data-act="interrupt" title="Stop Claude (Esc)" hidden>' + I.stop + '</button></div>' +
      '  </div>' +
      '</section></div>';
    const q = s => root.querySelector(s);
    els = {
      wrap: q('.cl-wrap'), places: q('.cl-places'), account: q('[data-act="account"]'), log: q('.cl-log'),
      title: q('.cl-htitle'), where: q('.cl-where'), state: q('.cl-state'), stop: q('[data-act="stop"]'),
      input: q('textarea'), ctx: q('.cl-ctx'), mention: q('.cl-mention'), mode: q('[data-act="mode"]'),
      model: q('[data-act="model"]'), send: q('[data-act="send"]'), interrupt: q('[data-act="interrupt"]'), hint: q('.cl-hint'),
    };
    els.mode.innerHTML = MODES.map(([v, l]) => '<option value="' + v + '">' + l + '</option>').join('');
    els.model.innerHTML = MODELS.map(([v, l]) => '<option value="' + v + '">' + l + '</option>').join('');
    els.mode.value = pref('mode', 'default');
    els.model.value = pref('model', '');
    root.addEventListener('click', onClick);
    root.addEventListener('change', onChange);
    els.input.addEventListener('keydown', onKey);
    els.input.addEventListener('input', onInput);
    root.addEventListener('keydown', onPaneKey);
    placesLoad = loadAccounts();
    newSession('');
  }

  function unmount() {
    closeStream();
    root?.replaceChildren();
    root = null; els = {}; tools.clear(); asks.clear(); places = []; cur = null; stream = null;
  }

  async function loadAccounts() {
    try {
      const j = await ctx.api('/api/pxi/claude/accounts');
      accounts = j.accounts || [];
      account = j.current || '';
      if (!j.installed) notice('Claude Code is not installed: no `claude` command was found. Install it, then reopen this tab.', true);
      else if (!accounts.length) notice('No Claude login found. Run `claude` once in a terminal (⌃`) and log in.', true);
      els.account.innerHTML = accounts.map(a => '<option value="' + esc(a.id) + '">' + esc(a.label) + (a.email ? ' · ' + esc(a.email) : '') + '</option>').join('');
      els.account.value = account;
      els.account.title = accounts.find(a => a.id === account)?.org || '';
    } catch (e) { notice(e.message, true); }
    return loadSessions();
  }

  async function loadSessions() {
    if (!root) return;
    try {
      const j = await ctx.api('/api/pxi/claude/sessions', { account });
      places = j.places || [];
    } catch (e) { places = []; }
    drawPlaces();
    drawHead();
  }

  function drawPlaces() {
    if (!els.places) return;
    els.places.innerHTML = places.map(p => {
      const sel = cur && !cur.session && cur.where === p.where;
      const term = p.where === '@terminal';
      return '<div class="cl-place">' +
        (term ? '<div class="cl-place-head cl-term-head" title="claude started in a terminal, connected to pxi">' + I.term + '<span class="cl-pname">' + esc(p.name) + '</span></div>' :
        '<div class="cl-place-head' + (sel ? ' sel' : '') + '" data-new="' + esc(p.where) + '" title="New session in ' + esc(p.path) + '">' +
        (p.where ? I.branch : I.repo) + '<span class="cl-pname">' + esc(p.name) + '</span>' +
        (p.branch && p.branch !== p.name ? '<span class="cl-pbranch">' + esc(p.branch) + '</span>' : '') +
        '<span class="grow"></span><span class="cl-plus">' + I.plus + '</span></div>') +
        sessionsOf(p).map(s => {
          const state = s.asking ? 'asking' : s.busy ? 'busy' : s.live ? 'live' : '';
          return '<div class="cl-sess' + (cur?.session === s.id ? ' sel' : '') + '" data-sess="' + esc(s.id) + '" data-where="' + esc(p.where) + '" title="' + esc(s.title) + '">' +
            '<span class="cl-dot ' + state + '" title="' + (state === 'asking' ? 'Waiting for you' : state === 'busy' ? 'Working' : state === 'live' ? 'Running' : '') + '"></span>' +
            '<span class="cl-stitle">' + esc(s.title || 'New session') + '</span><span class="cl-sago">' + ago(s.updated) + '</span></div>';
        }).join('') +
        (sessionsOf(p).length ? '' : '<div class="cl-none">No sessions yet</div>') +
        '</div>';
    }).join('');
  }

  function drawHead() {
    if (!cur || !els.title) return;
    const s = cur.session && sessionsOf(placeOf(cur.where)).find(x => x.id === cur.session);
    els.title.textContent = cur.session ? (s?.title || cur.title || 'Session') : 'New session';
    const p = placeOf(cur.where);
    els.where.innerHTML = (cur.where === '@terminal' ? I.term : cur.where ? I.branch : I.repo) + '<span>' + esc(placeName(cur.where)) + '</span>';
    root.querySelector('.cl-compose').hidden = !!cur.ide;
    els.where.title = p?.path || '';
    els.stop.hidden = !cur.live || !!cur.ide;
    els.state.textContent = cur.asking ? 'Waiting for you' : cur.busy ? 'Working…' : '';
    els.state.className = 'cl-state' + (cur.asking ? ' asking' : cur.busy ? ' busy' : '');
    els.send.hidden = !!cur.busy;
    els.interrupt.hidden = !cur.busy;
    els.hint.textContent = cur.busy ? 'Esc to stop' : '';
    drawCtx();
  }

  // ------------------------------------------------------------ sessions

  function closeStream() {
    if (cur?.es) { cur.es.close(); cur.es = null; }
  }

  function resetLog() {
    tools.clear(); asks.clear(); stream = null; streamText = '';
    els.log.replaceChildren();
  }

  function newSession(where) {
    closeStream();
    cur = { where: where || '', session: '', live: false, busy: false, asking: 0 };
    resetLog();
    els.log.append(h('<div class="cl-empty"><div class="cl-empty-mark">✳</div><div class="cl-empty-t">Claude in ' + esc(placeName(cur.where)) + '</div>' +
      '<div class="cl-empty-s">Runs your own <code>claude</code> with the account above. Edits wait for your OK unless you pick <b>Edit automatically</b>.<br>' +
      'Your open file and selection go along with the message.</div></div>'));
    drawPlaces(); drawHead();
    focus();
  }

  async function openSession(id, where) {
    closeStream();
    cur = { where: where || '', session: id, live: false, busy: false, asking: 0, ide: id.startsWith('ide-') };
    resetLog();
    drawPlaces(); drawHead();
    els.log.append(h('<div class="cl-faint">Loading…</div>'));
    let hist = [];
    try { await placesLoad; } catch {}
    try { hist = (await ctx.api('/api/pxi/claude/history', { account, session: id, where: cur.where })).events || []; } catch (e) { notice(e.message, true); }
    if (!cur || cur.session !== id) return;
    resetLog();
    if (cur.ide) els.log.append(h('<div class="cl-notice">This is <code>claude</code> running in a terminal and connected to pxi. The edits it proposes appear here to accept or reject (or answer in the terminal); it sees the file and selection open in pxi. Talk to it in the terminal.</div>'));
    for (const ev of hist) apply(ev, true);
    finishStream();
    listen();
    scrollEnd(true);
  }

  function listen() {
    if (!cur?.session) return;
    const es = new EventSource('api/pxi/claude/events?session=' + encodeURIComponent(cur.session));
    cur.es = es;
    const mine = cur;
    es.onmessage = e => {
      if (cur !== mine) return;
      let ev; try { ev = JSON.parse(e.data); } catch { return; }
      apply(ev, false);
    };
  }

  // ------------------------------------------------------------ events

  function atEnd() { const l = els.log; return l.scrollHeight - l.scrollTop - l.clientHeight < 80; }
  function scrollEnd(force) { if (force || atEnd()) requestAnimationFrame(() => { if (els.log) els.log.scrollTop = els.log.scrollHeight; }); }

  function apply(ev, history) {
    const stick = history || atEnd();
    switch (ev.k) {
      case 'user': finishStream(); addUser(ev.text, ev.ctx); break;
      case 'delta': addDelta(ev.text); break;
      case 'text': finishStream(ev.text); break;
      case 'tool': finishStream(); addTool(ev); break;
      case 'result': toolResult(ev); break;
      case 'ask': finishStream(); addAsk(ev); cur.asking = asks.size; break;
      case 'answered': if (ev.elsewhere) { const c = asks.get(ev.req); if (c && !c.dataset.done) markAnswered(c, false, 'Answered in the terminal'); } answered(ev); cur.asking = [...asks.values()].filter(a => !a.dataset.done).length; if (ev.mode) setMode(ev.mode); break;
      case 'init': cur.live = true; if (ev.mode) setMode(ev.mode); break;
      case 'busy': cur.busy = !!ev.busy; cur.live = !!ev.live || cur.live; if (ev.mode) setMode(ev.mode); break;
      case 'done':
        finishStream(); cur.busy = false;
        els.log.append(h('<div class="cl-done' + (ev.ok ? '' : ' err') + '">' + (ev.ok ? 'Done' : 'Stopped') + (ev.ms ? ' · ' + (ev.ms / 1000).toFixed(ev.ms < 10000 ? 1 : 0) + 's' : '') + (ev.error ? ' · ' + esc(ev.error) : '') + '</div>'));
        if (!history) loadSessions();
        break;
      case 'limit': notice('Usage limit: ' + esc(ev.status) + (ev.resets ? ', resets ' + new Date(ev.resets * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : ''), true); break;
      case 'notice': notice(esc(ev.text)); break;
      case 'exit':
        finishStream(); cur.live = false; cur.busy = false;
        if (cur.ide) notice('The terminal claude disconnected.');
        if (ev.code && ev.error) notice('claude exited: ' + esc(ev.error), true);
        for (const a of asks.values()) if (!a.dataset.done) markAnswered(a, false, 'Session ended');
        if (!history) loadSessions();
        break;
      case 'idle': break;
    }
    if (!history) { drawHead(); if (stick) scrollEnd(true); }
  }

  function notice(html, warn) {
    els.log?.append(h('<div class="cl-notice' + (warn ? ' warn' : '') + '">' + html + '</div>'));
  }

  function addUser(text, c) {
    els.log.querySelector('.cl-empty')?.remove();
    els.log.append(h('<div class="cl-user">' + (c ? '<div class="cl-uctx">' + I.file + esc(c) + '</div>' : '') + (text.includes('```') ? '<div class="cl-utext cl-text">' + ctx.md(text) + '</div>' : '<div class="cl-utext">' + esc(text) + '</div>') + '</div>'));
  }

  function addDelta(t) {
    if (!stream) {
      stream = h('<div class="cl-text cl-streaming"></div>');
      els.log.append(stream);
      streamText = '';
    }
    streamText += t;
    stream.textContent = streamText;
  }

  function finishStream(final) {
    if (final !== undefined) {
      const el = stream || h('<div class="cl-text"></div>');
      if (!stream) els.log.append(el);
      el.classList.remove('cl-streaming');
      el.innerHTML = ctx.md(final);
      linkPaths(el);
    } else if (stream) {
      stream.classList.remove('cl-streaming');
      stream.innerHTML = ctx.md(streamText);
      linkPaths(stream);
    }
    stream = null; streamText = '';
  }

  // Code spans that name a file in this repo open it.
  function linkPaths(el) {
    for (const code of el.querySelectorAll('code')) {
      if (code.closest('pre')) continue;
      const m = /^([\w./@-]+\.[\w]+)(?::(\d+))?$/.exec(code.textContent);
      if (m) { code.classList.add('cl-path'); code.dataset.path = m[1]; if (m[2]) code.dataset.line = m[2]; }
    }
  }

  function diffHTML(diff) {
    if (!diff) return '';
    let a = 0, d = 0;
    const rows = diff.replace(/\n$/, '').split('\n').map(l => {
      const c = l[0] === '+' ? 'add' : l[0] === '-' ? 'del' : l.startsWith('@@') ? 'hunk' : '';
      if (c === 'add') a++; else if (c === 'del') d++;
      return '<div class="cl-dl ' + c + '">' + esc(l || ' ') + '</div>';
    }).join('');
    return { html: '<div class="cl-diff">' + rows + '</div>', a, d };
  }

  function addTool(ev) {
    const verb = TOOL_VERB[ev.name] || ev.name;
    const isFile = ['Read', 'Edit', 'MultiEdit', 'Write', 'NotebookEdit'].includes(ev.name);
    const label = isFile ? shortPath(ev.label) : ev.label;
    let detail = '';
    if (ev.name === 'Bash' && ev.input?.command) detail = '<pre class="cl-cmd">' + esc(ev.input.command) + '</pre>';
    if (ev.name === 'TodoWrite' && Array.isArray(ev.input?.todos)) {
      detail = '<ul class="cl-todos">' + ev.input.todos.map(t => '<li class="' + esc(t.status) + '">' + esc(t.content) + '</li>').join('') + '</ul>';
    }
    const row = h('<div class="cl-tool' + (detail && ev.name === 'TodoWrite' ? ' open' : '') + '" data-tool="' + esc(ev.name) + '">' +
      '<div class="cl-tool-head"><span class="cl-chev">' + I.chev + '</span><span class="cl-tverb">' + esc(verb) + '</span>' +
      '<span class="cl-tlabel' + (isFile ? ' cl-path' : '') + '"' + (isFile ? ' data-path="' + esc(toPxiPath(ev.label) || '') + '"' : '') + '>' + esc(label || '') + '</span>' +
      '<span class="cl-tstat"></span></div><div class="cl-tool-body">' + detail + '</div></div>');
    els.log.append(row);
    if (ev.id) tools.set(ev.id, row);
  }

  function toolResult(ev) {
    const row = tools.get(ev.id);
    if (!row) return;
    row.classList.toggle('err', !!ev.error);
    const body = row.querySelector('.cl-tool-body');
    const stat = row.querySelector('.cl-tstat');
    if (ev.diff) {
      const d = diffHTML(ev.diff);
      stat.innerHTML = '<span class="add">+' + d.a + '</span> <span class="del">−' + d.d + '</span>';
      body.insertAdjacentHTML('beforeend', d.html);
    } else if (ev.text && row.dataset.tool !== 'Read') {
      body.insertAdjacentHTML('beforeend', '<pre class="cl-out">' + esc(ev.text) + '</pre>');
    }
    if (ev.error) stat.innerHTML = '<span class="del">failed</span>';
  }

  function addAsk(ev) {
    els.log.querySelector('.cl-empty')?.remove();
    if (asks.has(ev.req)) return;
    let title = '', body = '', extra = '';
    const path = ev.input?.file_path ? shortPath(ev.input.file_path) : '';
    switch (ev.tool) {
      case 'Edit': case 'MultiEdit': case 'Write': {
        title = (ev.tool === 'Write' ? 'Write ' : 'Edit ') + '<span class="cl-path" data-path="' + esc(toPxiPath(ev.input?.file_path) || '') + '">' + esc(path) + '</span>';
        const d = diffHTML(ev.diff || '');
        body = d ? d.html : '<div class="cl-faint">No preview available.</div>';
        if (d) title += ' <span class="cl-tstat"><span class="add">+' + d.a + '</span> <span class="del">−' + d.d + '</span></span>';
        if (!cur.ide) extra = '<button class="cl-btn" data-ans="all" title="Accept, and accept the rest of this session\'s edits without asking">Accept all edits</button>';
        break;
      }
      case 'Bash':
        title = 'Run a command' + (ev.input?.description ? ': ' + esc(ev.input.description) : '');
        body = '<pre class="cl-cmd">' + esc(ev.input?.command || '') + '</pre>';
        break;
      case 'ExitPlanMode':
        title = 'Claude\'s plan';
        body = '<div class="cl-plan">' + ctx.md(ev.input?.plan || '') + '</div>';
        break;
      case 'AskUserQuestion': {
        title = 'Claude has a question';
        const qs = ev.input?.questions || [];
        body = qs.map((q, qi) => '<div class="cl-q" data-qi="' + qi + '" data-multi="' + (q.multiSelect ? 1 : 0) + '"><div class="cl-qt">' + esc(q.question) + '</div>' +
          (q.options || []).map(o => '<button class="cl-opt" data-label="' + esc(o.label) + '"><b>' + esc(o.label) + '</b>' + (o.description ? '<span>' + esc(o.description) + '</span>' : '') + '</button>').join('') +
          '<input class="cl-other" placeholder="Other…"></div>').join('');
        break;
      }
      default:
        title = esc(TOOL_VERB[ev.tool] || ev.tool) + (ev.label ? ' <span class="cl-faint">' + esc(ev.label) + '</span>' : '');
        body = ev.input ? '<pre class="cl-out">' + esc(JSON.stringify(ev.input, null, 2)) + '</pre>' : '';
    }
    const plan = ev.tool === 'ExitPlanMode', question = ev.tool === 'AskUserQuestion';
    const card = h('<div class="cl-ask" data-req="' + esc(ev.req) + '" data-tool="' + esc(ev.tool) + '">' +
      '<div class="cl-ask-head">' + title + '</div><div class="cl-ask-body">' + body + '</div>' +
      '<div class="cl-ask-bar">' +
      (question ? '<button class="cl-btn cl-primary" data-ans="answer">Answer</button>' :
        '<button class="cl-btn cl-primary" data-ans="allow" title="⌘Enter">' + (plan ? 'Approve plan' : ev.tool === 'Bash' ? 'Run' : 'Accept') + '</button>' + extra) +
      '<button class="cl-btn cl-ghost" data-ans="deny">' + (plan ? 'Keep planning' : question ? 'Skip' : 'Reject') + '</button>' +
      '<input class="cl-reason" placeholder="' + (plan ? 'What to change in the plan…' : 'Tell Claude what to do instead (optional)') + '">' +
      '</div></div>');
    els.log.append(card);
    asks.set(ev.req, card);
    // The card shows this call; its plain row would repeat it.
    const row = ev.tid && tools.get(ev.tid);
    if (row) row.hidden = true;
    if (!document.hidden && root && !root.hidden) card.querySelector('.cl-primary')?.focus({ preventScroll: true });
  }

  function markAnswered(card, allow, text) {
    card.dataset.done = '1';
    card.classList.add('answered', allow ? 'allowed' : 'denied');
    card.querySelector('.cl-ask-bar').innerHTML = '<span class="cl-verdict">' + esc(text || (allow ? 'Accepted' : 'Rejected')) + '</span>';
  }

  function answered(ev) {
    const card = asks.get(ev.req);
    if (card && !card.dataset.done) markAnswered(card, ev.allow);
  }

  async function answer(card, how) {
    if (!cur?.session || card.dataset.done) return;
    const req = card.dataset.req;
    const body = { session: cur.session, req, allow: how !== 'deny', message: card.querySelector('.cl-reason')?.value || '' };
    if (how === 'all') { body.mode = 'acceptEdits'; }
    if (how === 'answer') {
      const answers = {};
      for (const q of card.querySelectorAll('.cl-q')) {
        const text = q.querySelector('.cl-qt').textContent;
        const picked = [...q.querySelectorAll('.cl-opt.on')].map(b => b.dataset.label);
        const other = q.querySelector('.cl-other').value.trim();
        if (other) picked.push(other);
        answers[text] = picked.join(', ');
      }
      body.answers = answers;
    }
    card.querySelectorAll('button').forEach(b => { b.disabled = true; });
    try {
      await ctx.apiPostJson('/api/pxi/claude/answer', body);
      const plan = card.dataset.tool === 'ExitPlanMode';
      if (plan && body.allow) setMode('default'); // Claude leaves plan mode once the plan is approved
      markAnswered(card, body.allow, how === 'all' ? 'Accepted · edits now automatic' : how === 'answer' ? 'Answered' :
        plan ? (body.allow ? 'Plan approved' : 'Sent back with your notes') : null);
    } catch (e) {
      card.querySelectorAll('button').forEach(b => { b.disabled = false; });
      ctx.toast('!', e.message);
    }
  }

  function setMode(m) { if (els.mode && m && els.mode.value !== m) els.mode.value = m; }

  // ------------------------------------------------------------ composer

  function drawCtx() {
    const c = ctx.context();
    if (!c || !attach) {
      els.ctx.hidden = !c;
      els.ctx.innerHTML = c ? '<button class="cl-chip off" data-act="attach" title="Send with the open file">' + I.file + esc(c.path.replace(/^@wt\/[^/]+\//, '')) + '</button>' : '';
      return;
    }
    const label = c.path.replace(/^@wt\/[^/]+\//, '') + (c.sel ? ':' + c.sel.l1 + (c.sel.l2 !== c.sel.l1 ? '-' + c.sel.l2 : '') : '');
    els.ctx.hidden = false;
    els.ctx.innerHTML = '<span class="cl-chip" title="Sent with your message: ' + (c.sel ? 'the selected lines' : 'which file is open') + '">' + I.file + esc(label) +
      '<button class="cl-chip-x" data-act="detach" title="Don\'t send">' + I.x + '</button></span>';
  }

  function contextBlock() {
    const c = attach ? ctx.context() : null;
    if (!c) return { text: '', label: '' };
    const abs = toAbs(c.path);
    const label = c.path.replace(/^@wt\/[^/]+\//, '') + (c.sel ? ':' + c.sel.l1 + (c.sel.l2 !== c.sel.l1 ? '-' + c.sel.l2 : '') : '');
    if (c.sel) {
      return { label, text: '<ide_selection>The user selected the lines ' + c.sel.l1 + ' to ' + c.sel.l2 + ' from ' + abs + ':\n' + c.sel.text + '\n\nThis may or may not be related to the current task.</ide_selection>\n' };
    }
    return { label, text: '<ide_opened_file>The user opened the file ' + abs + ' in the IDE. This may or may not be related to the current task.</ide_opened_file>\n' };
  }

  async function send() {
    const text = els.input.value.trim();
    if (!text || !cur) return;
    const c = contextBlock();
    const body = { account, where: cur.where, session: cur.session, text: c.text + text, ctx: c.label, model: els.model.value, mode: els.mode.value };
    els.input.value = '';
    autosize();
    els.send.disabled = true;
    try {
      const j = await ctx.apiPostJson('/api/pxi/claude/send', body);
      if (!cur.session) {
        cur.session = j.session;
        cur.title = text.slice(0, 90);
        els.log.querySelector('.cl-empty')?.remove();
        listen();
        loadSessions();
      }
      cur.busy = true; cur.live = true;
      ctx.watch();
      drawHead();
    } catch (e) {
      els.input.value = text;
      notice(esc(e.message), true);
    } finally {
      els.send.disabled = false;
    }
  }

  async function control(action, value) {
    if (!cur?.session) return;
    try { await ctx.apiPostJson('/api/pxi/claude/control', { session: cur.session, action, value: value || '' }); } catch (e) { ctx.toast('!', e.message); }
  }

  function autosize() {
    const t = els.input;
    t.style.height = 'auto';
    t.style.height = Math.min(t.scrollHeight, 240) + 'px';
  }

  // @-mentions: files from pxi's index.
  let mentionSeq = 0;
  async function onInput() {
    autosize();
    const t = els.input, upto = t.value.slice(0, t.selectionStart);
    const m = /(^|\s)@([\w./-]*)$/.exec(upto);
    if (!m) { hideMention(); return; }
    const q = m[2], seq = ++mentionSeq;
    let files = [];
    try { files = ((await ctx.api('/api/find', { q: q || '.', limit: 8 })).results || []); } catch {}
    if (seq !== mentionSeq) return;
    const list = files.map(f => (typeof f === 'string' ? f : f.path)).filter(Boolean).slice(0, 8);
    if (!list.length) { hideMention(); return; }
    mention = { start: upto.length - q.length - 1, list, i: 0 };
    drawMention();
  }
  function drawMention() {
    els.mention.hidden = false;
    els.mention.innerHTML = mention.list.map((p, i) => '<div class="cl-mi' + (i === mention.i ? ' on' : '') + '" data-mi="' + i + '">' + I.file + '<span>' + esc(p.split('/').pop()) + '</span><span class="cl-faint">' + esc(p) + '</span></div>').join('');
  }
  function hideMention() { mention = null; if (els.mention) { els.mention.hidden = true; els.mention.replaceChildren(); } }
  function pickMention(i) {
    const p = mention.list[i], t = els.input;
    const before = t.value.slice(0, mention.start), after = t.value.slice(t.selectionStart);
    const ins = '@' + p + ' '; // relative: Claude resolves it in its own folder (the worktree's copy)
    t.value = before + ins + after;
    t.selectionStart = t.selectionEnd = before.length + ins.length;
    hideMention();
    t.focus();
  }
  function onKey(e) {
    if (mention) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); mention.i = (mention.i + (e.key === 'ArrowDown' ? 1 : -1) + mention.list.length) % mention.list.length; drawMention(); return; }
      if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); pickMention(mention.i); return; }
      if (e.key === 'Escape') { e.preventDefault(); hideMention(); return; }
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); send(); }
  }

  function onPaneKey(e) {
    if (e.key === 'Escape' && !e.metaKey && !e.ctrlKey && cur?.busy && !mention) { e.preventDefault(); control('interrupt'); return; }
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      const card = [...asks.values()].find(a => !a.dataset.done);
      if (card) { e.preventDefault(); answer(card, card.dataset.tool === 'AskUserQuestion' ? 'answer' : 'allow'); }
    }
  }

  // ------------------------------------------------------------ clicks

  function onClick(e) {
    const t = e.target;
    const ans = t.closest('[data-ans]');
    if (ans) { answer(ans.closest('.cl-ask'), ans.dataset.ans); return; }
    const opt = t.closest('.cl-opt');
    if (opt) {
      const q = opt.closest('.cl-q');
      if (q.dataset.multi !== '1') q.querySelectorAll('.cl-opt').forEach(b => b.classList.remove('on'));
      opt.classList.toggle('on');
      return;
    }
    const mi = t.closest('[data-mi]');
    if (mi) { pickMention(+mi.dataset.mi); return; }
    const path = t.closest('.cl-path[data-path]');
    if (path && path.dataset.path) {
      const p = toPxiPath(path.dataset.path) || path.dataset.path;
      ctx.openPath(p, path.dataset.line ? +path.dataset.line : 0);
      return;
    }
    const head = t.closest('.cl-tool-head');
    if (head) { head.parentElement.classList.toggle('open'); return; }
    const sess = t.closest('[data-sess]');
    if (sess) { openSession(sess.dataset.sess, sess.dataset.where); return; }
    const nw = t.closest('[data-new]');
    if (nw) { newSession(nw.dataset.new); return; }
    const act = t.closest('[data-act]')?.dataset.act;
    switch (act) {
      case 'new': newSession(cur?.where || ''); break;
      case 'send': send(); break;
      case 'interrupt': control('interrupt'); break;
      case 'stop': control('stop'); break;
      case 'detach': attach = false; drawCtx(); break;
      case 'attach': attach = true; drawCtx(); break;
      case 'side': els.wrap.classList.toggle('cl-noside'); setPref('side', els.wrap.classList.contains('cl-noside') ? '0' : '1'); break;
    }
  }

  async function onChange(e) {
    const act = e.target.dataset.act;
    if (act === 'account') {
      account = e.target.value;
      els.account.title = accounts.find(a => a.id === account)?.org || '';
      try { await ctx.apiPostJson('/api/pxi/claude/account', { account }); } catch (err) { ctx.toast('!', err.message); }
      newSession(cur?.where || '');
      loadSessions();
    } else if (act === 'mode') {
      setPref('mode', e.target.value);
      if (cur?.live) control('mode', e.target.value);
    } else if (act === 'model') {
      setPref('model', e.target.value);
      if (cur?.live) control('model', e.target.value);
    }
  }

  // ------------------------------------------------------------ API

  async function open(opts) {
    if (!root || !opts) return;
    if (opts.latest) {
      // The worktree's running session, else its latest, else a new one.
      try { await placesLoad; } catch {}
      if (!root) return;
      const list = sessionsOf(placeOf(opts.where));
      const pick = list.find(x => x.live) || list[0];
      if (pick) {
        if (!(cur && cur.session === pick.id)) await openSession(pick.id, opts.where);
      } else if (!(cur && !cur.session && cur.where === (opts.where || ''))) newSession(opts.where);
    } else if (opts.session) openSession(opts.session, opts.where);
    else if (opts.where !== undefined) newSession(opts.where);
    if (opts.text && els.input) {
      attach = false; // the lines are quoted in the message already
      const t = els.input;
      t.value = (t.value.trim() ? t.value.replace(/\s*$/, '\n\n') : '') + opts.text;
      autosize();
      drawCtx();
      requestAnimationFrame(() => { t.focus(); t.selectionStart = t.selectionEnd = t.value.length; t.scrollTop = t.scrollHeight; });
      return;
    }
    if (!opts.quiet) focus();
  }

  function focus() {
    if (!els.input) return;
    drawCtx();
    requestAnimationFrame(() => els.input?.focus());
  }

  // Status from the server: refresh the session list's dots.
  let runsSig = '';
  function runs(list) {
    const sig = JSON.stringify(list.map(r => [r.session, r.busy, r.asking]));
    if (sig === runsSig) return;
    runsSig = sig;
    if (root) loadSessions();
  }

  const current = () => cur?.session || '';
  window.pxiClaudePanel = { mount, unmount, open, focus, runs, current };
})();
