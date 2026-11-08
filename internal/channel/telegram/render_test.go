package telegram

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/channel"
)

func TestRenderMarkdown(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain & <tags>", "plain &amp; &lt;tags&gt;"},
		{"**bold** and _it_ and *it2*", "<b>bold</b> and <i>it</i> and <i>it2</i>"},
		{"**bold _nested_**", "<b>bold <i>nested</i></b>"},
		{"use `kubectl get <pod>`", "use <code>kubectl get &lt;pod&gt;</code>"},
		{"snake_case_name stays", "snake_case_name stays"},
		{"a*b*c stays", "a*b*c stays"},
		{"5 * 3 * 2", "5 * 3 * 2"},
		{"[docs](https://ex.com/a?b=1&c=\"2\")", `<a href="https://ex.com/a?b=1&amp;c=&#34;2&#34;">docs</a>`},
		{"[bad](javascript:alert(1))", "bad (javascript:alert(1))"},
		{"# Root cause", "<b>Root cause</b>"},
		{"#hashtag", "#hashtag"},
		{"- one\n* two\n  - nested", "• one\n• two\n  • nested"},
		{"unclosed **bold", "unclosed **bold"},
		{"emoji 🔥 & é", "emoji 🔥 &amp; é"},
	} {
		got := RenderMarkdown(tc.in)
		require.Equal(t, tc.want, got, tc.in)
	}
}

func TestRenderCodeBlock(t *testing.T) {
	in := "Logs:\n```go\nif a < b && c > d {\n\t**not bold**\n}\n```\nafter"
	got := RenderMarkdown(in)
	require.Equal(t, "Logs:\n<pre><code class=\"language-go\">if a &lt; b &amp;&amp; c &gt; d {\n\t**not bold**\n}</code></pre>\nafter", got)

	unterminated := RenderMarkdown("```\n<x>")
	require.Equal(t, "<pre>&lt;x&gt;</pre>", unterminated)
}

func TestVisibleLen(t *testing.T) {
	require.Equal(t, 5, VisibleLen("<b>hello</b>"))
	require.Equal(t, 3, VisibleLen("a&amp;b"))
	require.Equal(t, 2, VisibleLen("🔥"), "supplementary rune counts as a surrogate pair")
	require.Equal(t, 1, VisibleLen("é"))
}

func TestTruncateNeverBreaksMarkup(t *testing.T) {
	long := "<b>" + strings.Repeat("a&amp;b ", 50) + "</b><pre><code>" + strings.Repeat("x", 100) + "</code></pre>"
	for max := 1; max < VisibleLen(long); max += 7 {
		out := Truncate(long, max, "")
		require.LessOrEqual(t, VisibleLen(out), max, "max=%d", max)
		require.True(t, strings.HasSuffix(out, "…"))
		requireBalanced(t, out)
		require.NotRegexp(t, `&[a-z]*…`, out, "cut inside an entity")
	}
	require.Equal(t, long, Truncate(long, 10000, ""))
}

func TestTruncateSurrogatesAndSuffix(t *testing.T) {
	s := strings.Repeat("🔥", 10) // 20 UTF-16 units
	out := Truncate(s, 7, "")
	require.Equal(t, "🔥🔥🔥…", out)

	suffix := fullTextSuffix("https://ui.example.com/incidents/1")
	out = Truncate(strings.Repeat("y", 5000), 4080, suffix)
	require.LessOrEqual(t, VisibleLen(out), 4080)
	require.True(t, strings.HasSuffix(out, `<a href="https://ui.example.com/incidents/1">Full text in UI</a>`))
}

func TestTruncateDropsEmptyTrailingTag(t *testing.T) {
	out := Truncate("aaaa<b>bbbb</b>", 4+1, "")
	require.Equal(t, "aaaa…", out)
}

func requireBalanced(t *testing.T, s string) {
	t.Helper()
	var stack []string
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		end := strings.IndexByte(s[i:], '>')
		require.GreaterOrEqual(t, end, 0, "unterminated tag in %q", s)
		tag := s[i : i+end+1]
		name := tagName(tag)
		if strings.HasPrefix(tag, "</") {
			require.NotEmpty(t, stack, "unexpected close %s in %q", tag, s)
			require.Equal(t, stack[len(stack)-1], name)
			stack = stack[:len(stack)-1]
		} else {
			stack = append(stack, name)
		}
		i += end
	}
	require.Empty(t, stack, "unclosed tags in %q", s)
}

