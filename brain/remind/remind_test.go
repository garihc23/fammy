package remind

import (
	"strings"
	"testing"
	"time"
)

var loc = time.FixedZone("IST", 5*3600+1800)

func at(h, m int) time.Time { return time.Date(2026, 10, 17, h, m, 0, 0, loc) }

func TestParseWhen(t *testing.T) {
	now := at(16, 40)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"09:30", time.Date(2026, 10, 18, 9, 30, 0, 0, loc)},  // past today ⇒ tomorrow
		{"16:40", time.Date(2026, 10, 18, 16, 40, 0, 0, loc)}, // exactly now ⇒ tomorrow
		{"18:05", at(18, 5)},
		{"7:05", time.Date(2026, 10, 18, 7, 5, 0, 0, loc)},
		{"in 5 minutes", now.Add(5 * time.Minute)},
		{"in 1 minute", now.Add(time.Minute)},
		{"5 min", now.Add(5 * time.Minute)},
		{"in 2 hours", now.Add(2 * time.Hour)},
		{"in an hour", now.Add(time.Hour)},
		{"in 30 seconds", now.Add(30 * time.Second)},
		{"In 10 Minutes.", now.Add(10 * time.Minute)},
		{"9:30 pm", at(21, 30)},
		{"12 am", time.Date(2026, 10, 18, 0, 0, 0, 0, loc)},
		{"at 6pm", at(18, 0)},
		{"tomorrow 08:00", time.Date(2026, 10, 18, 8, 0, 0, 0, loc)},
		{"tomorrow at 20:00", time.Date(2026, 10, 18, 20, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		got, err := ParseWhen(c.in, now)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("%q: got %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "soon", "25:00", "9", "13 pm", "in 0 minutes", "12:61"} {
		if _, err := ParseWhen(bad, now); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestHuman(t *testing.T) {
	now := at(16, 40)
	if got := Human(at(16, 42), now); got != "today at 16:42" {
		t.Errorf("got %q", got)
	}
	if got := Human(time.Date(2026, 10, 18, 9, 30, 0, 0, loc), now); got != "tomorrow at 09:30" {
		t.Errorf("got %q", got)
	}
	if got := Until(at(16, 42), now); got != "in 2 minutes" {
		t.Errorf("got %q", got)
	}
	if got := Until(at(18, 10), now); got != "in 1h 30m" {
		t.Errorf("got %q", got)
	}
}

func TestSchedulerFireAckEscalate(t *testing.T) {
	now := at(16, 40)
	s := New(10 * time.Minute)
	var fired, escalated []string
	s.OnFire = func(r Reminder) { fired = append(fired, r.ID) }
	s.OnEscalate = func(r Reminder) { escalated = append(escalated, r.ID) }

	water, err := s.Add("in 2 minutes", "Drink water", "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	med, err := s.AddDaily("16:45", "Asha", "Metformin 500 mg", "Rahul", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("in 2 minutes", "x", "weekly", "", now); err == nil {
		t.Fatal("bad repeat accepted")
	}

	s.Tick(now.Add(time.Minute))
	if len(fired) != 0 {
		t.Fatalf("fired early: %v", fired)
	}
	s.Tick(now.Add(2 * time.Minute))
	if len(fired) != 1 || fired[0] != water.ID {
		t.Fatalf("fired %v", fired)
	}
	if _, ok := s.Ack(water.ID); !ok {
		t.Fatal("ack failed")
	}
	if _, ok := s.Ack(water.ID); ok {
		t.Fatal("double ack accepted")
	}

	s.Tick(at(16, 45))
	if len(fired) != 2 || fired[1] != med.ID {
		t.Fatalf("medication not fired: %v", fired)
	}
	s.Tick(at(16, 54))
	if len(escalated) != 0 {
		t.Fatal("escalated too early")
	}
	s.Tick(at(16, 55))
	s.Tick(at(16, 56))
	if len(escalated) != 1 || escalated[0] != med.ID {
		t.Fatalf("escalated %v", escalated)
	}
	list := s.List()
	if len(list) != 1 || !list[0].Pending || !list[0].At.Equal(time.Date(2026, 10, 18, 16, 45, 0, 0, loc)) {
		t.Fatalf("daily not rescheduled: %+v", list)
	}
	if d := Describe(list[0], at(16, 56)); !strings.Contains(d, "waiting for a tap") || !strings.Contains(d, "Rahul") {
		t.Fatalf("describe: %s", d)
	}
	if _, ok := s.Ack(med.ID); !ok {
		t.Fatal("ack medication")
	}
	if len(s.List()) != 1 {
		t.Fatal("daily removed after ack")
	}
	if _, ok := s.Cancel(med.ID); !ok || len(s.List()) != 0 {
		t.Fatal("cancel")
	}
}

func TestUnackedOnceExpires(t *testing.T) {
	now := at(10, 0)
	s := New(time.Minute)
	r, _ := s.Add("in 1 minute", "Stretch", Once, "", now)
	s.Tick(now.Add(time.Minute))
	if p := s.Pending(); len(p) != 1 || p[0].ID != r.ID {
		t.Fatalf("pending %v", p)
	}
	s.Tick(now.Add(2 * time.Hour))
	if len(s.List()) != 0 {
		t.Fatal("not expired")
	}
}
