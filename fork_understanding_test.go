package main

import (
	"testing"

	"px0/internal/render"
)

func pxuHunk(rows ...render.DiffRow) []render.DiffHunk {
	return []render.DiffHunk{{OldStart: 1, NewStart: 1, Rows: rows}}
}

func pxuKinds(items []CodeInsightItem) map[string]int {
	m := map[string]int{}
	for _, it := range items {
		m[it.Kind]++
	}
	return m
}

func TestPxiUnderstandingPython(t *testing.T) {
	u := pxiExtractUnderstanding("m.py", pxuHunk(
		render.DiffRow{Type: "ctx", OldLine: 1, NewLine: 1, Text: "def f(x):"},
		render.DiffRow{Type: "add", NewLine: 2, Text: "    if x is None:"},
		render.DiffRow{Type: "add", NewLine: 3, Text: "        raise ValueError(\"x\")"},
		render.DiffRow{Type: "add", NewLine: 4, Text: "    if not user.can_edit: return None"},
		render.DiffRow{Type: "add", NewLine: 5, Text: "    try:"},
		render.DiffRow{Type: "add", NewLine: 6, Text: "        return g(x)"},
		render.DiffRow{Type: "add", NewLine: 7, Text: "    except KeyError:"},
		render.DiffRow{Type: "add", NewLine: 8, Text: "        raise NotFound()"},
		render.DiffRow{Type: "add", NewLine: 9, Text: "def helper(y):"},
		render.DiffRow{Type: "add", NewLine: 10, Text: "    return y"},
	))
	if k := pxuKinds(u.FastReturns); k["nil_check"] != 1 || k["auth"] != 1 || len(u.FastReturns) != 2 {
		t.Errorf("returns: %+v", u.FastReturns)
	}
	if k := pxuKinds(u.EdgeCases); k["fallback"] != 1 || k["error"] != 1 || len(u.EdgeCases) != 2 {
		t.Errorf("edge cases: %+v", u.EdgeCases)
	}
	if len(u.CoreLogic) != 1 || u.CoreLogic[0].Line != 9 {
		t.Errorf("core: %+v", u.CoreLogic)
	}
}

func TestPxiUnderstandingTypeScriptAndRemoved(t *testing.T) {
	u := pxiExtractUnderstanding("a.ts", pxuHunk(
		render.DiffRow{Type: "del", OldLine: 1, Text: "  if (!session.isAdmin) {"},
		render.DiffRow{Type: "del", OldLine: 2, Text: "    throw new Forbidden();"},
		render.DiffRow{Type: "del", OldLine: 3, Text: "  }"},
		render.DiffRow{Type: "del", OldLine: 4, Text: "  const a = load();"},
		render.DiffRow{Type: "add", NewLine: 1, Text: "  const a = load();"},
		render.DiffRow{Type: "add", NewLine: 2, Text: "  fetchIt().catch(err => log(err));"},
		render.DiffRow{Type: "add", NewLine: 3, Text: "export const run = async (x) => {"},
		render.DiffRow{Type: "add", NewLine: 4, Text: "  if (cache.has(x)) return cache.get(x);"},
		render.DiffRow{Type: "add", NewLine: 5, Text: "  if (ok(x)) {"},
		render.DiffRow{Type: "add", NewLine: 6, Text: "    go(x);"},
		render.DiffRow{Type: "add", NewLine: 7, Text: "  }"},
	))
	if k := pxuKinds(u.EdgeCases); k["removed"] != 1 || k["fallback"] != 1 {
		t.Errorf("edge cases: %+v", u.EdgeCases)
	}
	if u.EdgeCases[0].Kind == "removed" && u.EdgeCases[0].Line != 1 {
		t.Errorf("removed check should point at new line 1: %+v", u.EdgeCases[0])
	}
	if k := pxuKinds(u.FastReturns); k["cache"] != 1 || len(u.FastReturns) != 1 {
		t.Errorf("returns (an if without a bail-out is not one): %+v", u.FastReturns)
	}
	if len(u.CoreLogic) != 1 {
		t.Errorf("core: %+v", u.CoreLogic)
	}
}
