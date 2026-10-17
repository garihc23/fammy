package femi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"femi/brain/mcp"
	"femi/brain/remind"
)

var errNoScreen = errors.New("Femi's screen isn't connected right now, so that didn't happen")

// flexNumber accepts 5, 5.5 or "5".
type flexNumber float64

func (n *flexNumber) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(bytes.TrimSpace(b), `"`)
	if len(b) == 0 {
		return nil
	}
	v, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return fmt.Errorf("not a number: %s", b)
	}
	*n = flexNumber(v)
	return nil
}

func decodeArgs(raw json.RawMessage, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("bad arguments: %v", err)
	}
	return nil
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func enum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

func object(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func oneOf(v string, allowed ...string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, a := range allowed {
		if v == a {
			return v, nil
		}
	}
	return "", fmt.Errorf("must be one of: %s", strings.Join(allowed, ", "))
}

func (b *Brain) command(v any, ok string) (string, error) {
	if !b.cfg.Device.Connected() {
		return "", errNoScreen
	}
	if err := b.cfg.Device.Command(v); err != nil {
		return "", errNoScreen
	}
	return ok, nil
}

// ToolNames lists the Femi tool names (for --allowedTools as mcp__femi__<name>).
func ToolNames() []string {
	var out []string
	for _, t := range (&Brain{}).Tools() {
		out = append(out, t.Name)
	}
	return out
}

// Tools are Femi's own device tools, served over MCP.
func (b *Brain) Tools() []mcp.Tool {
	return []mcp.Tool{
		{
			Name: "request_approval",
			Description: "Ask the person at Femi to approve an action before you do it. REQUIRED before anything that " +
				"sends a message, email or text, places a phone call, books, buys or pays for something, or adds/changes/deletes " +
				"a calendar event. Femi shows the summary on its screen; the person taps to approve or holds to decline. " +
				"Blocks for up to 60 seconds and returns \"approved\", \"declined\", or \"no answer (treat as declined)\". " +
				"Only go ahead if the result is exactly \"approved\"; otherwise don't do it and briefly say so.",
			InputSchema: object(map[string]any{
				"summary": str("One short line saying exactly what will happen, e.g. \"Text Rahul: running 10 min late\" or \"Book table for 4, Sat 8pm, Karavalli\". Under 60 characters; it is shown on a small screen."),
			}, "summary"),
			Handle: func(ctx context.Context, raw json.RawMessage) (string, error) {
				summary, _ := soleString(raw, "summary")
				s := clip(summary, 120)
				if s == "" {
					return "", errors.New(`"summary" is required: one short line saying what will happen`)
				}
				return b.RequestApproval(ctx, s), nil
			},
		},
		{
			Name: "show_photos",
			Description: "Show family photos full-screen on Femi as a short slideshow (up to 5 photos, about 6 seconds each). " +
				"Use for memories and reminiscing, or when someone says \"show me <person, place or event>\". Albums are named " +
				"after people or events, and partial names work (\"riya\" finds \"Riya\"). Returns immediately with what is " +
				"being shown (and the photo captions, if any) while the photos keep playing; talk about them warmly. " +
				"If no album matches, returns the list of available albums.",
			InputSchema: object(map[string]any{
				"album": str("Album to show: a person's name or an event, e.g. \"Riya\" or \"Goa trip\"."),
			}, "album"),
			Handle: func(ctx context.Context, raw json.RawMessage) (string, error) {
				album, err := soleString(raw, "album")
				if err != nil {
					return "", err
				}
				return b.ShowPhotos(album)
			},
		},
		{
			Name: "set_reminder",
			Description: "Set a reminder on Femi. When it goes off, Femi shows it on screen, says it out loud and waits for " +
				"a tap to confirm. Use for medicines, chores, homework, appointments and any \"remind me to…\". Optionally " +
				"escalate_to: if nobody taps to confirm within a few minutes, Femi has Fo text that family member a calm " +
				"heads-up (use it for important things like medicine, and only when asked or set up by the family). " +
				"Returns the reminder id and when it will go off.",
			InputSchema: object(map[string]any{
				"when":        str("24-hour local time \"HH:MM\" (if that time already passed today it means tomorrow), or \"in N minutes\" / \"in N hours\"."),
				"text":        str("What the reminder says, short and friendly, e.g. \"Drink a glass of water\" or \"Take your blood pressure tablet\"."),
				"repeat":      enum("\"once\" (default) or \"daily\".", remind.Once, remind.Daily),
				"escalate_to": str("Optional: name of a family member or contact to text if the reminder isn't confirmed."),
			}, "when", "text"),
			Handle: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					When       string `json:"when"`
					Time       string `json:"time"`
					Text       string `json:"text"`
					Message    string `json:"message"`
					Repeat     string `json:"repeat"`
					EscalateTo string `json:"escalate_to"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				when := firstNonEmpty(a.When, a.Time)
				if when == "" {
					return "", errors.New(`"when" is required: "HH:MM" (24-hour) or "in N minutes"`)
				}
				now := b.cfg.Now()
				r, err := b.cfg.Reminders.Add(when, firstNonEmpty(a.Text, a.Message), a.Repeat, a.EscalateTo, now)
				if err != nil {
					return "", err
				}
				msg := fmt.Sprintf("Reminder %s set for %s (%s)", r.ID, remind.Human(r.At, now), remind.Until(r.At, now))
				if r.Repeat == remind.Daily {
					msg = fmt.Sprintf("Daily reminder %s set for %s every day, first one %s", r.ID, r.Clock, remind.Human(r.At, now))
				}
				if r.EscalateTo != "" {
					msg += fmt.Sprintf("; if not confirmed within %s, %s gets a text", humanDuration(b.cfg.Reminders.AckWindow), r.EscalateTo)
				}
				return msg + ".", nil
			},
		},
		{
			Name:        "list_reminders",
			Description: "List all reminders and daily medicine schedules, with their ids, times and whether one is waiting to be confirmed.",
			InputSchema: object(map[string]any{}),
			Handle: func(ctx context.Context, raw json.RawMessage) (string, error) {
				list := b.cfg.Reminders.List()
				if len(list) == 0 {
					return "No reminders set.", nil
				}
				now := b.cfg.Now()
				lines := make([]string, len(list))
				for i, r := range list {
					lines[i] = remind.Describe(r, now)
				}
				return strings.Join(lines, "\n"), nil
			},
		},
		{
			Name:        "cancel_reminder",
			Description: "Cancel a reminder or medicine schedule by its id (get ids from list_reminders).",
			InputSchema: object(map[string]any{"id": str("Reminder id, e.g. \"r3\".")}, "id"),
			Handle: func(ctx context.Context, raw json.RawMessage) (string, error) {
				id, err := soleString(raw, "id")
				if err != nil {
					return "", err
				}
				r, ok := b.cfg.Reminders.Cancel(id)
				if !ok {
					return "", fmt.Errorf("no reminder with id %q; call list_reminders to see the ids", id)
				}
				return fmt.Sprintf("Cancelled reminder %s (%s).", r.ID, r.Text), nil
			},
		},
		{
			Name: "set_timer",
			Description: "Start Femi's on-screen countdown timer, e.g. for cooking, homework, brushing teeth or a break. " +
				"Whole minutes from 1 to 180. It rings on the device when done.",
			InputSchema: object(map[string]any{"minutes": map[string]any{"type": "number", "description": "Length in minutes (1-180)."}}, "minutes"),
			Handle: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct{ Minutes flexNumber }
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				m := int(math.Round(float64(a.Minutes)))
				if a.Minutes > 0 && m < 1 {
					m = 1
				}
				if m < 1 || m > 180 {
					return "", errors.New("minutes must be between 1 and 180")
				}
				return b.command(map[string]any{"type": "timer", "minutes": m}, fmt.Sprintf("Timer started for %d minute%s.", m, plural(m)))
			},
		},
		{
			Name:        "timer_control",
			Description: "Control Femi's on-screen timer: \"cancel\", \"pause\" or \"resume\" it, or \"pomodoro\" to start a 25-minute focus session with breaks.",
			InputSchema: object(map[string]any{"action": enum("What to do with the timer.", "cancel", "pause", "resume", "pomodoro")}, "action"),
			Handle: func(ctx context.Context, raw json.RawMessage) (string, error) {
				v, _ := soleString(raw, "action")
				act, err := oneOf(v, "cancel", "pause", "resume", "pomodoro")
				if err != nil {
					return "", fmt.Errorf(`"action" %v`, err)
				}
				return b.command(map[string]any{"type": "timer", "action": act}, "Timer: "+act+".")
			},
		},
		{
			Name: "start_app",
			Description: "Start a little app on Femi's screen: \"game\" (a simple touch game, fun for kids) or \"morse\" " +
				"(learn and practise Morse code by tapping the touch pad).",
			InputSchema: object(map[string]any{"name": enum("Which app.", "game", "morse")}, "name"),
			Handle: func(ctx context.Context, raw json.RawMessage) (string, error) {
				v, _ := soleString(raw, "name")
				name, err := oneOf(v, "game", "morse")
				if err != nil {
					return "", fmt.Errorf(`"name" %v`, err)
				}
				return b.command(map[string]any{"type": "app", "name": name}, "Started the "+name+" app.")
			},
		},
		{
			Name: "show_list",
			Description: "Show a short list on Famyy's screen until it is tapped: up to 8 rows of about 24 characters, " +
				"e.g. recent calls (\"Sun 1:44  The Lalit\"), reminders or options to choose from. Say a one-line summary; " +
				"don't read the whole list out.",
			InputSchema: object(map[string]any{
				"title": str("Heading, under 24 characters, e.g. \"Recent calls\"."),
				"items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The rows, most important first; each under 24 characters."},
			}, "title", "items"),
			Handle: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					Title string   `json:"title"`
					Items []string `json:"items"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				return b.ShowList(a.Title, a.Items)
			},
		},
		{
			Name: "replay_call",
			Description: "Play a phone call Fo made back through Famyy's speaker, read from its transcript in two voices " +
				"(Fo's side and the other person's). Get the transcript with get_fo_conversation first (calls are Fo " +
				"conversations with channel \"voice\") and pass its turns in order: role \"assistant\" is speaker \"fo\", " +
				"role \"user\" is speaker \"other\". Keep their words as they are; skip empty lines. Returns as soon as it " +
				"starts playing; then reply with one very short line only.",
			InputSchema: object(map[string]any{
				"title": str("Who was called, e.g. \"The Lalit\"."),
				"lines": map[string]any{
					"type":        "array",
					"description": "The call, in order (at most 40 lines).",
					"items": object(map[string]any{
						"speaker": enum("Who said it.", "fo", "other"),
						"text":    str("What they said."),
					}, "speaker", "text"),
				},
			}, "title", "lines"),
			Handle: func(ctx context.Context, raw json.RawMessage) (string, error) {
				var a struct {
					Title string     `json:"title"`
					Lines []CallLine `json:"lines"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return "", err
				}
				return b.ReplayCall(ctx, a.Title, a.Lines)
			},
		},
		{
			Name:        "set_theme",
			Description: "Change Femi's screen theme: \"normal\" (friendly face) or \"hacker\" (green-on-black terminal look).",
			InputSchema: object(map[string]any{"name": enum("Which theme.", "normal", "hacker")}, "name"),
			Handle: func(ctx context.Context, raw json.RawMessage) (string, error) {
				v, _ := soleString(raw, "name")
				name, err := oneOf(v, "normal", "hacker")
				if err != nil {
					return "", fmt.Errorf(`"name" %v`, err)
				}
				return b.command(map[string]any{"type": "theme", "name": name}, "Theme set to "+name+".")
			},
		},
	}
}

// soleString returns argument key, or, when Claude guessed another name
// (it may call a tool before loading its schema), the only string argument.
func soleString(raw json.RawMessage, key string) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("bad arguments: %v", err)
	}
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v), nil
	}
	var only []string
	for _, v := range m {
		if s, ok := v.(string); ok {
			only = append(only, s)
		}
	}
	if len(only) == 1 {
		return strings.TrimSpace(only[0]), nil
	}
	return "", fmt.Errorf("missing argument %q", key)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func humanDuration(d interface{ Minutes() float64 }) string {
	m := d.Minutes()
	if m < 1 {
		return fmt.Sprintf("%.0f seconds", m*60)
	}
	n := int(math.Round(m))
	return fmt.Sprintf("%d minute%s", n, plural(n))
}
