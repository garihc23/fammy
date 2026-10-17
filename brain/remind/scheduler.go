package remind

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Repeat values.
const (
	Once  = "once"
	Daily = "daily"
)

// Reminder is one scheduled reminder or daily medication.
type Reminder struct {
	ID         string
	Text       string    // what it is about, e.g. "Drink water" or "Metformin 500 mg"
	Who        string    // optional: the family member it is for
	At         time.Time // next time it goes off (zero once a "once" reminder has fired)
	Clock      string    // "HH:MM" for daily reminders
	Repeat     string    // Once | Daily
	EscalateTo string    // optional: who Fo should text if it isn't acknowledged
	Medication bool

	Pending   bool      // went off, waiting for a tap
	FiredAt   time.Time // when it last went off
	Escalated bool      // the escalation for the current firing was started
}

// Scheduler fires reminders, tracks acknowledgements and escalates unacked
// ones after AckWindow. Callbacks run on the ticking goroutine, outside the
// lock; they should hand slow work to a goroutine.
type Scheduler struct {
	AckWindow  time.Duration
	OnFire     func(Reminder)
	OnEscalate func(Reminder)

	mu    sync.Mutex
	items map[string]*Reminder
	next  int
}

// New returns a scheduler that escalates after ackWindow.
func New(ackWindow time.Duration) *Scheduler {
	return &Scheduler{AckWindow: ackWindow, items: map[string]*Reminder{}}
}

// Add schedules a reminder for when (see ParseWhen). repeat is Once or Daily.
func (s *Scheduler) Add(when, text, repeat, escalateTo string, now time.Time) (Reminder, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Reminder{}, fmt.Errorf("the reminder needs a text")
	}
	repeat = strings.ToLower(strings.TrimSpace(repeat))
	if repeat == "" {
		repeat = Once
	}
	if repeat != Once && repeat != Daily {
		return Reminder{}, fmt.Errorf("repeat must be %q or %q", Once, Daily)
	}
	at, err := ParseWhen(when, now)
	if err != nil {
		return Reminder{}, err
	}
	r := Reminder{Text: text, At: at, Repeat: repeat, EscalateTo: strings.TrimSpace(escalateTo)}
	if repeat == Daily {
		r.Clock = at.Format("15:04")
	}
	return s.insert(r), nil
}

// AddDaily schedules a daily medication at clock ("HH:MM").
func (s *Scheduler) AddDaily(clock, who, what, escalateTo string, now time.Time) (Reminder, error) {
	at, err := ParseWhen(clock, now)
	if err != nil {
		return Reminder{}, err
	}
	return s.insert(Reminder{
		Text: what, Who: who, At: at, Clock: at.Format("15:04"), Repeat: Daily,
		EscalateTo: escalateTo, Medication: true,
	}), nil
}

func (s *Scheduler) insert(r Reminder) Reminder {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	r.ID = fmt.Sprintf("r%d", s.next)
	s.items[r.ID] = &r
	return r
}

// List returns all reminders, soonest first (pending ones first of all).
func (s *Scheduler) List() []Reminder {
	s.mu.Lock()
	out := make([]Reminder, 0, len(s.items))
	for _, r := range s.items {
		out = append(out, *r)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Pending != b.Pending {
			return a.Pending
		}
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return a.ID < b.ID
	})
	return out
}

// Pending returns reminders that went off and wait for a tap.
func (s *Scheduler) Pending() []Reminder {
	var out []Reminder
	for _, r := range s.List() {
		if r.Pending {
			out = append(out, r)
		}
	}
	return out
}

// Cancel removes a reminder.
func (s *Scheduler) Cancel(id string) (Reminder, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.items[strings.TrimSpace(id)]
	if !ok {
		return Reminder{}, false
	}
	delete(s.items, r.ID)
	return *r, true
}

// Ack records the tap for a reminder that went off. It reports false if
// the reminder is unknown or wasn't waiting.
func (s *Scheduler) Ack(id string) (Reminder, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.items[id]
	if !ok || !r.Pending {
		return Reminder{}, false
	}
	r.Pending = false
	out := *r
	if r.Repeat == Once {
		delete(s.items, id)
	}
	return out, true
}

// expireAfter is how long a fired one-off reminder waits for a tap before
// it is dropped from the list.
func (s *Scheduler) expireAfter() time.Duration {
	return max(time.Hour, s.AckWindow+time.Minute)
}

// Tick fires due reminders and escalates unacknowledged ones.
func (s *Scheduler) Tick(now time.Time) {
	var fire, escalate []Reminder
	s.mu.Lock()
	for id, r := range s.items {
		if r.Pending && !r.Escalated && r.EscalateTo != "" && now.Sub(r.FiredAt) >= s.AckWindow {
			r.Escalated = true
			escalate = append(escalate, *r)
		}
		if r.Repeat == Once && r.At.IsZero() && r.Pending && now.Sub(r.FiredAt) >= s.expireAfter() {
			delete(s.items, id)
			continue
		}
		if !r.At.IsZero() && !now.Before(r.At) {
			r.Pending, r.FiredAt, r.Escalated = true, now, false
			if r.Repeat == Daily {
				h, m := r.At.Hour(), r.At.Minute()
				r.At = nextClock(h, m, now, false)
			} else {
				r.At = time.Time{}
			}
			fire = append(fire, *r)
		}
	}
	s.mu.Unlock()
	sort.Slice(fire, func(i, j int) bool { return fire[i].ID < fire[j].ID })
	for _, r := range escalate {
		if s.OnEscalate != nil {
			s.OnEscalate(r)
		}
	}
	for _, r := range fire {
		if s.OnFire != nil {
			s.OnFire(r)
		}
	}
}

// Run ticks every second until ctx is done.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.Tick(now)
		}
	}
}

// Describe is a one-line description for list_reminders.
func Describe(r Reminder, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: ", r.ID)
	if r.Who != "" {
		fmt.Fprintf(&b, "%s, ", r.Who)
	}
	b.WriteString(r.Text)
	switch {
	case r.Pending:
		fmt.Fprintf(&b, " — went off at %s, waiting for a tap", r.FiredAt.Format("15:04"))
		if r.Repeat == Daily {
			fmt.Fprintf(&b, " (daily at %s)", r.Clock)
		}
	case r.Repeat == Daily:
		fmt.Fprintf(&b, " — daily at %s, next %s", r.Clock, Human(r.At, now))
	default:
		fmt.Fprintf(&b, " — %s (%s)", Human(r.At, now), Until(r.At, now))
	}
	if r.Medication {
		b.WriteString(", medication")
	}
	if r.EscalateTo != "" {
		fmt.Fprintf(&b, ", texts %s if not confirmed", r.EscalateTo)
	}
	return b.String()
}
