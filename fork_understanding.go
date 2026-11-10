package main

// pxi's reading of a diff for the Review walkthrough (px0's review_workflow.go
// calls it from extractCodeUnderstanding). px0's rules are written for Go and
// JavaScript; these also read Python, TypeScript, Ruby and Java, and look at
// removed lines too:
//
//   - Returns: an `if` that bails out (return, continue, break, raise, throw)
//     within the next few lines, or on the same line. A plain `return` at the
//     end of a function is not a guard.
//   - Edge cases: raising or throwing an error, catching one (except, catch,
//     rescue), checking one (`if err != nil`), timeouts, panics and asserts.
//   - Removed checks: a guard, raise, catch or permission check that the change
//     deletes, unless the same line comes back elsewhere in the hunk (a move or
//     a re-indent). Listed with the edge cases, kind "removed".
//   - Core: new or changed function, method and class definitions. No filler
//     entry for files without one, so the Core count means something.

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"px0/internal/render"
)

var (
	pxuIf       = regexp.MustCompile(`^(?:\}\s*)?(?:if|elif|else\s+if|unless|guard)\b`)
	pxuBail     = regexp.MustCompile(`(?:^|[\s:;{)])(?:return|continue|break|raise|throw|abort|reject)\b|\bpanic\(|\bos\.Exit\(|\bsys\.exit\(|\bprocess\.exit\(`)
	pxuRaise    = regexp.MustCompile(`^(?:raise|throw)\b|\bthrow\s+new\b|\breject\(|\bpanic\(|\blog\.Fatal|\bt\.Fatal`)
	pxuReturnEr = regexp.MustCompile(`^return\b.*(?:\berr\b|Err[A-Z]\w*|errors\.New|fmt\.Errorf|Error\()`)
	pxuCatch    = regexp.MustCompile(`^(?:\}\s*)?(?:except\b.*:|catch\b|rescue\b|finally\b)|\.catch\(|\brecover\(\)`)
	pxuErrCheck = regexp.MustCompile(`\berr\s*!=\s*nil\b|\bif\s+.*\berror\b|\bis\s+Err\b`)
	pxuTimeout  = regexp.MustCompile(`(?i)\btime\s*out\b|\btimeout\b|ctx\.Done\(\)|DeadlineExceeded|context\.Canceled|\bexpired?\b`)
	pxuAssert   = regexp.MustCompile(`^assert\b|\bassert\(|\bpanic\(`)
	pxuAuth     = regexp.MustCompile(`(?i)auth|permission|forbidden|unauthori[sz]ed|\btoken\b|credential|denied|\brole\b|\bscope\b|\btenant\b|\bowner\b|\badmin\b|\bcan_\w+|\bhas_\w+`)
	pxuNil      = regexp.MustCompile(`(?i)==\s*nil|!=\s*nil|===?\s*(?:null|undefined)|!==?\s*(?:null|undefined)|\bis\s+(?:not\s+)?None\b|\bif\s+not\s+\w|\bif\s*\(\s*!\w|\.nil\?|\bnil\?`)
	pxuValid    = regexp.MustCompile(`(?i)valid|required|missing|empty|\blen\(|\.length\b|<=?\s*0\b|>=?\s*0\b|bounds|overflow|limit|\bmax\b|\bmin\b|isinstance|typeof`)
	pxuCache    = regexp.MustCompile(`(?i)cache|cached|memo|idempoten|already|existing|\bfound\b|\bseen\b`)
	pxuKeyword  = regexp.MustCompile(`^(?:\}\s*)?(?:if|else|for|while|switch|select|match|return|catch|try|do|with|await|go|defer)\b`)
	pxuDef      = regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:async\s+)?(?:func|def|function|class|interface|struct)\b|^(?:public|private|protected|static|override|async|\s)*\w[\w<>\[\],\s]*\s+\w+\s*\([^;]*\)\s*(?:throws\s+[\w.,\s]+)?\{\s*$|^(?:export\s+)?(?:const|let)\s+\w+\s*=\s*(?:async\s+)?(?:\([^)]*\)|\w+)\s*=>|^type\s+\w+\s+(?:struct|interface)\b`)
)

// pxuComment: lines that say nothing about control flow.
func pxuComment(t string) bool {
	return t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "/*") ||
		strings.HasPrefix(t, "*") || strings.HasPrefix(t, `"""`) || strings.HasPrefix(t, "'''") || strings.HasPrefix(t, "--")
}

func pxuItem(line int, kind, text, expl string) CodeInsightItem {
	return CodeInsightItem{Line: line, Kind: kind, Text: truncateInsightText(text, 90), Explanation: expl}
}

// pxuGuardKind names what an `if` that bails out is checking.
func pxuGuardKind(cond, bail string) (kind, expl string) {
	raises := pxuRaise.MatchString(bail)
	switch {
	case pxuAuth.MatchString(cond):
		kind, expl = "auth", "Access check: stops callers who may not do this"
	case pxuNil.MatchString(cond):
		kind, expl = "nil_check", "Missing-value check: stops before using something that is not there"
	case pxuCache.MatchString(cond):
		kind, expl = "cache", "Shortcut: the work is already done or cached"
	case pxuValid.MatchString(cond):
		kind, expl = "validation", "Input check: rejects values outside what is allowed"
	default:
		kind, expl = "guard", "Early exit when the condition holds"
	}
	if raises {
		expl += " (raises an error)"
	}
	return
}

type pxuRow struct {
	t    string // trimmed text
	line int    // line in the new file to point at
}

// pxiExtractUnderstanding replaces px0's extractor. It never returns nil.
func pxiExtractUnderstanding(rel string, hunks []render.DiffHunk) *ReviewUnderstanding {
	und := &ReviewUnderstanding{FastReturns: []CodeInsightItem{}, EdgeCases: []CodeInsightItem{}, CoreLogic: []CodeInsightItem{}}
	adds, dels := 0, 0
	for _, h := range hunks {
		var added, removed []pxuRow
		addedText := map[string]bool{}
		removedText := map[string]bool{}
		next := h.NewStart // the new-file line a removed row sits before
		for _, r := range h.Rows {
			t := strings.TrimSpace(r.Text)
			switch r.Type {
			case "add":
				added = append(added, pxuRow{t, r.NewLine})
				addedText[t] = true
				next = r.NewLine + 1
			case "del":
				removed = append(removed, pxuRow{t, next})
				removedText[t] = true
			default:
				if r.NewLine > 0 {
					next = r.NewLine + 1
				}
			}
		}
		adds += len(added)
		dels += len(removed)
		pxuScan(und, added, removedText, false)
		pxuScan(und, removed, addedText, true)
	}

	if adds == 0 && dels == 0 {
		und.Summary = "No modifications in this file."
		return und
	}
	var parts []string
	if n := len(und.FastReturns); n > 0 {
		parts = append(parts, fmt.Sprintf("%d early exit%s", n, pluralS(n)))
	}
	removed := 0
	for _, e := range und.EdgeCases {
		if e.Kind == "removed" {
			removed++
		}
	}
	if n := len(und.EdgeCases) - removed; n > 0 {
		parts = append(parts, fmt.Sprintf("%d error path%s", n, pluralS(n)))
	}
	if removed > 0 {
		parts = append(parts, fmt.Sprintf("%d removed check%s", removed, pluralS(removed)))
	}
	if n := len(und.CoreLogic); n > 0 {
		parts = append(parts, fmt.Sprintf("%d definition%s", n, pluralS(n)))
	}
	if len(parts) > 0 {
		und.Summary = "Adds or changes " + strings.Join(parts, ", ") + "."
		if removed > 0 && len(parts) == 1 {
			und.Summary = "Removes " + parts[0] + "."
		}
	} else {
		und.Summary = fmt.Sprintf("+%d -%d lines; no guards, error paths or definitions changed.", adds, dels)
	}
	return und
}

// pxuScan classifies one side of a hunk. other holds the opposite side's
// lines: a removed line that is also added is a move, not a removal.
func pxuScan(und *ReviewUnderstanding, rows []pxuRow, other map[string]bool, removedSide bool) {
	for i := 0; i < len(rows); i++ {
		r := rows[i]
		t := r.t
		if pxuComment(t) {
			continue
		}
		if removedSide && other[t] {
			continue
		}
		// An `if` that bails out: on its own line (Python `if x: return`) or
		// within the next three lines of the same side.
		if pxuIf.MatchString(t) {
			bail, at := "", -1
			if loc := pxuIf.FindStringIndex(t); pxuBail.MatchString(t[loc[1]:]) {
				bail, at = t[loc[1]:], i // `if x: return`, `if (x) return y;`
			}
			for j := i + 1; bail == "" && j < len(rows) && j <= i+3; j++ {
				n := rows[j].t
				if pxuComment(n) {
					continue
				}
				if pxuBail.MatchString(" " + n) {
					bail, at = n, j
					break
				}
				if pxuIf.MatchString(n) || strings.HasPrefix(n, "}") || strings.HasPrefix(n, "else") {
					break
				}
			}
			if bail != "" {
				if removedSide {
					und.EdgeCases = append(und.EdgeCases, pxuItem(r.line, "removed", t, "Removed check: make sure nothing relied on it"))
				} else if pxuErrCheck.MatchString(t) {
					und.EdgeCases = append(und.EdgeCases, pxuItem(r.line, "error", t, "Error check: passes the failure on"))
				} else {
					k, e := pxuGuardKind(t, bail)
					und.FastReturns = append(und.FastReturns, pxuItem(r.line, k, t, e))
				}
				if at > i {
					i = at // the bail-out line belongs to this guard
				}
				continue
			}
		}
		switch {
		case removedSide && (pxuRaise.MatchString(t) || pxuCatch.MatchString(t) || (pxuIf.MatchString(t) && pxuAuth.MatchString(t))):
			und.EdgeCases = append(und.EdgeCases, pxuItem(r.line, "removed", t, "Removed error handling or access check: make sure nothing relied on it"))
		case removedSide:
			// Other removed lines: covered by the diff itself.
		case pxuCatch.MatchString(t):
			und.EdgeCases = append(und.EdgeCases, pxuItem(r.line, "fallback", t, "Handles a failure: what happens when this goes wrong"))
		case pxuRaise.MatchString(t) || pxuReturnEr.MatchString(t):
			und.EdgeCases = append(und.EdgeCases, pxuItem(r.line, "error", t, "Raises or returns an error"))
		case pxuIf.MatchString(t) && pxuErrCheck.MatchString(t):
			und.EdgeCases = append(und.EdgeCases, pxuItem(r.line, "error", t, "Error check"))
		case pxuTimeout.MatchString(t) && !(pxuDef.MatchString(t) && !pxuKeyword.MatchString(t)):
			und.EdgeCases = append(und.EdgeCases, pxuItem(r.line, "timeout", t, "Timeout or expiry handling"))
		case pxuAssert.MatchString(t):
			und.EdgeCases = append(und.EdgeCases, pxuItem(r.line, "panic", t, "Assertion: stops when an assumption does not hold"))
		case pxuDef.MatchString(t) && !pxuKeyword.MatchString(t):
			und.CoreLogic = append(und.CoreLogic, pxuItem(r.line, "function", t, "New or changed definition"))
		}
	}
}

// pxiWalkthroughFileCap bounds one walkthrough: each file carries its diff and
// highlighted rows to the page.
const pxiWalkthroughFileCap = 200

// handlePxiWorktreeWalkthrough answers /api/review/walkthrough?wt=<name>: the
// walkthrough of one worktree's changes against the base it branched from
// (commits, uncommitted and untracked), like the Worktrees view lists them.
func (s *Server) handlePxiWorktreeWalkthrough(w http.ResponseWriter, name string) {
	wt := s.worktrees().lookup(name)
	if wt == nil || wt.Prunable {
		fail(w, http.StatusNotFound, "no worktree named "+name)
		return
	}
	resp := ReviewWalkthroughResponse{Files: []ReviewWalkthroughFile{}, BaseRef: wt.BaseRef, HeadRef: "worktree " + wt.Name}
	if resp.BaseRef == "" {
		resp.BaseRef = "HEAD"
	}
	for i, f := range wt.Files {
		if i == pxiWalkthroughFileCap {
			break
		}
		diff := wtDiff(wt, f.Path)
		if diff == "" {
			continue
		}
		hunks := render.HighlightDiff(f.Path, diff)
		add, del := 0, 0
		for hi := range hunks {
			for ri := range hunks[hi].Rows {
				r := &hunks[hi].Rows[ri]
				r.HTML = "" // the walkthrough shows plain text; don't send highlighting
				if r.Type == "add" {
					add++
				} else if r.Type == "del" {
					del++
				}
			}
		}
		status := f.Status
		if status == "U" {
			status = "A"
		}
		resp.TotalAdditions += add
		resp.TotalDeletions += del
		resp.Files = append(resp.Files, ReviewWalkthroughFile{
			Path: f.Path, Status: status, Additions: add, Deletions: del,
			Hunks: hunks, Understanding: extractCodeUnderstanding(f.Path, hunks),
		})
	}
	resp.TotalFiles = len(resp.Files)
	writeJSON(w, resp)
}
