package render

import (
	"strings"
	"testing"
)

func TestPxiFrontmatterAsCodeBlock(t *testing.T) {
	html, err := RenderMarkdown([]byte("---\nname: review\ndescription: Reviews a diff\n---\n\n# Title\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "<pre") || !strings.Contains(html, "Reviews a diff") || strings.Count(html, "<h1") != 1 {
		t.Errorf("front matter not a code block: %s", html)
	}
	if strings.Contains(html, "<h2") || strings.Contains(html, "<hr") {
		t.Errorf("front matter leaked into headings or rules: %s", html)
	}
	// A rule in the middle of a document is untouched, and so is a file
	// whose opening --- is never closed.
	if got := string(pxiFrontmatter([]byte("text\n---\nmore\n"))); got != "text\n---\nmore\n" {
		t.Errorf("mid-document rule changed: %q", got)
	}
	if got := string(pxiFrontmatter([]byte("---\nno end\n"))); got != "---\nno end\n" {
		t.Errorf("unclosed front matter changed: %q", got)
	}
	if got := string(pxiFrontmatter([]byte("+++\ntitle = 'x'\n+++\nbody\n"))); got != "```toml\ntitle = 'x'\n```\nbody\n" {
		t.Errorf("toml: %q", got)
	}
}
