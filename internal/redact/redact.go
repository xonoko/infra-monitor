// Package redact removes secrets from text before it is stored, shown in chat channels or sent
// to the LLM: Kubernetes Secret data, key=value / key: value credentials, bearer tokens, JWTs,
// PEM private keys and URLs with embedded passwords.
package redact

import (
	"regexp"
	"strings"
)

const mask = "<redacted>"

var (
	pemBlock = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	bearer   = regexp.MustCompile(`(?i)(bearer|basic)\s+[A-Za-z0-9\-._~+/]{8,}=*`)
	jwt      = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
	urlCreds = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^:/@\s]+:)[^@\s]+@`)
	// key=value, key: value, "key": "value" for credential-looking keys.
	kv = regexp.MustCompile(`(?i)("?[A-Za-z0-9_.-]*(password|passwd|pwd|secret|token|api[_-]?key|apikey|access[_-]?key|private[_-]?key|client[_-]?secret|credentials?)"?\s*[:=]\s*)("[^"\n]*"|'[^'\n]*'|[^\s,}\]\n]+)`)
	// Known token shapes.
	tokens = regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9_-]{20,}|imt_[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16})\b`)
)

// Text redacts credentials in free text (logs, command output, YAML/JSON).
func Text(s string) string {
	if s == "" {
		return s
	}
	s = secretData(s)
	s = pemBlock.ReplaceAllString(s, mask)
	s = jwt.ReplaceAllString(s, mask)
	s = tokens.ReplaceAllString(s, mask)
	s = bearer.ReplaceAllString(s, "$1 "+mask)
	s = urlCreds.ReplaceAllString(s, "${1}"+mask+"@")
	s = kv.ReplaceAllStringFunc(s, func(m string) string {
		sub := kv.FindStringSubmatch(m)
		if len(sub) < 4 || strings.TrimSpace(sub[3]) == "" || sub[3] == mask || strings.Trim(sub[3], `"'`) == mask {
			return m
		}
		return sub[1] + mask
	})
	return s
}

// secretData masks the values under data:/stringData: in YAML documents of kind Secret.
func secretData(s string) string {
	if !strings.Contains(s, "kind: Secret") && !strings.Contains(s, `"kind":"Secret"`) && !strings.Contains(s, `"kind": "Secret"`) {
		return s
	}
	lines := strings.Split(s, "\n")
	in := -1 // indentation of the data:/stringData: key while inside it
	for i, l := range lines {
		trim := strings.TrimLeft(l, " +-") // tolerate diff prefixes
		indent := len(l) - len(strings.TrimLeft(l, " +-"))
		if in >= 0 {
			if strings.TrimSpace(trim) != "" && indent <= in {
				in = -1
			} else if k, _, ok := strings.Cut(trim, ":"); ok && strings.TrimSpace(trim) != "" {
				lines[i] = l[:indent] + k + ": " + mask
				continue
			}
		}
		if trim == "data:" || trim == "stringData:" {
			in = indent
		}
	}
	return strings.Join(lines, "\n")
}

// Map redacts every string in a JSON-like value (params, payloads).
func Map(v any) any {
	switch t := v.(type) {
	case string:
		return Text(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = Map(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = Map(x)
		}
		return out
	}
	return v
}
