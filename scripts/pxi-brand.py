#!/usr/bin/env python3
"""Rebrand what users see from px0 to pxi.

Only text a person reads changes: Go and JavaScript string literals, HTML text and
attributes, the build scripts' binary name, and the settings/state folders
(~/.px0 -> ~/.pxi). Code identifiers, comments, the Go module path, browser
storage keys and internal markers keep px0's names, so upstream px0 changes
still merge cleanly. Logos and favicons that px0 loads from px0.ai become a
built-in pxi wordmark, so the app makes no requests to px0.ai.

Safe to run again: run it after every upstream merge to brand new strings.
    python3 scripts/pxi-brand.py          # rewrite files
    python3 scripts/pxi-brand.py --check  # exit 1 if anything would change
"""
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
REPO = "snehal96/pxi"

# Never renamed: the Go module and dependency, git refs px0 already wrote into
# users' repositories, internal markers, and px0.<key>/px0-<key> names (browser
# storage keys, temp-file prefixes, the child-ready marker).
PROTECT = re.compile(
    r"px0/internal|px0-ai/harness|refs/px0/|px0\.invalid"
    r"|px0\.(?!ai\b)[A-Za-z]|px0-(?!ai\b)[A-Za-z]"
)
WORD = re.compile(r"\bpx0\b")


def brand(s: str) -> str:
    s = s.replace("https://github.com/px0-ai/px0", f"https://github.com/{REPO}")
    s = s.replace("px0-ai/px0", REPO).replace("pxi-ai/pxi", REPO)
    s = re.sub(r"https://px0\.ai/?(?=[\"'\s>])", f"https://github.com/{REPO}", s)
    s = s.replace(" from px0.ai", "")  # "Reload tips from px0.ai" -> "Reload tips"
    s = s.replace('title="px0.ai"', 'title="pxi on GitHub"')
    out, last = [], 0
    for m in PROTECT.finditer(s):
        out.append(WORD.sub("pxi", s[last:m.start()]))
        out.append(m.group(0))
        last = m.end()
    out.append(WORD.sub("pxi", s[last:]))
    return "".join(out)


# ---------------------------------------------------------------- logos

WORDMARK = (
    '<svg class="pxi-wordmark" width="{w}" height="{h}" viewBox="0 0 48 18" role="img" aria-label="pxi">'
    '<text x="1" y="14" font-family="ui-monospace, SFMono-Regular, Menlo, monospace" font-size="16" '
    'font-weight="700" fill="currentColor">px<tspan fill="#5bcc8f">i</tspan></text></svg>'
)
FAVICON = (
    '<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,'
    "%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'%3E%3Crect width='32' height="
    "'32' rx='7' fill='%231c2130'/%3E%3Cg fill='none' stroke-width='2.4' stroke-linecap='round'"
    "%3E%3Cpath d='M8 16C14 16 15.5 8 23.5 8' stroke='%234ade80'/%3E%3Cpath d='M8 16H23.5' stro"
    "ke='%2360a5fa'/%3E%3Cpath d='M8 16C14 16 15.5 24 23.5 24' stroke='%23fbbf24'/%3E%3C/g%3E%3"
    "Ccircle cx='8' cy='16' r='3.2' fill='%23e8ebf2'/%3E%3Ccircle cx='24' cy='8' r='3' fill='%2"
    "34ade80'/%3E%3Ccircle cx='24' cy='16' r='3' fill='%2360a5fa'/%3E%3Ccircle cx='24' cy='24' "
    "r='3' fill='%23fbbf24'/%3E%3C/svg%3E"
    '">'
)


