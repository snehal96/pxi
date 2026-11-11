#!/usr/bin/env bash
# How many times each pxi release was downloaded (get.sh and the website both
# download pxi-mac.zip from the release). Needs only curl and python3.
set -euo pipefail
curl -fsSL "https://api.github.com/repos/snehal96/pxi/releases?per_page=100" | python3 -c '
import json, sys
total = 0
for r in json.load(sys.stdin):
    for a in r["assets"]:
        if a["name"] == "pxi-mac.zip":
            total += a["download_count"]
            print(f"{r[\"tag_name\"]:<16} {a[\"download_count\"]:>6}  ({r[\"published_at\"][:10]})")
print(f"{\"total\":<16} {total:>6}")
'
