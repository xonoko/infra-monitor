package generic

import (
	"fmt"
	"strings"
)

// Kinds approved actions may never create or change: identities, credentials and RBAC.
// (The approved ServiceAccount's RBAC also excludes them; this is defense in depth.)
var forbiddenKinds = map[string]bool{
	"secret": true, "serviceaccount": true, "role": true, "rolebinding": true, "clusterrole": true,
	"clusterrolebinding": true, "certificatesigningrequest": true, "tokenrequest": true,
	"mutatingwebhookconfiguration": true, "validatingwebhookconfiguration": true,
}

// Fields that would let a workload run under another identity.
var forbiddenFields = map[string]bool{
	"serviceaccountname": true, "serviceaccount": true, "automountserviceaccounttoken": true,
}

// guard refuses changes to identities: forbidden kinds and any service-account field in the payload.
func guard(kind string, payload any) error {
	if forbiddenKinds[strings.ToLower(kind)] {
		return fmt.Errorf("approved actions can't change %s objects (identities, credentials and RBAC are out of scope)", kind)
	}
	if path := findField(payload, ""); path != "" {
		return fmt.Errorf("approved actions can't set or change service accounts (%s)", path)
	}
	return nil
}

func findField(v any, path string) string {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			p := path + "." + k
			if forbiddenFields[strings.ToLower(k)] {
				return strings.TrimPrefix(p, ".")
			}
			// JSON patch operations: {"op":"replace","path":"/spec/template/spec/serviceAccountName"}
			if k == "path" {
				if s, ok := x.(string); ok {
					for _, seg := range strings.Split(s, "/") {
						if forbiddenFields[strings.ToLower(seg)] {
							return s
						}
					}
				}
			}
			if r := findField(x, p); r != "" {
				return r
			}
		}
	case []any:
		for i, x := range t {
			if r := findField(x, fmt.Sprintf("%s[%d]", path, i)); r != "" {
				return r
			}
		}
	}
	return ""
}
