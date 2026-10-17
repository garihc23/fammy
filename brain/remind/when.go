// Package remind parses reminder times and runs the reminder scheduler.
package remind

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	relRe   = regexp.MustCompile(`^(?:in\s+)?(\d+(?:\.\d+)?|an?|one)\s*(s|secs?|seconds?|m|mins?|minutes?|h|hrs?|hours?)$`)
	clockRe = regexp.MustCompile(`^(?:at\s+)?(\d{1,2})(?::(\d{2}))?\s*(am|pm|a\.m\.|p\.m\.)?$`)
)

// ParseWhen understands "HH:MM" (24-hour, local; a time already past today
// means tomorrow), "9:30 am", "tomorrow 08:00", and "in N minutes" / "in N
// hours" / "in N seconds".
func ParseWhen(when string, now time.Time) (time.Time, error) {
	s := strings.ToLower(strings.TrimSpace(when))
	s = strings.TrimSuffix(s, ".")
	s = strings.Join(strings.Fields(s), " ")
	if m := relRe.FindStringSubmatch(s); m != nil {
		n := 1.0
		if m[1] != "a" && m[1] != "an" && m[1] != "one" {
			n, _ = strconv.ParseFloat(m[1], 64)
		}
		unit := time.Minute
		switch m[2][0] {
		case 's':
			unit = time.Second
		case 'h':
			unit = time.Hour
		}
		d := time.Duration(n * float64(unit))
		if d <= 0 || d > 7*24*time.Hour {
			return time.Time{}, fmt.Errorf("%q is out of range", when)
		}
		return now.Add(d).Truncate(time.Second), nil
	}
	tomorrow := false
	if rest, ok := strings.CutPrefix(s, "tomorrow"); ok {
		tomorrow = true
		s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), "at"))
	}
	if m := clockRe.FindStringSubmatch(s); m != nil && (m[2] != "" || m[3] != "") {
		h, _ := strconv.Atoi(m[1])
		mins := 0
		if m[2] != "" {
			mins, _ = strconv.Atoi(m[2])
		}
		if m[3] != "" {
			if h < 1 || h > 12 {
				return time.Time{}, fmt.Errorf("%q: hour must be 1-12 with am/pm", when)
			}
			h %= 12
			if m[3][0] == 'p' {
				h += 12
			}
		}
		if h > 23 || mins > 59 {
			return time.Time{}, fmt.Errorf("%q is not a valid time of day", when)
		}
		return nextClock(h, mins, now, tomorrow), nil
	}
	return time.Time{}, fmt.Errorf(`can't understand %q: use "HH:MM" (24-hour) or "in N minutes"`, when)
}

// nextClock is the next h:m strictly after now (or the one tomorrow).
func nextClock(h, m int, now time.Time, tomorrow bool) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if tomorrow {
		return time.Date(now.Year(), now.Month(), now.Day()+1, h, m, 0, 0, now.Location())
	}
	if !t.After(now) {
		t = time.Date(now.Year(), now.Month(), now.Day()+1, h, m, 0, 0, now.Location())
	}
	return t
}

// Human describes t relative to now, e.g. "today at 16:42",
// "tomorrow at 09:00", "Mon 3 Nov at 09:00".
func Human(t, now time.Time) string {
	ty, tm, td := t.Date()
	ny, nm, nd := now.Date()
	switch {
	case ty == ny && tm == nm && td == nd:
		return "today at " + t.Format("15:04")
	case t.Sub(time.Date(ny, nm, nd, 0, 0, 0, 0, now.Location())) < 48*time.Hour && t.After(now):
		return "tomorrow at " + t.Format("15:04")
	}
	return t.Format("Mon 2 Jan at 15:04")
}

// Until describes the delay from now to t, e.g. "in 2 minutes", "in 3 hours".
func Until(t, now time.Time) string {
	d := t.Sub(now).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "in under a minute"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute")
	case d%time.Hour == 0 || d >= 10*time.Hour:
		return plural(int(d.Round(time.Hour)/time.Hour), "hour")
	}
	return fmt.Sprintf("in %dh %02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

func plural(n int, unit string) string {
	if n == 1 {
		return "in 1 " + unit
	}
	return fmt.Sprintf("in %d %ss", n, unit)
}
