// pxi site: copy keys, the banner that retracts on scroll, and the hero's field:
// agent edits as raw diff glyphs on the left, resolving into reviewed rows on the right.
(() => {
  for (const b of document.querySelectorAll('[data-copy]')) {
    b.addEventListener('click', async () => {
      const text = document.getElementById(b.dataset.copy)?.textContent || '';
      try { await navigator.clipboard.writeText(text); b.textContent = 'Copied'; b.classList.add('is-done'); }
      catch { b.textContent = 'Select it'; }
      setTimeout(() => { b.textContent = 'Copy'; b.classList.remove('is-done'); }, 1600);
    });
  }

  const head = document.querySelector('.masthead');
  const banner = head?.querySelector('.banner');
  if (head && banner) {
    head.style.setProperty('--banner-h', banner.offsetHeight + 'px');
    let ticking = false;
    addEventListener('scroll', () => {
      if (ticking) return;
      ticking = true;
      requestAnimationFrame(() => { head.classList.toggle('is-retracted', scrollY > 40); ticking = false; });
    }, { passive: true });
  }

  const hero = document.querySelector('.hero');
  const canvas = hero?.querySelector('.field');
  if (!canvas || !canvas.getContext) return;
  const ctx = canvas.getContext('2d');
  const css = getComputedStyle(document.documentElement);
  const tint = ['--color-glyph-1', '--color-glyph-2', '--color-glyph-3', '--color-glyph-4', '--color-accent-deep'].map(v => css.getPropertyValue(v).trim());
  const RAW = ' .:-=+*#%@';
  const CW = 8, CH = 16;
  let cols = 0, rows = 0, dpr = 1, t = 0, running = false, last = 0;
  const lens = { x: -1e4, y: -1e4 };
  // Cheap value noise.
  const h = (x, y) => { const s = Math.sin(x * 127.1 + y * 311.7) * 43758.5453; return s - Math.floor(s); };
  const noise = (x, y) => {
    const xi = Math.floor(x), yi = Math.floor(y), xf = x - xi, yf = y - yi;
    const u = xf * xf * (3 - 2 * xf), v = yf * yf * (3 - 2 * yf);
    const a = h(xi, yi), b = h(xi + 1, yi), c = h(xi, yi + 1), d = h(xi + 1, yi + 1);
    return a + (b - a) * u + (c - a) * v + (a - b - c + d) * u * v;
  };
  function size() {
    dpr = Math.min(devicePixelRatio || 1, 2);
    const r = hero.getBoundingClientRect();
    canvas.width = Math.round(r.width * dpr); canvas.height = Math.round(r.height * dpr);
    cols = Math.ceil(r.width / CW); rows = Math.ceil(r.height / CH);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.font = '12px "JetBrains Mono", ui-monospace, monospace';
    ctx.textBaseline = 'top';
  }
  function frame() {
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    const cx = cols / 2, cy = rows / 2;
    for (let y = 0; y < rows; y++) {
      for (let x = 0; x < cols; x++) {
        const fx = x / cols;                       // 0 raw (left) .. 1 reviewed (right)
        // Keep the reading column quiet.
        const dx = (x - cx) / (cols * 0.26), dy = (y - cy) / (rows * 0.42);
        const quiet = Math.min(1, Math.max(0, Math.hypot(dx, dy) - 0.55));
        if (quiet <= 0) continue;
        let n = noise(x * 0.09 - t * 0.6 * (1 - fx), y * 0.18 + t * 0.15);
        const lx = x * CW - lens.x, ly = y * CH - lens.y;
        n += Math.max(0, 1 - Math.hypot(lx, ly) / 140) * 0.35;
        let ch;
        if (fx < 0.5) {
          ch = RAW[Math.min(RAW.length - 1, Math.floor(n * n * RAW.length * 1.4 * quiet))];
        } else {
          // Reviewed side: edits settle into ruled rows, every other line a diff mark.
          const settle = (fx - 0.5) * 2;
          const row = y % 3 === 0;
          if (n * quiet < 0.25 + settle * 0.2) continue;
          ch = row ? (x % 2 ? '-' : '+') : (n > 0.72 ? '=' : '.');
        }
        if (ch === ' ') continue;
        const lvl = Math.min(4, Math.floor(n * quiet * 5));
        ctx.fillStyle = tint[lvl];
        ctx.fillText(ch, x * CW, y * CH);
      }
    }
  }
  function loop(now) {
    if (!running) return;
    if (now - last > 33) { t += 0.033; frame(); last = now; }
    requestAnimationFrame(loop);
  }
  const still = matchMedia('(prefers-reduced-motion: reduce)');
  size(); frame();
  hero.classList.add('has-field');
  addEventListener('resize', () => { size(); frame(); });
  hero.addEventListener('pointermove', e => { const r = hero.getBoundingClientRect(); lens.x = e.clientX - r.left; lens.y = e.clientY - r.top; if (still.matches) frame(); });
  hero.addEventListener('pointerleave', () => { lens.x = lens.y = -1e4; });
  new IntersectionObserver(([e]) => {
    running = e.isIntersecting && !still.matches;
    if (running) requestAnimationFrame(loop);
  }).observe(hero);
  document.fonts?.ready.then(() => { size(); frame(); });
})();