def logos(s: str) -> str:
    s = re.sub(r'<link rel="icon" type="image/x-icon" href="https://px0\.ai/favicon\.ico">', FAVICON, s)
    s = re.sub(r'[ \t]*<link rel="icon" type="image/png" sizes="\d+x\d+" href="https://px0\.ai/favicon-\d+x\d+\.png">\n', "", s)
    return re.sub(
        r'<img class="logo-dark" src="https://px0\.ai/logo/px0-logo-dark\.png" alt="px0" width="(\d+)" height="(\d+)">'
        r'\s*<img class="logo-light" src="https://px0\.ai/logo/px0-logo-light\.png"[^>]*>',
        lambda m: WORDMARK.format(w=m.group(1), h=m.group(2)),
        s,
    )


# ---------------------------------------------------------------- tokenizers


def map_go_strings(src: str, fn) -> str:
    """Apply fn to the body of every Go string literal ("..." and `...`)."""
    out, i, n = [], 0, len(src)
    while i < n:
        c = src[i]
        if src.startswith("//", i):
            j = src.find("\n", i)
            j = n if j < 0 else j
            out.append(src[i:j]); i = j
        elif src.startswith("/*", i):
            j = src.find("*/", i + 2)
            j = n if j < 0 else j + 2
            out.append(src[i:j]); i = j
        elif c == "`":
            j = src.find("`", i + 1)
            j = n if j < 0 else j
            out.append("`" + fn(src[i + 1:j]) + "`"); i = j + 1
        elif c in "\"'":
            j = i + 1
            while j < n and src[j] != c and src[j] != "\n":
                j += 2 if src[j] == "\\" else 1
            if j >= n:
                out.append(src[i:]); break
            out.append(c + (fn(src[i + 1:j]) if c == '"' else src[i + 1:j]) + c); i = j + 1
        else:
            out.append(c); i += 1
    return "".join(out)


def map_js_strings(src: str, fn) -> str:
    """Apply fn to the body of every JavaScript string and template literal.
    Regex literals are recognised by the token before the slash."""
    out, i, n = [], 0, len(src)
    prev = ""  # last significant character
    while i < n:
        c = src[i]
        if src.startswith("//", i):
            j = src.find("\n", i)
            j = n if j < 0 else j
            out.append(src[i:j]); i = j
        elif src.startswith("/*", i):
            j = src.find("*/", i + 2)
            j = n if j < 0 else j + 2
            out.append(src[i:j]); i = j
        elif c == "/" and (prev == "" or prev in "(,=:[!&|?{};+-*%<>~^"):
            j, in_class = i + 1, False
            while j < n and src[j] != "\n":
                if src[j] == "\\":
                    j += 2; continue
                if src[j] == "[": in_class = True
                elif src[j] == "]": in_class = False
                elif src[j] == "/" and not in_class: break
                j += 1
            out.append(src[i:j + 1]); i = j + 1; prev = "x"
        elif c in "\"'`":
            j = i + 1
            while j < n and src[j] != c and (c == "`" or src[j] != "\n"):
                j += 2 if src[j] == "\\" else 1
            if j >= n:
                out.append(src[i:]); break
            out.append(c + fn(src[i + 1:j]) + c); i = j + 1; prev = "x"
        else:
            out.append(c)
            if not c.isspace():
                prev = c
            i += 1
    return "".join(out)


def map_html_text(src: str, fn) -> str:
    """Apply fn outside <!-- comments -->."""
    parts = re.split(r"(<!--.*?-->)", src, flags=re.S)
    return "".join(p if p.startswith("<!--") else fn(p) for p in parts)


# ---------------------------------------------------------------- one-off edits

TIPS_GUARD = "  const url = S.settings?.['tips.url'] || TIPS_URL;\n  if (!url) { updateTipsCountBadge(); return loadedTips; }\n"