func TestRenderCard(t *testing.T) {
	c := channel.Card{
		IncidentID: 42, Title: "KubePodCrashLooping <api>", Status: "acknowledged", Severity: "critical", Verdict: "actionable", Muted: true,
		Fields:         []channel.Field{{Name: "Cluster", Value: "manager"}, {Name: "Namespace", Value: "shop"}},
		Summary:        "Pod **api** OOMs.",
		Notice:         "⚠️ AI investigation unavailable",
		PendingActions: []string{"rollout restart `deployment/api`"},
		URL:            "https://ui.example.com/incidents/42",
	}
	out := RenderCard(c)
	require.Contains(t, out, "🔴 <b>KubePodCrashLooping &lt;api&gt;</b>")
	require.Contains(t, out, "👀 acknowledged · 🔕 muted · critical · #42")
	require.Contains(t, out, "Cluster: <code>manager</code>")
	require.Contains(t, out, "Verdict: <b>actionable</b>")
	require.Contains(t, out, "<i>⚠️ AI investigation unavailable</i>")
	require.Contains(t, out, "Pod <b>api</b> OOMs.")
	require.Contains(t, out, "• rollout restart <code>deployment/api</code>")
	requireBalanced(t, out)

	c.Status, c.Muted, c.Severity = "resolved", false, "info"
	require.True(t, strings.HasPrefix(RenderCard(c), "🔵 "))
	require.Contains(t, RenderCard(c), "✅ resolved")

	c.Summary = strings.Repeat("long analysis ", 1000)
	out = RenderCard(c)
	require.LessOrEqual(t, VisibleLen(out), MaxMessageLen)
	require.Contains(t, out, "Full text in UI")
	requireBalanced(t, out)
}

func TestRenderMessage(t *testing.T) {
	out := RenderMessage(channel.Message{Author: "Lucas via UI", Body: "restart **now**", Footer: "approved"})
	require.Equal(t, "<b>Lucas via UI</b>\nrestart <b>now</b>\n\n<i>approved</i>", out)
}

func TestKeyboard(t *testing.T) {
	kb := Keyboard([]channel.Button{
		{Label: "Approve", Action: &channel.ButtonAction{Op: "approve", ActionID: 7}},
		{Label: "Reject", Action: &channel.ButtonAction{Op: "reject", ActionID: 7}},
		{Label: "Ack", Action: &channel.ButtonAction{Op: "ack", IncidentID: 3}},
		{Label: "Mute 1h", Action: &channel.ButtonAction{Op: "mute", IncidentID: 3, Arg: "1h"}},
		{Label: "Open in UI", URL: "https://ui.example.com/incidents/3"},
		{Label: "Local", URL: "http://localhost:8080/incidents/3"},
		{Label: "Nothing"},
	})
	require.Len(t, kb.InlineKeyboard, 2)
	require.Len(t, kb.InlineKeyboard[0], 3)
	require.Equal(t, "ay:7", kb.InlineKeyboard[0][0].CallbackData)
	require.Equal(t, "an:7", kb.InlineKeyboard[0][1].CallbackData)
	require.Equal(t, "ia:3", kb.InlineKeyboard[0][2].CallbackData)
	require.Equal(t, "im:3:1h", kb.InlineKeyboard[1][0].CallbackData)
	require.Equal(t, "https://ui.example.com/incidents/3", kb.InlineKeyboard[1][1].URL)
	require.Len(t, kb.InlineKeyboard[1], 2, "localhost URL and empty buttons are skipped")

	empty := Keyboard(nil)
	require.NotNil(t, empty.InlineKeyboard, "empty keyboard serializes as [] to remove buttons")
}

func TestParseCommand(t *testing.T) {
	cmd, args := parseCommand("/Mute@infra_monitor_bot 1h  please")
	require.Equal(t, "mute", cmd)
	require.Equal(t, []string{"1h", "please"}, args)
	cmd, args = parseCommand("/status")
	require.Equal(t, "status", cmd)
	require.Empty(t, args)
}
