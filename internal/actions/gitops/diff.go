package gitops

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// unifiedDiff renders a git-style unified diff (3 lines of context). oldExists=false renders a new file.
func unifiedDiff(path, oldContent, newContent string, oldExists bool) string {
	from := "a/" + path
	if !oldExists {
		from = "/dev/null"
	}
	d, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        diffLines(oldContent),
		B:        diffLines(newContent),
		FromFile: from,
		ToFile:   "b/" + path,
		Context:  3,
	})
	if err != nil {
		return ""
	}
	return d
}

// diffStats counts added and removed lines in a unified diff.
func diffStats(diff string) (added, removed int) {
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
		case strings.HasPrefix(l, "+"):
			added++
		case strings.HasPrefix(l, "-"):
			removed++
		}
	}
	return added, removed
}

type hunk struct {
	oldStart, oldCount, newCount int
	lines                        []string // with their ' ', '-', '+' prefix
}

// applyUnifiedDiff applies a unified diff strictly: every context and removed line must match
// the original at the position stated by the hunk header.
func applyUnifiedDiff(original, diff string) (string, error) {
	hunks, err := parseHunks(diff)
	if err != nil {
		return "", err
	}
	if len(hunks) == 0 {
		return "", fmt.Errorf("diff contains no hunks")
	}
	trailingNL := original == "" || strings.HasSuffix(original, "\n")
	src := splitNoNL(original)
	var out []string
	pos := 0 // index in src
	for hi, h := range hunks {
		start := h.oldStart - 1
		if h.oldStart == 0 {
			start = 0
		}
		if start < pos || start > len(src) {
			return "", fmt.Errorf("hunk %d: start line %d out of order or beyond the file (%d lines)", hi+1, h.oldStart, len(src))
		}
		out = append(out, src[pos:start]...)
		pos = start
		for _, l := range h.lines {
			if l == "" {
				return "", fmt.Errorf("hunk %d: empty line without a ' ', '-' or '+' prefix", hi+1)
			}
			body := l[1:]
			switch l[0] {
			case ' ', '-':
				if pos >= len(src) || src[pos] != body {
					got := "<end of file>"
					if pos < len(src) {
						got = strconv.Quote(src[pos])
					}
					return "", fmt.Errorf("hunk %d: line %d does not match: expected %q, found %s", hi+1, pos+1, body, got)
				}
				if l[0] == ' ' {
					out = append(out, body)
				}
				pos++
			case '+':
				out = append(out, body)
			default:
				return "", fmt.Errorf("hunk %d: unexpected line prefix %q", hi+1, l[:1])
			}
		}
	}
	out = append(out, src[pos:]...)
	res := strings.Join(out, "\n")
	if trailingNL && len(out) > 0 {
		res += "\n"
	}
	return res, nil
}

func splitNoNL(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func parseHunks(diff string) ([]hunk, error) {
	var hunks []hunk
	var cur *hunk
	oldLeft, newLeft := 0, 0
	files := 0
	for _, l := range strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n") {
		if cur != nil && (oldLeft > 0 || newLeft > 0) {
			if strings.HasPrefix(l, `\ No newline`) {
				continue
			}
			if l == "" {
				l = " " // some tools strip the space of empty context lines
			}
			switch l[0] {
			case ' ':
				oldLeft--
				newLeft--
			case '-':
				oldLeft--
			case '+':
				newLeft--
			default:
				return nil, fmt.Errorf("hunk %d: unexpected line %q", len(hunks), l)
			}
			if oldLeft < 0 || newLeft < 0 {
				return nil, fmt.Errorf("hunk %d: more lines than its header announces", len(hunks))
			}
			cur.lines = append(cur.lines, l)
			continue
		}
		switch {
		case strings.HasPrefix(l, "@@"):
			h, err := parseHunkHeader(l)
			if err != nil {
				return nil, err
			}
			hunks = append(hunks, h)
			cur = &hunks[len(hunks)-1]
			oldLeft, newLeft = h.oldCount, h.newCount
		case strings.HasPrefix(l, "+++ "):
			files++
			if files > 1 {
				return nil, fmt.Errorf("diff touches several files; propose one gitops.change per file")
			}
		case strings.HasPrefix(l, `\ No newline`), strings.TrimSpace(l) == "":
		default:
			// headers (diff --git, index, ---) between or before hunks
		}
	}
	if oldLeft > 0 || newLeft > 0 {
		return nil, fmt.Errorf("hunk %d is truncated (%d old / %d new lines missing)", len(hunks), oldLeft, newLeft)
	}
	return hunks, nil
}

// parseHunkHeader parses "@@ -l[,s] +l[,s] @@".
func parseHunkHeader(l string) (hunk, error) {
	fields := strings.Fields(l)
	if len(fields) < 3 || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return hunk{}, fmt.Errorf("invalid hunk header %q", l)
	}
	os, oc, err1 := rangeOf(strings.TrimPrefix(fields[1], "-"))
	_, nc, err2 := rangeOf(strings.TrimPrefix(fields[2], "+"))
	if err1 != nil || err2 != nil {
		return hunk{}, fmt.Errorf("invalid hunk header %q", l)
	}
	return hunk{oldStart: os, oldCount: oc, newCount: nc}, nil
}

func rangeOf(s string) (start, count int, err error) {
	count = 1
	if i := strings.IndexByte(s, ','); i >= 0 {
		if count, err = strconv.Atoi(s[i+1:]); err != nil {
			return 0, 0, err
		}
		s = s[:i]
	}
	start, err = strconv.Atoi(s)
	if err != nil || start < 0 || count < 0 {
		return 0, 0, fmt.Errorf("invalid range")
	}
	return start, count, nil
}

// diffLines splits content into lines that each end with "\n" (difflib.SplitLines adds a
// phantom empty line at the end, which would corrupt hunk ranges).
func diffLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	} else {
		lines[len(lines)-1] += "\n"
	}
	return lines
}