# pxi's own themes (web/themes/pxi-*.css) are the default and listed first.
THEME_RULES = {
    "internal/settings/settings.go": [
        ('Default:     "catppuccin-mocha",', 'Default:     "pxi-dark",'),
        ('"catppuccin-mocha", "catppuccin-latte",', '"pxi-dark", "pxi-light", "catppuccin-mocha", "catppuccin-latte",'),
    ],
    "internal/settings/settings_test.go": [
        ('m["workbench.colorTheme"] != "catppuccin-mocha"', 'm["workbench.colorTheme"] != "pxi-dark"'),
    ],
    "web/src/settings.js": [
        ('default: "catppuccin-mocha",', 'default: "pxi-dark",'),
        ('"catppuccin-mocha", "catppuccin-latte",', '"pxi-dark", "pxi-light", "catppuccin-mocha", "catppuccin-latte",'),
    ],
    "workbench.go": [('\treturn "catppuccin-mocha"\n', '\treturn "pxi-dark"\n')],
    "web/src/theme-preference.js": [("export const DEFAULT_THEME = 'catppuccin-mocha';", "export const DEFAULT_THEME = 'pxi-dark';")],
    "web/index.html": [
        ('<html lang="en" data-theme="catppuccin-mocha">', '<html lang="en" data-theme="pxi-dark">'),
        # pxi's VS Code-style chrome (web/pxi/shell.css), after the themes it reads.
        ('<link rel="stylesheet" href="static/themes.css">\n', '<link rel="stylesheet" href="static/themes.css">\n<link rel="stylesheet" href="static/pxi/shell.css">\n'),
    ],
}


# pxi builds the web assets with Bun (scripts, Make, CI, Docker).
BUN_RULES = {
    "scripts/build-web.js": [
        ("#!/usr/bin/env node\n", "#!/usr/bin/env bun\n"),
        ("import zlib from 'node:zlib';\n", "import zlib from 'node:zlib';\n\n" + "// pxi: builds run on Bun, which has no --check flag; parse with its transpiler.\nfunction checkSyntax(file) {\n  if (typeof Bun !== 'undefined') {\n    new Bun.Transpiler({ loader: 'js' }).transformSync(fs.readFileSync(file, 'utf8'));\n    return;\n  }\n  execFileSync(process.execPath, ['--check', file], { stdio: 'pipe' });\n}\n"),
        ("execFileSync(process.execPath, ['--check', outFile], { stdio: 'pipe' });", "checkSyntax(outFile);"),
    ],
    "scripts/check-web.js": [
        ("#!/usr/bin/env node\n", "#!/usr/bin/env bun\n"),
        ("import { execFileSync } from 'node:child_process';\n", "import { execFileSync } from 'node:child_process';\n\n" + "// pxi: checks run on Bun, which has no --check flag; parse with its transpiler.\nfunction checkSyntax(file) {\n  if (typeof Bun !== 'undefined') {\n    new Bun.Transpiler({ loader: 'js' }).transformSync(fs.readFileSync(file, 'utf8'));\n    return;\n  }\n  execFileSync(process.execPath, ['--check', file], { stdio: 'pipe' });\n}\n"),
        ("execFileSync('node', ['--check', filePath], { stdio: 'pipe' });", "checkSyntax(filePath);"),
        ("execFileSync('node', ['--check', bundlePath], { stdio: 'pipe' });", "checkSyntax(bundlePath);"),
    ],
    "package.json": [
        ('"build": "node ./scripts/build-web.js"', '"build": "bun ./scripts/build-web.js"'),
        ('"build-web": "node ./scripts/build-web.js"', '"build-web": "bun ./scripts/build-web.js"'),
        ('"check": "node ./scripts/check-web.js"', '"check": "bun ./scripts/check-web.js"'),
    ],
    "Makefile": [
        ("@node ./scripts/build-web.js", "@bun ./scripts/build-web.js"),
        ("@node ./scripts/check-web.js", "@bun ./scripts/check-web.js"),
    ],
    "scripts/test-bench.sh": [("  node scripts/build-web.js", "  bun scripts/build-web.js")],
    "Dockerfile": [
        ("FROM node:20-alpine AS web-builder", "FROM oven/bun:1.3.14-alpine AS web-builder"),
        ("RUN node ./scripts/build-web.js", "RUN bun ./scripts/build-web.js"),
    ],
    ".github/workflows/ci.yml": [("      - name: Setup Node.js\n        uses: actions/setup-node@v4\n        with:\n          node-version: '20'\n", '      - name: Setup Bun\n        uses: oven-sh/setup-bun@v2\n        with:\n          bun-version: 1.3.14\n'), ("run: node ./scripts/build-web.js", "run: bun ./scripts/build-web.js")],
    ".github/workflows/release.yml": [("      - name: Setup Node.js\n        uses: actions/setup-node@v4\n        with:\n          node-version: '20'\n", '      - name: Setup Bun\n        uses: oven-sh/setup-bun@v2\n        with:\n          bun-version: 1.3.14\n'), ("          node ./scripts/build-web.js", "          bun ./scripts/build-web.js")],
}


