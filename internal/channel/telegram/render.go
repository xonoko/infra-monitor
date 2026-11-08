package telegram

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-telegram/bot/models"

	"github.com/xonoko/infra-monitor/internal/channel"
)

// MaxMessageLen is Telegram's limit on message text after entity parsing (UTF-16 code units).
const MaxMessageLen = 4096

// safetyMargin keeps a few units free in case our count differs slightly from Telegram's.
const safetyMargin = 16

// RenderMarkdown converts the channel markdown subset to Telegram HTML:
// **bold**, _italic_ / *italic*, `code`, ```fenced``` blocks, [text](url),
// "# " headings (bold line) and "- " / "* " lists ("• ").
func RenderMarkdown(md string) string {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	lines := strings.Split(md, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "```") {
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			var code []string
			j := i + 1
			for ; j < len(lines); j++ {
				if strings.HasPrefix(strings.TrimLeft(lines[j], " \t"), "```") {
					break
				}
				code = append(code, lines[j])
			}
			body := html.EscapeString(strings.Join(code, "\n"))
			if lang != "" && isLangName(lang) {
				out = append(out, `<pre><code class="language-`+lang+`">`+body+`</code></pre>`)
			} else {
				out = append(out, "<pre>"+body+"</pre>")
			}
			i = j // skip closing fence (or run to end when unterminated)
			continue
		}
		indent := line[:len(line)-len(trimmed)]
		switch {
		case strings.HasPrefix(trimmed, "#"):
			h := strings.TrimLeft(trimmed, "#")
			if strings.HasPrefix(h, " ") {
				out = append(out, "<b>"+renderInline(strings.TrimSpace(h))+"</b>")
				continue
			}
			out = append(out, indent+renderInline(trimmed))
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ "):
			out = append(out, indent+"• "+renderInline(trimmed[2:]))
		default:
			out = append(out, indent+renderInline(trimmed))
		}
	}
	return strings.Join(out, "\n")
}

func isLangName(s string) bool {
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '+' || r == '#') {
			return false
		}
	}
	return len(s) <= 32
}

// renderInline renders inline markup of a single line.
func renderInline(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '`':
			if end := strings.IndexByte(s[i+1:], '`'); end >= 0 {
				b.WriteString("<code>" + html.EscapeString(s[i+1:i+1+end]) + "</code>")
				i += end + 2
				continue
			}
		case c == '*' && strings.HasPrefix(s[i:], "**"):
			if end := strings.Index(s[i+2:], "**"); end > 0 {
				b.WriteString("<b>" + renderInline(s[i+2:i+2+end]) + "</b>")
				i += end + 4
				continue
			}
		case c == '_' && strings.HasPrefix(s[i:], "__"):
			if end := strings.Index(s[i+2:], "__"); end > 0 && leftBoundary(s, i) {
				b.WriteString("<u>" + renderInline(s[i+2:i+2+end]) + "</u>")
				i += end + 4
				continue
			}
		case c == '*' || c == '_':
			if end, ok := italicEnd(s, i); ok {
				b.WriteString("<i>" + renderInline(s[i+1:end]) + "</i>")
				i = end + 1
				continue
			}
		case c == '[':
			if text, href, n, ok := parseLink(s[i:]); ok {
				if safeHref(href) {
					b.WriteString(`<a href="` + html.EscapeString(href) + `">` + renderInline(text) + "</a>")
				} else {
					b.WriteString(renderInline(text) + " (" + html.EscapeString(href) + ")")
				}
				i += n
				continue
			}
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		b.WriteString(html.EscapeString(s[i : i+size]))
		i += size
	}
	return b.String()
}

// leftBoundary reports whether position i starts a delimiter run (start of text or after space/punct).
func leftBoundary(s string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return unicode.IsSpace(r) || unicode.IsPunct(r)
}

// italicEnd finds the closing delimiter of a single-char italic span starting at i.
// Intra-word delimiters (snake_case, a*b*c) are not treated as markup.
func italicEnd(s string, i int) (int, bool) {
	d := s[i]
	if !leftBoundary(s, i) || i+1 >= len(s) || s[i+1] == ' ' || s[i+1] == d {
		return 0, false
	}
	for j := i + 1; j < len(s); j++ {
		if s[j] != d || s[j-1] == ' ' {
			continue
		}
		if j+1 < len(s) {
			if s[j+1] == d {
				continue
			}
			r, _ := utf8.DecodeRuneInString(s[j+1:])
			if !(unicode.IsSpace(r) || unicode.IsPunct(r)) {
				continue
			}
		}
		return j, true
	}
	return 0, false
}

