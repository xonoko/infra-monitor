// Package domain holds types shared by the service layer, the API and the channels.
package domain

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Role ordering: viewer < operator < admin.
type Role string

const (
	RoleNone     Role = ""
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

func (r Role) Level() int {
	switch r {
	case RoleViewer:
		return 1
	case RoleOperator:
		return 2
	case RoleAdmin:
		return 3
	default:
		return 0
	}
}

// Allows reports whether r grants at least min.
func (r Role) Allows(min Role) bool { return r.Level() >= min.Level() }

func ParseRole(s string) (Role, error) {
	switch Role(strings.ToLower(s)) {
	case RoleViewer:
		return RoleViewer, nil
	case RoleOperator:
		return RoleOperator, nil
	case RoleAdmin:
		return RoleAdmin, nil
	}
	return RoleNone, fmt.Errorf("unknown role %q (viewer, operator, admin)", s)
}

// Channel types of origin for timeline events and messages.
const (
	OriginUI     = "ui"
	OriginAPI    = "api"
	OriginSystem = "system"
)

type ActorType string

const (
	ActorUser   ActorType = "user"
	ActorAgent  ActorType = "agent"
	ActorSystem ActorType = "system"
)

// Actor is who performs a service operation and through which channel.
type Actor struct {
	Type        ActorType
	UserID      int
	Name        string
	Role        Role
	Channel     string // ui | api | telegram | slack | system
	ChannelName string // channel instance name (for echo suppression)
}

func (a Actor) UserIDPtr() *int {
	if a.UserID == 0 {
		return nil
	}
	id := a.UserID
	return &id
}

// System is the actor for automatic operations.
var System = Actor{Type: ActorSystem, Name: "infra-monitor", Role: RoleAdmin, Channel: OriginSystem}

// Agent is the actor for the AI agent.
var Agent = Actor{Type: ActorAgent, Name: "agent", Role: RoleViewer, Channel: OriginSystem}

type actorKey struct{}

func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

func ActorFrom(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	return a, ok
}

// Signal is a normalized alert produced by a source.
type Signal struct {
	Source       string
	Fingerprint  string
	Status       string // firing | resolved
	Labels       map[string]string
	Annotations  map[string]string
	StartsAt     time.Time
	EndsAt       *time.Time
	GeneratorURL string
	Raw          []byte
}

// Label returns a label value.
func (s Signal) Label(k string) string { return s.Labels[k] }

// Timeline event types.
const (
	EventIncidentOpened         = "incident_opened"
	EventIncidentReopened       = "incident_reopened"
	EventSignalFiring           = "signal_firing"
	EventSignalResolved         = "signal_resolved"
	EventStatusChanged          = "incident_status_changed"
	EventInvestigationStarted   = "investigation_started"
	EventInvestigationCompleted = "investigation_completed"
	EventInvestigationFailed    = "investigation_failed"
	EventInvestigationSkipped   = "investigation_skipped"
	EventActionProposed         = "action_proposed"
	EventActionApproved         = "action_approved"
	EventActionRejected         = "action_rejected"
	EventActionStarted          = "action_started"
	EventActionSucceeded        = "action_succeeded"
	EventActionFailed           = "action_failed"
	EventActionExpired          = "action_expired"
	EventMessage                = "message"
	EventMuteCreated            = "mute_created"
	EventMuteEnded              = "mute_ended"
	EventNotificationRouted     = "notification_routed"
	EventNotificationSent       = "notification_sent"
	EventAssigned               = "assigned"
	EventAgentThinking          = "agent_thinking"
)

// Severity ranks: critical > warning > info > none/unknown.
func SeverityRank(s string) int {
	switch strings.ToLower(s) {
	case "critical", "page", "error":
		return 4
	case "warning", "warn":
		return 3
	case "info":
		return 2
	case "none":
		return 0
	default:
		return 1
	}
}

// MaxSeverity returns the more severe of a and b.
func MaxSeverity(a, b string) string {
	if SeverityRank(b) > SeverityRank(a) {
		return b
	}
	return a
}
