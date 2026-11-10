package render

// pxi: YAML (---) or TOML (+++) front matter at the top of a Markdown file
// (Claude Code skills and agents, static-site pages) renders as a highlighted
// code block, the way GitHub shows it, instead of a run of paragraphs and a
// stray heading. Line count is kept, so preview lines still match the source.

import "bytes"

func pxiFrontmatter(src []byte) []byte {
	var lang string
	switch {
	case bytes.HasPrefix(src, []byte("---\n")):
		lang = "yaml"
	case bytes.HasPrefix(src, []byte("+++\n")):
		lang = "toml"
	default:
		return src
	}
	marker := src[:3]
	rest := src[4:]
	end := -1 // index in rest of the closing marker line
	for off := 0; off < len(rest); {
		nl := bytes.IndexByte(rest[off:], '\n')
		line := rest[off:]
		if nl >= 0 {
			line = rest[off : off+nl]
		}
		if bytes.Equal(bytes.TrimRight(line, " \t"), marker) {
			end = off
			break
		}
		if nl < 0 {
			break
		}
		off += nl + 1
	}
	if end < 0 {
		return src // no closing marker: not front matter
	}
	var out bytes.Buffer
	out.Grow(len(src) + 8)
	out.WriteString("```" + lang + "\n")
	out.Write(rest[:end])
	out.WriteString("```")
	out.Write(bytes.TrimLeft(rest[end:], "-+ \t"))
	return out.Bytes()
}