def themes(path: str, s: str) -> str:
    for a, b in BUN_RULES.get(path, []):
        if b not in s:
            s = s.replace(a, b)
    for a, b in THEME_RULES.get(path, []):
        if b not in s:
            s = s.replace(a, b)
    return s


def special(path: str, s: str) -> str:
    if path == "web/src/tips-util.js":
        s = s.replace("export const TIPS_URL = 'https://px0.ai/tips.json';", "export const TIPS_URL = ''; // pxi: no remote tips")
    if path == "web/src/tips.js":
        s = s.replace("showToast('i', 'Fetching latest tips from px0.ai...');", "showToast('i', 'Reloading tips...');")
        if TIPS_GUARD not in s:
            s = s.replace("  const url = S.settings?.['tips.url'] || TIPS_URL;\n", TIPS_GUARD, 1)
    return s


# Tests keep upstream's fixtures (URLs, owners); only what they assert about
# pxi's own names changes: state folders, the error prefix, update asset names.
TEST_RULES = [
    ('"px0", "settings.json"', '"pxi", "settings.json"'),
    ('"px0", "sessions"', '"pxi", "sessions"'),
    ('"px0", "update_check.json"', '"pxi", "update_check.json"'),
    ('".px0"', '".pxi"'),
    ("echo 'px0: px0: ", "echo 'pxi: pxi: "),
    ('"px0-0.2.0', '"pxi-0.2.0'),
]


def test_file(path: str, s: str) -> str:
    s = themes(path, s)
    for a, b in TEST_RULES:
        s = s.replace(a, b)
    return s


def go_file(path: str, s: str) -> str:
    return map_go_strings(themes(path, s), lambda b: brand(logos(b)))


def js_file(path: str, s: str) -> str:
    return map_js_strings(special(path, themes(path, s)), brand)


def html_file(path: str, s: str) -> str:
    return map_html_text(logos(themes(path, s)), brand)


def plain_file(path: str, s: str) -> str:
    return brand(themes(path, s))


def rules_file(path: str, s: str) -> str:
    return themes(path, s)


# Files that must name px0 on purpose (reading px0's old settings folder).
SKIP = {"fork_brand.go", "fork_brand_test.go"}


def targets():
    files = subprocess.run(["git", "ls-files"], cwd=ROOT, capture_output=True, text=True, check=True).stdout.split()
    for f in files:
        if f in SKIP:
            continue
        if f.endswith("_test.go"):
            yield f, test_file
            continue
        if f.endswith(".test.js"):
            continue
        if f.endswith(".go"):
            yield f, go_file
        elif f.startswith("web/src/") and f.endswith(".js"):
            yield f, js_file
        elif f in ("web/index.html", "web/workbench.html"):
            yield f, html_file
        elif f in ("Makefile", "scripts/replace-local.sh"):
            yield f, plain_file
        elif f in BUN_RULES:
            yield f, rules_file


def main():
    check = "--check" in sys.argv
    changed = []
    for rel, fn in targets():
        p = ROOT / rel
        old = p.read_text()
        new = fn(rel, old)
        if new != old:
            changed.append(rel)
            if not check:
                p.write_text(new)
    for c in changed:
        print(("would change " if check else "branded ") + c)
    if check and changed:
        sys.exit(1)


if __name__ == "__main__":
    main()
