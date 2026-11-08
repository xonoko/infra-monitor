package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// Matcher is an Alertmanager-style label matcher: name=value, name!=value, name=~re, name!~re.
type Matcher struct {
	Name  string `json:"name"`
	Op    string `json:"op"`
	Value string `json:"value"`
	re    *regexp.Regexp
}

// ParseMatcher parses `name=value`, `name!=value`, `name=~regex`, `name!~regex` (values may be quoted).
func ParseMatcher(s string) (Matcher, error) {
	s = strings.TrimSpace(s)
	for _, op := range []string{"=~", "!~", "!=", "="} {
		if i := strings.Index(s, op); i > 0 {
			m := Matcher{Name: strings.TrimSpace(s[:i]), Op: op, Value: strings.Trim(strings.TrimSpace(s[i+len(op):]), `"`)}
			return m, m.compile()
		}
	}
	return Matcher{}, fmt.Errorf("invalid matcher %q", s)
}

func ParseMatchers(ss []string) ([]Matcher, error) {
	out := make([]Matcher, 0, len(ss))
	for _, s := range ss {
		m, err := ParseMatcher(s)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (m *Matcher) compile() error {
	switch m.Op {
	case "=~", "!~":
		re, err := regexp.Compile("^(?:" + m.Value + ")$")
		if err != nil {
			return fmt.Errorf("matcher %s: %w", m.Name, err)
		}
		m.re = re
	case "=", "!=":
	default:
		return fmt.Errorf("matcher %s: unknown operator %q", m.Name, m.Op)
	}
	return nil
}

// Validate compiles the matcher (needed after JSON decoding).
func (m *Matcher) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("matcher name is required")
	}
	return m.compile()
}

func (m *Matcher) Matches(labels map[string]string) bool {
	v := labels[m.Name]
	switch m.Op {
	case "=":
		return v == m.Value
	case "!=":
		return v != m.Value
	case "=~":
		if m.re == nil {
			_ = m.compile()
		}
		return m.re != nil && m.re.MatchString(v)
	case "!~":
		if m.re == nil {
			_ = m.compile()
		}
		return m.re != nil && !m.re.MatchString(v)
	}
	return false
}

func (m Matcher) String() string { return m.Name + m.Op + fmt.Sprintf("%q", m.Value) }

// MatchAll reports whether all matchers match (an empty list matches everything).
func MatchAll(ms []Matcher, labels map[string]string) bool {
	for i := range ms {
		if !ms[i].Matches(labels) {
			return false
		}
	}
	return true
}
