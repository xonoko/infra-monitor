package actions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// ErrPreconditionChanged is returned when the target changed since the proposal was prepared
// (resourceVersion, file SHA...). The core then re-prepares and asks for a new approval.
var ErrPreconditionChanged = errors.New("target changed since the proposal was prepared")

// Prepared is everything a human needs to approve an action precisely. It is computed when the
// action is proposed (and again before execution) and shown verbatim in the UI and chat cards.
type Prepared struct {
	// Target identifies exactly what changes, e.g. "manager / demo-incident / apps/v1 Deployment demo-api"
	// or "github.com/lucasgalton/infra-argocd@main:gitops/clusters/manager/values-demo.yaml".
	Target string `json:"target"`
	// Operation is the exact operation, e.g. "strategic-merge patch", "server-side apply", "delete",
	// "exec [sh -c 'redis-cli FLUSHALL'] in pod x container y", "commit to main".
	Operation string `json:"operation"`
	// Payload is the full payload: patch / manifest (YAML) / argv / unified diff. Never a paraphrase.
	Payload string `json:"payload"`
	// PayloadFormat: yaml | json | diff | text (rendering hint).
	PayloadFormat string `json:"payload_format" enum:"yaml,json,diff,text"`
	// DryRun is the server-side dry-run outcome (e.g. a diff of the live object vs the dry-run result),
	// or the rendered diff against the current file for git. Empty when not possible (exec).
	DryRun string `json:"dry_run"`
	// DryRunOK is false when the dry-run failed (the error is in DryRun); such proposals can't be approved.
	DryRunOK bool `json:"dry_run_ok"`
	// Effect explains the expected effect in one or two sentences (derived, not from the agent).
	Effect string `json:"effect"`
	// Rollback describes how to undo it; RollbackAction is a proposal (type + params) that undoes it, if any.
	Rollback       string          `json:"rollback"`
	RollbackAction *RollbackAction `json:"rollback_action,omitempty"`
	Risk           string          `json:"risk" enum:"low,medium,high"`
	// Precondition pins the target state: resourceVersion, file blob SHA, pod UID...
	Precondition string `json:"precondition"`
	// CoreInfrastructure: kube-system or a namespace annotated auto-approve=none.
	CoreInfrastructure bool `json:"core_infrastructure"`
	// ArgoManaged is the Argo CD application managing the target ("" if none). Live patches on such
	// targets are reverted by selfHeal: prefer gitops.change.
	ArgoManaged string   `json:"argo_managed,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// RollbackAction undoes an action (proposed, never executed automatically).
type RollbackAction struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
}

// Preparer is implemented by executors whose proposals need precise approval.
type Preparer interface {
	// Prepare resolves the exact target, runs a dry-run and computes rollback and precondition.
	// It must not change anything.
	Prepare(ctx context.Context, params map[string]any, t Target) (*Prepared, error)
}

// Supervised is implemented by executors that must never be auto-approved.
type Supervised interface {
	RequiresApproval() bool
}

// PreconditionExecutor executes only if the target still matches the prepared precondition.
type PreconditionExecutor interface {
	ExecutePrepared(ctx context.Context, params map[string]any, t Target, p *Prepared) (string, error)
}

// PayloadHash binds an approval to the exact action: type, params and precondition.
func PayloadHash(typ string, params map[string]any, precondition string) string {
	b, _ := json.Marshal(params) // map keys are sorted
	h := sha256.New()
	h.Write([]byte(typ))
	h.Write([]byte{0})
	h.Write(b)
	h.Write([]byte{0})
	h.Write([]byte(precondition))
	return hex.EncodeToString(h.Sum(nil))
}

// CoreNamespace reports whether a namespace counts as core infrastructure.
func CoreNamespace(ns string, annotations map[string]string, annotationPrefix string) bool {
	if ns == "kube-system" || ns == "kube-public" || ns == "kube-node-lease" {
		return true
	}
	v, ok := annotations[annotationPrefix+"/auto-approve"]
	return ok && (v == "none" || v == "")
}