// parseLink parses "[text](url)" at the start of s.
func parseLink(s string) (text, href string, n int, ok bool) {
	closeText := strings.Index(s, "](")
	if closeText <= 0 {
		return "", "", 0, false
	}
	if strings.ContainsAny(s[1:closeText], "[]\n") {
		return "", "", 0, false
	}
	rest := s[closeText+2:]
	closeURL := strings.IndexByte(rest, ')')
	if closeURL <= 0 {
		return "", "", 0, false
	}
	href = rest[:closeURL]
	if strings.ContainsAny(href, " \n") {
		return "", "", 0, false
	}
	return s[1:closeText], href, closeText + 2 + closeURL + 1, true
}

func safeHref(href string) bool {
	u, err := url.Parse(href)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "http", "https", "tg", "mailto":
		return true
	}
	return false
}

// buttonURLAllowed reports whether Telegram accepts the URL for an inline URL button.
func buttonURLAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "0.0.0.0" || strings.HasSuffix(h, ".localhost") {
		return false
	}
	return true
}

// VisibleLen counts the UTF-16 code units Telegram sees once HTML tags are stripped
// and entities decoded.
func VisibleLen(s string) int {
	n := 0
	i := 0
	for i < len(s) {
		switch s[i] {
		case '<':
			if end := strings.IndexByte(s[i:], '>'); end >= 0 {
				i += end + 1
				continue
			}
		case '&':
			if end := strings.IndexByte(s[i:], ';'); end > 0 && end < 12 {
				n++
				i += end + 1
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		n += utf16Len(r)
		i += size
	}
	return n
}

func utf16Len(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// Truncate shortens rendered HTML so its visible length fits max. It never cuts inside a tag,
// an entity or a surrogate pair, closes open tags, appends "…" and the optional suffix (HTML).
func Truncate(htmlText string, max int, suffix string) string {
	if VisibleLen(htmlText) <= max {
		return htmlText
	}
	budget := max - VisibleLen(suffix) - 1 // "…"
	if budget < 0 {
		budget = 0
	}
	var (
		b     strings.Builder
		stack []string
		n     int
		i     int
	)
loop:
	for i < len(htmlText) {
		switch htmlText[i] {
		case '<':
			if end := strings.IndexByte(htmlText[i:], '>'); end >= 0 {
				tag := htmlText[i : i+end+1]
				name := tagName(tag)
				if strings.HasPrefix(tag, "</") {
					if len(stack) > 0 && stack[len(stack)-1] == name {
						stack = stack[:len(stack)-1]
					}
				} else if name != "" {
					stack = append(stack, name)
				}
				b.WriteString(tag)
				i += end + 1
				continue
			}
		case '&':
			if end := strings.IndexByte(htmlText[i:], ';'); end > 0 && end < 12 {
				if n+1 > budget {
					break loop
				}
				b.WriteString(htmlText[i : i+end+1])
				n++
				i += end + 1
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(htmlText[i:])
		w := utf16Len(r)
		if n+w > budget {
			break
		}
		b.WriteString(htmlText[i : i+size])
		n += w
		i += size
	}
	out := strings.TrimRightFunc(b.String(), unicode.IsSpace)
	// Drop tags opened right at the cut point that contain nothing.
	for len(stack) > 0 && strings.HasSuffix(out, ">") {
		last := strings.LastIndexByte(out, '<')
		if last < 0 || strings.HasPrefix(out[last:], "</") || tagName(out[last:]) != stack[len(stack)-1] {
			break
		}
		out = out[:last]
		stack = stack[:len(stack)-1]
	}
	var closing strings.Builder
	for j := len(stack) - 1; j >= 0; j-- {
		closing.WriteString("</" + stack[j] + ">")
	}
	return out + closing.String() + "…" + suffix
}

func tagName(tag string) string {
	t := strings.TrimPrefix(strings.TrimPrefix(tag, "<"), "/")
	t = strings.TrimSuffix(t, ">")
	if i := strings.IndexAny(t, " \t\n"); i >= 0 {
		t = t[:i]
	}
	return strings.ToLower(t)
}

// fullTextSuffix is appended when content is truncated and a UI link exists.
func fullTextSuffix(link string) string {
	if link == "" || !safeHref(link) {
		return ""
	}
	return "\n\n" + `<a href="` + html.EscapeString(link) + `">Full text in UI</a>`
}

func fit(htmlText, link string) string {
	return Truncate(htmlText, MaxMessageLen-safetyMargin, fullTextSuffix(link))
}

func severityEmoji(sev string) string {
	switch strings.ToLower(sev) {
	case "critical", "page", "error":
		return "🔴"
	case "warning", "warn":
		return "🟠"
	case "info":
		return "🔵"
	default:
		return "⚪"
	}
}

func statusLabel(status string, muted bool) string {
	var s string
	switch strings.ToLower(status) {
	case "resolved", "closed":
		s = "✅ " + strings.ToLower(status)
	case "acknowledged":
		s = "👀 acknowledged"
	case "open":
		s = "🔥 open"
	default:
		s = html.EscapeString(status)
	}
	if muted {
		s += " · 🔕 muted"
	}
	return s
}

// RenderCard renders an incident card to Telegram HTML.
func RenderCard(c channel.Card) string {
	var b strings.Builder
	b.WriteString(severityEmoji(c.Severity) + " <b>" + html.EscapeString(c.Title) + "</b>\n")
	b.WriteString(statusLabel(c.Status, c.Muted))
	if c.Severity != "" {
		b.WriteString(" · " + html.EscapeString(c.Severity))
	}
	if c.IncidentID > 0 {
		b.WriteString(fmt.Sprintf(" · #%d", c.IncidentID))
	}
	b.WriteString("\n")
	if len(c.Fields) > 0 {
		b.WriteString("\n")
		for _, f := range c.Fields {
			b.WriteString(html.EscapeString(f.Name) + ": <code>" + html.EscapeString(f.Value) + "</code>\n")
		}
	}
	if c.Verdict != "" {
		b.WriteString("Verdict: <b>" + html.EscapeString(c.Verdict) + "</b>\n")
	}
	if c.Notice != "" {
		b.WriteString("\n<i>" + html.EscapeString(c.Notice) + "</i>\n")
	}
	if strings.TrimSpace(c.Summary) != "" {
		b.WriteString("\n<b>AI summary</b>\n" + RenderMarkdown(strings.TrimSpace(c.Summary)) + "\n")
	}
	if len(c.PendingActions) > 0 {
		b.WriteString("\n<b>Pending actions</b>\n")
		for _, a := range c.PendingActions {
			b.WriteString("• " + renderInline(a) + "\n")
		}
	}
	return fit(strings.TrimRight(b.String(), "\n"), c.URL)
}

// RenderMessage renders a thread message to Telegram HTML.
func RenderMessage(m channel.Message) string {
	var b strings.Builder
	if m.Author != "" {
		b.WriteString("<b>" + html.EscapeString(m.Author) + "</b>\n")
	}
	b.WriteString(RenderMarkdown(strings.TrimSpace(m.Body)))
	if m.Footer != "" {
		b.WriteString("\n\n<i>" + html.EscapeString(m.Footer) + "</i>")
	}
	return fit(b.String(), m.URL)
}

// Keyboard converts buttons to an inline keyboard (max 3 buttons per row).
// It always returns a non-nil markup so an empty keyboard removes existing buttons on edit.
func Keyboard(buttons []channel.Button) *models.InlineKeyboardMarkup {
	rows := [][]models.InlineKeyboardButton{}
	var row []models.InlineKeyboardButton
	for _, btn := range buttons {
		var kb models.InlineKeyboardButton
		switch {
		case btn.Action != nil:
			kb = models.InlineKeyboardButton{Text: btn.Label, CallbackData: btn.Action.Encode()}
		case btn.URL != "" && buttonURLAllowed(btn.URL):
			kb = models.InlineKeyboardButton{Text: btn.Label, URL: btn.URL}
		default:
			continue
		}
		row = append(row, kb)
		if len(row) == 3 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}
