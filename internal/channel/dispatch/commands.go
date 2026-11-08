package dispatch

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/xonoko/infra-monitor/internal/channel"
	"github.com/xonoko/infra-monitor/internal/domain"
	incsvc "github.com/xonoko/infra-monitor/internal/incident"
)

const helpText = `**infra-monitor commands**
- reply to an incident message: chat with the AI agent about it
- /status — open incidents
- /ack, /resolve, /reinvestigate — reply to an incident card (or add the id: /ack 42)
- /reinvestigate deep — dig deeper with the bigger model (costs more)
- /mute 1h — mute the incident you reply to
- /approve 12, /reject 12 — decide on a proposed action
- /link CODE — link your account (code from your profile page)`

func (d *Dispatcher) command(ctx context.Context, ch channel.Channel, ev channel.InboundEvent) (channel.Response, error) {
	switch ev.Command {
	case "start", "help":
		return channel.Response{Text: helpText}, nil
	case "link":
		if len(ev.Args) != 1 {
			return channel.Response{Text: "Usage: /link CODE (generate the code in your infra-monitor profile)"}, nil
		}
		name := firstNonEmpty(ev.User.Name, ev.User.Username)
		if ev.User.Username != "" && ev.User.Name != "" {
			name = ev.User.Name + " (@" + ev.User.Username + ")"
		}
		u, err := d.auth.LinkIdentity(ctx, ev.Args[0], ch.Type(), ev.User.ID, name)
		if err != nil {
			return channel.Response{}, err
		}
		return channel.Response{Text: fmt.Sprintf("✅ Linked to %s (%s).", u.Email, u.Role)}, nil
	case "status":
		return d.statusCommand(ctx)
	}

	actor, err := d.actor(ctx, ch, ev)
	if err != nil {
		return channel.Response{}, err
	}
	if actor == nil {
		return channel.Response{Text: d.linkHelp()}, nil
	}
	switch ev.Command {
	case "approve", "reject":
		if len(ev.Args) < 1 {
			return channel.Response{Text: "Usage: /" + ev.Command + " ACTION_ID"}, nil
		}
		id, err := strconv.Atoi(strings.TrimPrefix(ev.Args[0], "#"))
		if err != nil {
			return channel.Response{Text: "Usage: /" + ev.Command + " ACTION_ID"}, nil
		}
		b := channel.ButtonAction{Op: ev.Command, ActionID: id}
		if len(ev.Args) > 1 {
			b.Arg = ev.Args[1] // payload hash for precise proposals: /approve 42 1a2b3c4d
		}
		return d.button(ctx, *actor, b)
	case "ack", "resolve", "reinvestigate", "mute":
		incID, rest := d.targetIncident(ctx, ch, ev)
		if incID == 0 {
			return channel.Response{Text: "Reply to an incident card, or give its id: /" + ev.Command + " 42"}, nil
		}
		b := channel.ButtonAction{Op: ev.Command, IncidentID: incID}
		if ev.Command == "reinvestigate" && len(rest) > 0 && strings.EqualFold(rest[0], "deep") {
			b.Arg = "deep"
		}
		if ev.Command == "mute" {
			b.Arg = "1h"
			if len(rest) > 0 {
				if _, err := parseDuration(rest[0]); err != nil {
					return channel.Response{Text: "Usage: /mute 1h (units: m, h, d)"}, nil
				}
				dd, _ := parseDuration(rest[0])
				b.Arg = dd.String()
			}
		}
		return d.button(ctx, *actor, b)
	}
	return channel.Response{Text: "Unknown command. " + helpText}, nil
}

// targetIncident resolves the incident from an explicit id argument or the replied-to message.
func (d *Dispatcher) targetIncident(ctx context.Context, ch channel.Channel, ev channel.InboundEvent) (int, []string) {
	if len(ev.Args) > 0 {
		if id, err := strconv.Atoi(strings.TrimPrefix(ev.Args[0], "#")); err == nil {
			return id, ev.Args[1:]
		}
	}
	if id, ok := d.incidentFor(ctx, ch, ev); ok {
		return id, ev.Args
	}
	return 0, ev.Args
}

func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func (d *Dispatcher) statusCommand(ctx context.Context) (channel.Response, error) {
	items, total, _, err := d.svc.List(ctx, incsvc.ListFilter{Status: []string{"open", "acknowledged"}, Limit: 15})
	if err != nil {
		return channel.Response{}, err
	}
	if total == 0 {
		return channel.Response{Text: "✅ No open incidents."}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%d open incident(s)**\n", total)
	for _, inc := range items {
		icon := map[int]string{4: "🔴", 3: "🟠", 2: "🔵"}[domain.SeverityRank(inc.Severity)]
		if icon == "" {
			icon = "⚪"
		}
		muted := ""
		if inc.Muted {
			muted = " 🔕"
		}
		fmt.Fprintf(&b, "- %s [#%d](%s) %s · `%s`%s\n", icon, inc.ID, d.svc.IncidentURL(inc.ID), inc.Title, firstNonEmpty(inc.Cluster, "-"), muted)
	}
	if total > len(items) {
		fmt.Fprintf(&b, "…and %d more: %s", total-len(items), d.svc.PublicURL())
	}
	return channel.Response{Text: b.String()}, nil
}
