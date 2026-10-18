package femi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"femi/brain/link"
	"femi/brain/mcp"
	"femi/brain/remind"
)

// ---- fakes ----

type rec struct {
	Kind    string // state | say | command | image
	Face    string
	Line    string
	ID      string
	Text    string
	Bytes   int
	Listen  bool
	Aside   bool
	Turn    int
	Command map[string]any
}

type fakeDevice struct {
	mu        sync.Mutex
	connected bool
	recs      []rec
	notify    chan struct{}
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{connected: true, notify: make(chan struct{}, 1000)}
}

func (d *fakeDevice) add(r rec) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.connected {
		return link.ErrNotConnected
	}
	d.recs = append(d.recs, r)
	d.notify <- struct{}{}
	return nil
}

func (d *fakeDevice) Connected() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.connected
}
func (d *fakeDevice) State(face, line string) error {
	return d.add(rec{Kind: "state", Face: face, Line: line})
}
func (d *fakeDevice) Say(ctx context.Context, id, text string, pcm []byte, opts link.SayOptions) error {
	return d.add(rec{Kind: "say", ID: id, Text: text, Bytes: len(pcm), Listen: opts.Listen, Aside: opts.Aside, Turn: opts.Turn})
}
func (d *fakeDevice) Command(v any) error {
	raw, _ := json.Marshal(v)
	var m map[string]any
	json.Unmarshal(raw, &m)
	return d.add(rec{Kind: "command", Command: m})
}
func (d *fakeDevice) Snapshot(caption string, w, h int, rgb565 []byte) error {
	return d.add(rec{Kind: "snapshot", Text: caption, Bytes: len(rgb565)})
}
func (d *fakeDevice) Image(caption string, w, h int, rgb565 []byte) error {
	return d.add(rec{Kind: "image", Text: caption, Bytes: len(rgb565)})
}

func (d *fakeDevice) all() []rec {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]rec(nil), d.recs...)
}

// waitFor waits until a record matching pred exists and returns it.
func (d *fakeDevice) waitFor(t *testing.T, what string, pred func(rec) bool) rec {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		for _, r := range d.all() {
			if pred(r) {
				return r
			}
		}
		select {
		case <-d.notify:
		case <-deadline:
			t.Fatalf("timed out waiting for %s; got %+v", what, d.all())
		}
	}
}

func isSay(text string) func(rec) bool {
	return func(r rec) bool { return r.Kind == "say" && r.Text == text }
}

type call struct {
	Session, Text string
}

type fakeThinker struct {
	mu    sync.Mutex
	calls []call
	fn    func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error)
}

func (f *fakeThinker) Think(ctx context.Context, session, text string, onEvent func(Event)) (Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call{session, text})
	n := len(f.calls)
	f.mu.Unlock()
	return f.fn(ctx, n, text, onEvent)
}

func (f *fakeThinker) Calls() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

type fakeVoice struct{}

func (fakeVoice) Speak(ctx context.Context, text string, rate int) ([]byte, error) {
	return make([]byte, 2*len(text)), nil
}

type fakeEars struct {
	text string
	got  chan int
}

func (e fakeEars) Transcribe(ctx context.Context, pcm []byte, rate int) (string, error) {
	e.got <- len(pcm)
	return e.text, nil
}

func newBrain(t *testing.T, th Thinker, mod func(*Config)) (*Brain, *fakeDevice) {
	t.Helper()
	dev := newFakeDevice()
	cfg := Config{
		Device:    dev,
		Thinker:   th,
		Voice:     fakeVoice{},
		Reminders: remind.New(10 * time.Minute),
		FamilyDir: t.TempDir(),
		Logf:      t.Logf,
	}
	if mod != nil {
		mod(&cfg)
	}
	return New(cfg), dev
}

func textEvent(s string) link.Frame {
	raw, _ := json.Marshal(map[string]string{"type": "text", "text": s})
	return link.Frame{Type: link.TypeEvent, Payload: raw}
}

func replyThinker(reply string) *fakeThinker {
	return &fakeThinker{fn: func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error) {
		return Result{Reply: reply, SessionID: fmt.Sprintf("sess-%d", n)}, nil
	}}
}

// ---- pipeline ----

func TestTextTurnStatesAndReply(t *testing.T) {
	th := &fakeThinker{fn: func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error) {
		onEvent(Event{Kind: "tool_call", Tool: "ask_fo", Text: "Asking Fo: weather"})
		onEvent(Event{Kind: "fo_progress", Tool: "get_fo_reply", Text: "Working for 55s: comparing sources and prices across sites"})
		onEvent(Event{Kind: "tool_call", Tool: "set_reminder"})
		return Result{Reply: "It's **sunny**, 32°C. Details: https://wajo.ai/d/abc", SessionID: "s-1"}, nil
	}}
	b, dev := newBrain(t, th, nil)
	b.DeviceFrame(textEvent("what's the weather in Mumbai today?"))

	say := dev.waitFor(t, "reply", func(r rec) bool { return r.Kind == "say" })
	if say.Text != "It's sunny, 32°C. Details" || say.Bytes != 2*len(say.Text) {
		t.Fatalf("say = %+v", say)
	}
	var states []string
	for _, r := range dev.all() {
		if r.Kind == "state" {
			states = append(states, r.Face+"|"+r.Line)
		}
	}
	want := []string{
		"thinking|what's the weather in Mumbai today?",
		"working|Asking Fo…",
		"working|Comparing sources and prices across sites",
	}
	if strings.Join(states, "\n") != strings.Join(want, "\n") {
		t.Fatalf("states:\n%s\nwant:\n%s", strings.Join(states, "\n"), strings.Join(want, "\n"))
	}
	for _, s := range states {
		if n := len([]rune(strings.SplitN(s, "|", 2)[1])); n > lineMax {
			t.Fatalf("line too long (%d): %s", n, s)
		}
	}
}

func TestSessionKeptAndIdleReset(t *testing.T) {
	th := replyThinker("ok")
	now := time.Date(2026, 10, 17, 18, 0, 0, 0, time.Local)
	var mu sync.Mutex
	b, dev := newBrain(t, th, func(c *Config) {
		c.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	})
	turn := func(text string, n int) {
		b.DeviceFrame(textEvent(text))
		deadline := time.Now().Add(3 * time.Second)
		for {
			says := 0
			for _, r := range dev.all() {
				if r.Kind == "say" {
					says++
				}
			}
			if says >= n {
				waitIdle(t, b)
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("turn %d never answered", n)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	turn("hello", 1)
	turn("and again", 2)
	mu.Lock()
	now = now.Add(31 * time.Minute)
	mu.Unlock()
	turn("much later", 3)
	calls := th.Calls()
	if calls[0].Session != "" || calls[1].Session != "sess-1" || calls[2].Session != "" {
		t.Fatalf("sessions: %+v", calls)
	}
}

func TestBusySaysOneMoment(t *testing.T) {
	release := make(chan struct{})
	th := &fakeThinker{fn: func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error) {
		<-release
		return Result{Reply: "first done"}, nil
	}}
	b, dev := newBrain(t, th, nil)
	b.DeviceFrame(textEvent("first"))
	dev.waitFor(t, "thinking", func(r rec) bool { return r.Kind == "state" && r.Face == "thinking" })
	b.DeviceFrame(textEvent("second"))
	dev.waitFor(t, "busy", isSay(MsgBusy))
	close(release)
	dev.waitFor(t, "first reply", isSay("first done"))
	if n := len(th.Calls()); n != 1 {
		t.Fatalf("thinker called %d times", n)
	}
}

func TestTurnErrorIsSad(t *testing.T) {
	th := &fakeThinker{fn: func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error) {
		return Result{}, errors.New("claude exited 1")
	}}
	b, dev := newBrain(t, th, nil)
	b.DeviceFrame(textEvent("break please"))
	dev.waitFor(t, "apology", isSay(MsgError))
	dev.waitFor(t, "sad", func(r rec) bool { return r.Kind == "state" && r.Face == "sad" })
}

func TestCaptionOnlyWithoutVoice(t *testing.T) {
	b, dev := newBrain(t, replyThinker("Hello!"), func(c *Config) { c.Voice = nil })
	b.DeviceFrame(textEvent("hi"))
	if r := dev.waitFor(t, "reply", isSay("Hello!")); r.Bytes != 0 {
		t.Fatalf("bytes = %d", r.Bytes)
	}
}

func audio(b *Brain, n int, reason string) {
	b.DeviceFrame(link.Frame{Type: link.TypeAudioStart, Payload: []byte(`{"rate":16000,"trigger":"tap"}`)})
	for sent := 0; sent < n; sent += 3200 {
		b.DeviceFrame(link.Frame{Type: link.TypeAudioChunk, Payload: make([]byte, min(3200, n-sent))})
	}
	b.DeviceFrame(link.Frame{Type: link.TypeAudioEnd, Payload: []byte(`{"reason":"` + reason + `"}`)})
}

func TestVoicePipeline(t *testing.T) {
	ears := fakeEars{text: "set a timer for five minutes", got: make(chan int, 4)}
	th := replyThinker("Timer set.")
	b, dev := newBrain(t, th, func(c *Config) { c.Ears = ears })

	audio(b, 32000, "silence") // 1 s
	if n := <-ears.got; n != 32000 {
		t.Fatalf("stt got %d bytes", n)
	}
	dev.waitFor(t, "reply", isSay("Timer set."))
	if c := th.Calls(); len(c) != 1 || !strings.HasSuffix(c[0].Text, "] set a timer for five minutes") {
		t.Fatalf("calls %+v", c)
	}

	// Over the cap (32 s) is cut to it.
	audio(b, 40*32000, "max")
	if n := <-ears.got; n != maxAudioBytes {
		t.Fatalf("stt got %d bytes, want cap %d", n, maxAudioBytes)
	}
	dev.waitFor(t, "second reply", func(r rec) bool { return r.Kind == "say" && r.ID == "s2" })

	// Cancel drops the audio.
	audio(b, 32000, "cancel")
	time.Sleep(50 * time.Millisecond)
	if len(ears.got) != 0 || len(th.Calls()) != 2 {
		t.Fatal("cancelled utterance was processed")
	}
}

func TestVoiceEmptyTranscript(t *testing.T) {
	ears := fakeEars{text: " . ", got: make(chan int, 1)}
	th := replyThinker("never")
	b, dev := newBrain(t, th, func(c *Config) { c.Ears = ears })
	audio(b, 32000, "silence")
	dev.waitFor(t, "didn't catch", isSay(MsgDidntCatch))
	if len(th.Calls()) != 0 {
		t.Fatal("thinker called for empty transcript")
	}
}

func TestSpokenAndLines(t *testing.T) {
	cases := map[string]string{
		"- one\n- two":                        "one two",
		"**Bold** and `code`":                 "Bold and code",
		"See (https://x.y/z) for more.":       "See for more.",
		"Done!\n\n1. First\n2) Second":        "Done! First Second",
		"Your page: https://wajo.ai/d/e5377d": "Your page",
	}
	for in, want := range cases {
		if got := Spoken(in); got != want {
			t.Errorf("Spoken(%q) = %q, want %q", in, got, want)
		}
	}
	if got := foLine("Still waiting on Fo (30s)"); got != "Waiting on Fo (30s)" {
		t.Errorf("foLine = %q", got)
	}
	if got := clip("exactly twenty-four char", 24); got != "exactly twenty-four char" {
		t.Errorf("clip = %q", got)
	}
}

// ---- approvals ----

func TestRequestApproval(t *testing.T) {
	b, dev := newBrain(t, replyThinker(""), func(c *Config) { c.ApprovalTimeout = 100 * time.Millisecond })
	answer := func(ok bool) string {
		res := make(chan string, 1)
		go func() { res <- b.RequestApproval(context.Background(), "Text Rahul: on my way") }()
		before := len(dev.all())
		cmd := dev.waitFor(t, "approve command", func(r rec) bool {
			return r.Kind == "command" && r.Command["type"] == "approve" && len(dev.all()) > before
		})
		if cmd.Command["text"] != "Text Rahul: on my way" {
			t.Fatalf("command %+v", cmd.Command)
		}
		id := cmd.Command["id"].(string)
		raw, _ := json.Marshal(map[string]any{"type": "approval", "id": id, "ok": ok})
		b.DeviceFrame(link.Frame{Type: link.TypeEvent, Payload: raw})
		return <-res
	}
	if got := answer(true); got != "approved" {
		t.Fatalf("tap: %q", got)
	}
	// Make sure the second approval waits for its own (new) command.
	dev.mu.Lock()
	dev.recs = nil
	dev.mu.Unlock()
	if got := answer(false); got != "declined" {
		t.Fatalf("hold: %q", got)
	}

	if got := b.RequestApproval(context.Background(), "Book a table"); got != "no answer (treat as declined)" {
		t.Fatalf("timeout: %q", got)
	}

	res := make(chan string, 1)
	b.cfg.ApprovalTimeout = 5 * time.Second
	go func() { res <- b.RequestApproval(context.Background(), "Call the dentist") }()
	dev.waitFor(t, "third command", func(r rec) bool { return r.Kind == "command" && r.Command["text"] == "Call the dentist" })
	b.DeviceDisconnected()
	if got := <-res; got != "device disconnected (treat as declined)" {
		t.Fatalf("disconnect: %q", got)
	}

	dev.mu.Lock()
	dev.connected = false
	dev.mu.Unlock()
	if got := b.RequestApproval(context.Background(), "x"); got != "no device connected (treat as declined)" {
		t.Fatalf("no device: %q", got)
	}
}

// ---- photos ----

func writePNG(t *testing.T, path string, w, h int, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}

func TestShowPhotos(t *testing.T) {
	b, dev := newBrain(t, replyThinker(""), func(c *Config) { c.SlideInterval = 10 * time.Millisecond })
	dir := filepath.Join(b.cfg.FamilyDir, "photos", "Riya")
	os.MkdirAll(dir, 0o755)
	os.MkdirAll(filepath.Join(b.cfg.FamilyDir, "photos", "Goa trip"), 0o755)
	writePNG(t, filepath.Join(dir, "beach.png"), 64, 64, color.RGBA{200, 100, 0, 255})
	writePNG(t, filepath.Join(dir, "cake.png"), 30, 20, color.RGBA{0, 100, 200, 255})
	os.WriteFile(filepath.Join(dir, "captions.txt"), []byte("beach.png: Riya at the beach, 2019\n"), 0o644)

	got, err := b.ShowPhotos("riya")
	if err != nil || got != "Showing 2 photos of Riya (captions: Riya at the beach, 2019)" {
		t.Fatalf("ShowPhotos = %q, %v", got, err)
	}
	first := dev.waitFor(t, "first image", func(r rec) bool { return r.Kind == "image" })
	second := dev.waitFor(t, "second image", func(r rec) bool { return r.Kind == "image" && r.Text == "Riya" })
	if first.Text != "Riya at the beach, 2019" || first.Bytes != 320*240*2 || second.Bytes != 320*240*2 {
		t.Fatalf("images %+v %+v", first, second)
	}

	got, _ = b.ShowPhotos("grandpa")
	if got != `No album matches "grandpa". Available albums: Goa trip, Riya.` {
		t.Fatalf("not found: %q", got)
	}
	got, _ = b.ShowPhotos("goa")
	if got != "The Goa trip album has no photos yet." {
		t.Fatalf("empty album: %q", got)
	}
	if a := matchAlbum([]string{"Goa trip", "Riya"}, "Riya's birthday"); a != "Riya" {
		t.Fatalf("matchAlbum = %q", a)
	}
}

// ---- reminders ----

func TestReminderFireAckAndEscalate(t *testing.T) {
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.Local)
	esc := replyThinker("I texted Rahul.")
	b, dev := newBrain(t, replyThinker(""), func(c *Config) {
		c.Now = func() time.Time { return now }
		c.Escalator = esc
		c.Profile = &Profile{Members: []Member{{Name: "Asha", Relation: "grandmother"}}}
	})
	tools := map[string]func(context.Context, json.RawMessage) (string, error){}
	for _, tl := range b.Tools() {
		tools[tl.Name] = tl.Handle
	}
	msg, err := tools["set_reminder"](context.Background(), json.RawMessage(`{"when":"in 5 minutes","text":"Drink water"}`))
	if err != nil || msg != "Reminder r1 set for today at 09:05 (in 5 minutes)." {
		t.Fatalf("set_reminder = %q, %v", msg, err)
	}
	if _, err := tools["set_reminder"](context.Background(), json.RawMessage(`{"when":"whenever","text":"x"}`)); err == nil {
		t.Fatal("bad time accepted")
	}
	// Claude sometimes guesses argument names before loading the schema.
	if msg, err := tools["set_reminder"](context.Background(), json.RawMessage(`{"time":"in 2 minutes","text":"Stretch"}`)); err != nil || !strings.HasPrefix(msg, "Reminder r2 set") {
		t.Fatalf("time alias: %q %v", msg, err)
	}
	b.cfg.Reminders.Cancel("r2")

	sched := b.cfg.Reminders
	sched.Tick(now.Add(5 * time.Minute))
	cmd := dev.waitFor(t, "reminder command", func(r rec) bool { return r.Kind == "command" && r.Command["type"] == "reminder" })
	if cmd.Command["id"] != "r1" || cmd.Command["text"] != "Drink water" || cmd.Command["urgent"] != true {
		t.Fatalf("command %+v", cmd.Command)
	}
	dev.waitFor(t, "spoken reminder", isSay("Reminder: Drink water."))
	b.DeviceFrame(link.Frame{Type: link.TypeEvent, Payload: []byte(`{"type":"ack","id":"r1"}`)})
	dev.waitFor(t, "thanks", isSay(MsgNoted))
	dev.waitFor(t, "happy", func(r rec) bool { return r.Kind == "state" && r.Face == "happy" })

	// Medication with escalation, never acked.
	med, _ := sched.AddDaily("09:30", "Asha", "Metformin 500 mg", "Rahul", now)
	sched.Tick(time.Date(2026, 10, 17, 9, 30, 0, 0, time.Local))
	dev.waitFor(t, "medicine spoken", isSay("Asha, it's time for your Metformin 500 mg."))
	sched.Tick(time.Date(2026, 10, 17, 9, 40, 0, 0, time.Local))
	dev.waitFor(t, "escalation state", func(r rec) bool { return r.Kind == "state" && r.Face == "working" && r.Line == "Texting Rahul…" })
	dev.waitFor(t, "escalation done", func(r rec) bool { return r.Kind == "state" && r.Line == "Rahul was told" })
	calls := esc.Calls()
	if len(calls) != 1 || calls[0].Session != "" ||
		!strings.Contains(calls[0].Text, "Asha (grandmother) hasn't confirmed the 09:30 medicine (Metformin 500 mg)") ||
		!strings.Contains(calls[0].Text, "text Rahul") {
		t.Fatalf("escalation calls %+v", calls)
	}
	for _, r := range dev.all() {
		if r.Kind == "say" && strings.Contains(r.Text, "Rahul") {
			t.Fatalf("escalation was spoken: %+v", r)
		}
	}

	list, _ := tools["list_reminders"](context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(list, med.ID+": Asha, Metformin 500 mg — went off at 09:30, waiting to be confirmed") {
		t.Fatalf("list = %q", list)
	}
	if msg, err := tools["cancel_reminder"](context.Background(), json.RawMessage(`{"id":"`+med.ID+`"}`)); err != nil || !strings.Contains(msg, "Cancelled") {
		t.Fatalf("cancel = %q %v", msg, err)
	}
	if _, err := tools["cancel_reminder"](context.Background(), json.RawMessage(`{"id":"r99"}`)); err == nil {
		t.Fatal("cancel unknown accepted")
	}
}

func TestDeviceCommandTools(t *testing.T) {
	b, dev := newBrain(t, replyThinker(""), nil)
	tools := map[string]func(context.Context, json.RawMessage) (string, error){}
	for _, tl := range b.Tools() {
		tools[tl.Name] = tl.Handle
	}
	cases := []struct {
		tool, args string
		want       map[string]any
	}{
		{"set_timer", `{"minutes":5}`, map[string]any{"type": "timer", "minutes": float64(5)}},
		{"set_timer", `{"minutes":"10"}`, map[string]any{"type": "timer", "minutes": float64(10)}},
		{"timer_control", `{"action":"pomodoro"}`, map[string]any{"type": "timer", "action": "pomodoro"}},
		{"start_app", `{"name":"morse"}`, map[string]any{"type": "app", "name": "morse"}},
		{"set_theme", `{"name":"Hacker"}`, map[string]any{"type": "theme", "name": "hacker"}},
		{"set_theme", `{"theme":"normal"}`, map[string]any{"type": "theme", "name": "normal"}},
		{"start_app", `{"app":"game"}`, map[string]any{"type": "app", "name": "game"}},
	}
	for _, c := range cases {
		if _, err := tools[c.tool](context.Background(), json.RawMessage(c.args)); err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
		recs := dev.all()
		got := recs[len(recs)-1].Command
		a, _ := json.Marshal(got)
		w, _ := json.Marshal(c.want)
		if string(a) != string(w) {
			t.Fatalf("%s: command %s, want %s", c.tool, a, w)
		}
	}
	if _, err := tools["start_app"](context.Background(), json.RawMessage(`{"name":"doom"}`)); err == nil {
		t.Fatal("bad app accepted")
	}
	if _, err := tools["set_timer"](context.Background(), json.RawMessage(`{"minutes":500}`)); err == nil {
		t.Fatal("500 minutes accepted")
	}
	dev.mu.Lock()
	dev.connected = false
	dev.mu.Unlock()
	if _, err := tools["set_theme"](context.Background(), json.RawMessage(`{"name":"normal"}`)); err == nil {
		t.Fatal("no device: expected error")
	}
	if len(ToolNames()) != 13 {
		t.Fatalf("ToolNames = %v", ToolNames())
	}
}

func TestTooShortIgnoresSoundTags(t *testing.T) {
	for s, want := range map[string]bool{
		"*ding*":               true,
		"[music]":              true,
		"(coughs) ":            true,
		"foreign":              true,
		"Foreign.":             true,
		"*ding* what time?":    false,
		"foreign languages":    false,
		"you":                  true,
		"You.":                 true,
		"Thanks for watching!": true,
		"you there?":           false,
		"hi":                   false,
	} {
		if got := tooShort(s); got != want {
			t.Errorf("tooShort(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestFoTask(t *testing.T) {
	for req, want := range map[string]string{
		"Asking Fo: Call the salon and book a haircut for Saturday": "call",
		"Asking Fo: Text Priya that dinner is at 8":                 "message",
		"Asking Fo: Check my email for the flight confirmation":     "email",
		"Asking Fo: Order two samosas from the usual place":         "shop",
		"Asking Fo: Book a table for four tomorrow at 7":            "calendar",
		"Asking Fo: What's the weather in Bengaluru today?":         "web",
	} {
		if got := foTask(req); got != want {
			t.Errorf("foTask(%q) = %q, want %q", req, got, want)
		}
	}
}

func TestKeepListening(t *testing.T) {
	for _, c := range []struct {
		request, reply string
		want           bool
	}{
		{"what's the weather?", "Sunny and 28 degrees.", true},
		{"thanks", "You're welcome!", false},
		{"Thank you, Famyy.", "Anytime.", false},
		{"okay that's all", "Alright.", false},
		{"no", "Okay, I won't.", false},
		{"stop", "Stopping.", false},
		{"stop the timer", "Timer stopped.", true},
		{"no, call Flute instead", "Calling Flute.", true},
		{"you can go to sleep for now", "Okay.", false},
		{"tell me a joke", "Why did the cat sit on the laptop? Bye for now!", false},
		{"say bye to grandma for me", "I'll text her.", true},
	} {
		if got := keepListening(c.request, c.reply); got != c.want {
			t.Errorf("keepListening(%q, %q) = %v, want %v", c.request, c.reply, got, c.want)
		}
	}
}

func TestHeardCommandPrecedesThinkingState(t *testing.T) {
	long := strings.Repeat("please find me a good salon near the office ", 10) // ~440 characters
	b, dev := newBrain(t, replyThinker("ok"), nil)
	b.DeviceFrame(textEvent(long))
	dev.waitFor(t, "reply", func(r rec) bool { return r.Kind == "say" })

	recs := dev.all()
	hi, ti := -1, -1
	for i, r := range recs {
		if r.Kind == "command" && r.Command["type"] == "heard" && hi < 0 {
			hi = i
		}
		if r.Kind == "state" && r.Face == "thinking" && ti < 0 {
			ti = i
		}
	}
	if hi < 0 || ti < 0 || hi > ti {
		t.Fatalf("want the heard command before the thinking state, got heard at %d, thinking at %d: %+v", hi, ti, recs)
	}
	heard, _ := recs[hi].Command["text"].(string)
	if n := len([]rune(heard)); n != heardMax || !strings.HasPrefix(heard, "please find me a good salon") || !strings.HasSuffix(heard, "…") {
		t.Errorf("heard text is %d runes: %q", n, heard)
	}
	if n := len([]rune(recs[ti].Line)); n != lineMax {
		t.Errorf("thinking line is %d runes, want it cut to %d", n, lineMax)
	}
	raw, _ := json.Marshal(heardCommand{Type: "heard", Text: "hi"})
	if string(raw) != `{"type":"heard","text":"hi"}` {
		t.Errorf("key order: %s", raw)
	}
}

func TestReplyListensUnlessDone(t *testing.T) {
	th := &fakeThinker{fn: func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error) {
		return Result{Reply: fmt.Sprintf("Reply %d", n), SessionID: "s"}, nil
	}}
	b, dev := newBrain(t, th, nil)
	b.DeviceFrame(textEvent("what's up"))
	if r := dev.waitFor(t, "reply 1", isSay("Reply 1")); !r.Listen {
		t.Error("a normal reply should keep listening")
	}
	waitIdle(t, b)
	b.DeviceFrame(textEvent("thanks, that's all"))
	if r := dev.waitFor(t, "reply 2", isSay("Reply 2")); r.Listen {
		t.Error("after \"that's all\" Famyy should stop listening")
	}
}

func TestStopDropsTheTurn(t *testing.T) {
	started := make(chan struct{})
	th := &fakeThinker{fn: func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error) {
		close(started)
		<-ctx.Done() // a long Fo errand
		return Result{SessionID: "s"}, ctx.Err()
	}}
	b, dev := newBrain(t, th, nil)
	b.DeviceFrame(textEvent("call all the restaurants"))
	<-started
	b.DeviceFrame(link.Frame{Type: link.TypeEvent, Payload: []byte(`{"type":"stop"}`)})
	// the turn slot frees up, and nothing is said about it
	deadline := time.Now().Add(3 * time.Second)
	for !b.tryTurn() {
		if time.Now().After(deadline) {
			t.Fatal("the stopped turn still holds the turn slot")
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.endTurn()
	for _, r := range dev.all() {
		if r.Kind == "say" {
			t.Errorf("said %q after a stop", r.Text)
		}
	}
}

func TestQuietFollowUpMiss(t *testing.T) {
	th := &fakeThinker{fn: func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error) {
		return Result{Reply: "hi"}, nil
	}}
	b, dev := newBrain(t, th, func(c *Config) { c.Ears = fakeEars{text: "*ding*", got: make(chan int, 10)} })
	pcm := make([]byte, minAudioBytes*2)
	for _, trigger := range []string{"followup", "wake"} {
		b.DeviceFrame(link.Frame{Type: link.TypeAudioStart, Payload: []byte(`{"rate":16000,"trigger":"` + trigger + `"}`)})
		b.DeviceFrame(link.Frame{Type: link.TypeAudioChunk, Payload: pcm})
		b.DeviceFrame(link.Frame{Type: link.TypeAudioEnd, Payload: []byte(`{"reason":"silence"}`)})
		if trigger == "followup" {
			dev.waitFor(t, "idle after an empty follow-up", func(r rec) bool { return r.Kind == "state" && r.Face == "idle" })
			for _, r := range dev.all() {
				if r.Kind == "say" {
					t.Fatalf("an empty follow-up said %q", r.Text)
				}
			}
		}
	}
	if r := dev.waitFor(t, "didn't catch", isSay(MsgDidntCatch)); !r.Listen {
		t.Error("after a missed wake request Famyy should listen again")
	}
}

func (fakeVoice) SpeakAs(ctx context.Context, voiceID, text string, rate int) ([]byte, error) {
	return make([]byte, 2*len(text)), nil
}

func TestShowListAndReplayCall(t *testing.T) {
	b, dev := newBrain(t, replyThinker("ok"), func(c *Config) { c.FoVoice, c.CallVoice = "fo-voice", "them-voice" })
	tools := map[string]func(context.Context, json.RawMessage) (string, error){}
	for _, tl := range b.Tools() {
		tools[tl.Name] = tl.Handle
	}
	if _, err := tools["show_list"](context.Background(), json.RawMessage(`{"title":"Recent calls","items":["Sun 1:44 The Lalit","Sun 1:44 Flute | 24 Hrs",""]}`)); err != nil {
		t.Fatal(err)
	}
	r := dev.waitFor(t, "list", func(r rec) bool { return r.Kind == "command" && r.Command["type"] == "list" })
	if r.Command["items"] != "Sun 1:44 The Lalit|Sun 1:44 Flute / 24 Hrs" || r.Command["title"] != "Recent calls" {
		t.Errorf("list command = %v", r.Command)
	}
	if _, err := tools["show_list"](context.Background(), json.RawMessage(`{"title":"x","items":[]}`)); err == nil {
		t.Error("empty list accepted")
	}

	out, err := tools["replay_call"](context.Background(), json.RawMessage(`{"title":"The Lalit","lines":[
		{"speaker":"fo","text":"Hi, are you open?"},{"speaker":"other","text":""},{"speaker":"other","text":"Haan, open hai."}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2 lines") {
		t.Errorf("replay_call = %q, want 2 lines (the empty one skipped)", out)
	}
	say := dev.waitFor(t, "replay", isSay("Call: The Lalit"))
	want := 2*len("Hi, are you open?") + 2*len("Haan, open hai.") + SampleRate*2*callGapMs/1000
	if say.Bytes != want || say.Listen {
		t.Errorf("replay say = %+v, want %d bytes and no listening after", say, want)
	}
}

func TestQuietReplyAfterDecline(t *testing.T) {
	b, dev := newBrain(t, replyThinker("[quiet]"), nil)
	b.DeviceFrame(textEvent("call the salon"))
	r := dev.waitFor(t, "an empty say", func(r rec) bool { return r.Kind == "say" })
	if r.Text != "" || r.Bytes != 0 || r.Listen {
		t.Errorf("quiet reply sent %+v, want an empty say that doesn't listen", r)
	}
	for _, s := range []string{"[quiet]", "quiet", " [Quiet]. "} {
		if !quietReply(s) {
			t.Errorf("quietReply(%q) = false", s)
		}
	}
	if quietReply("Quiet hours start at ten.") {
		t.Error("a real reply about quiet hours was treated as quiet")
	}
}

func TestDropLoops(t *testing.T) {
	for in, want := range map[string]string{
		strings.Repeat("He will kill you. ", 7):                "",
		"What's the weather? Thank you. Thank you. Thank you.": "What's the weather?",
		"thank you thank you thank you thank you":              "",
		"Call Aditya and tell him I'm late.":                   "Call Aditya and tell him I'm late.",
		"No, no, no.":                                          "No, no, no.",
		"Yes. Yes.":                                            "Yes. Yes.",
		"Remind me at 8. Remind me at 9. Remind me at 10.":     "Remind me at 8. Remind me at 9. Remind me at 10.",
	} {
		if got := dropLoops(in); got != want {
			t.Errorf("dropLoops(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAcknowledgeReminderByVoice(t *testing.T) {
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.Local)
	var seen []string
	th := &fakeThinker{fn: func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error) {
		seen = append(seen, text)
		return Result{Reply: "Well done!"}, nil
	}}
	b, dev := newBrain(t, th, func(c *Config) { c.Now = func() time.Time { return now } })
	sched := b.cfg.Reminders
	if out, _ := b.AcknowledgeReminder(""); out != "Nothing is waiting to be confirmed right now." {
		t.Errorf("nothing waiting: %q", out)
	}
	med, _ := sched.AddDaily("09:30", "Asha", "Metformin 500 mg", "Rahul", now)
	sched.Tick(time.Date(2026, 10, 18, 9, 30, 0, 0, time.Local))
	dev.waitFor(t, "medicine spoken", isSay("Asha, it's time for your Metformin 500 mg."))

	// the turn is told what is waiting
	b.DeviceFrame(textEvent("I have taken my medicine"))
	dev.waitFor(t, "reply", isSay("Well done!"))
	if len(seen) != 1 || !strings.Contains(seen[0], "waiting to be confirmed: "+med.ID+" Asha: Metformin 500 mg (went off 09:30)") {
		t.Errorf("turn text %q lacks the waiting reminder", seen)
	}

	out, err := b.AcknowledgeReminder("")
	if err != nil || !strings.Contains(out, "Confirmed: Metformin 500 mg (Asha). It stays scheduled daily at 09:30") {
		t.Fatalf("acknowledge = %q, %v", out, err)
	}
	dev.waitFor(t, "card closed", func(r rec) bool {
		return r.Kind == "command" && r.Command["type"] == "reminder_done" && r.Command["id"] == med.ID
	})
	if len(sched.Pending()) != 0 || len(sched.List()) != 1 {
		t.Errorf("after the ack: pending %v, list %v (the daily schedule must stay)", sched.Pending(), sched.List())
	}
	sched.Tick(time.Date(2026, 10, 18, 9, 45, 0, 0, time.Local)) // past the ack window: no text to Rahul
	if _, err := b.AcknowledgeReminder(med.ID); err == nil {
		t.Error("acknowledged twice")
	}
}

type fakeCamera struct{ jpg []byte }

func (c fakeCamera) Snap(ctx context.Context) ([]byte, error) { return c.jpg, nil }

func TestLookAtCamera(t *testing.T) {
	var buf bytes.Buffer
	jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 960, 720)), nil)
	b, dev := newBrain(t, replyThinker("ok"), func(c *Config) { c.Camera = fakeCamera{buf.Bytes()} })
	var look mcp.Tool
	for _, tl := range b.Tools() {
		if tl.Name == "look_at_camera" {
			look = tl
		}
	}
	text, img, mime, err := look.HandleImage(context.Background(), json.RawMessage(`{}`))
	if err != nil || mime != "image/jpeg" || !bytes.Equal(img, buf.Bytes()) || !strings.Contains(text, "not saved") {
		t.Fatalf("look_at_camera = %q, %d bytes, %q, %v", text, len(img), mime, err)
	}
	if r := dev.waitFor(t, "polaroid", func(r rec) bool { return r.Kind == "snapshot" }); r.Bytes != snapW*snapH*2 {
		t.Errorf("polaroid has %d bytes, want %d", r.Bytes, snapW*snapH*2)
	}

	b2, _ := newBrain(t, replyThinker("ok"), nil) // no camera
	if _, _, _, err := b2.LookAtCamera(context.Background()); err == nil {
		t.Error("no camera: expected an error")
	}
}

func TestPhotoAgeInTheTimeStamp(t *testing.T) {
	now := time.Date(2026, 10, 18, 3, 44, 0, 0, time.Local)
	var seen []string
	th := &fakeThinker{fn: func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error) {
		seen = append(seen, text)
		return Result{Reply: "ok"}, nil
	}}
	var buf bytes.Buffer
	jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 64, 48)), nil)
	b, dev := newBrain(t, th, func(c *Config) {
		c.Camera = fakeCamera{buf.Bytes()}
		c.Now = func() time.Time { return now }
	})
	b.DeviceFrame(textEvent("hello"))
	dev.waitFor(t, "first reply", isSay("ok"))
	if strings.Contains(seen[0], "camera") {
		t.Errorf("no photo yet, but the stamp says %q", seen[0])
	}
	now = now.Add(-2 * time.Minute) // the photo was taken at 03:42
	b.LookAtCamera(context.Background())
	now = now.Add(2 * time.Minute)
	waitIdle(t, b)
	b.DeviceFrame(textEvent("what colours suit me?"))
	deadline := time.Now().Add(3 * time.Second)
	for len(th.Calls()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls := th.Calls(); len(calls) < 2 || !strings.Contains(calls[1].Text, "| last camera photo 03:42 (2 min ago)]") {
		t.Errorf("stamp lacks the photo's age: %+v", calls)
	}
}

func TestStatusShowsATurnInFlight(t *testing.T) {
	release := make(chan struct{})
	th := &fakeThinker{fn: func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error) {
		<-release // Fo on a long errand
		return Result{Reply: "found three outfits"}, nil
	}}
	b, dev := newBrain(t, th, nil)
	if b.Status().Busy {
		t.Fatal("busy before anything happened")
	}
	b.DeviceFrame(textEvent("find me wedding outfits"))
	deadline := time.Now().Add(3 * time.Second)
	for !b.Status().Turn {
		if time.Now().After(deadline) {
			t.Fatal("status never showed the turn")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !b.Status().Busy {
		t.Error("a turn in flight should be busy")
	}
	close(release)
	dev.waitFor(t, "reply", isSay("found three outfits"))
	for b.Status().Busy {
		if time.Now().After(deadline) {
			t.Fatal("still busy after the reply")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A stop (or a failure) never loses the conversation; "start over" does.
func TestConversationSurvivesAStop(t *testing.T) {
	gate := make(chan struct{})
	th := &fakeThinker{fn: func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error) {
		switch n {
		case 1:
			return Result{Reply: "Teal is lovely.", SessionID: "sess-1"}, nil
		case 2:
			close(gate)
			<-ctx.Done() // stopped before Claude reported anything
			return Result{}, ctx.Err()
		}
		return Result{Reply: fmt.Sprintf("reply %d", n), SessionID: "sess-1"}, nil
	}}
	b, dev := newBrain(t, th, nil)
	b.DeviceFrame(textEvent("my favourite colour is teal"))
	dev.waitFor(t, "first reply", isSay("Teal is lovely."))
	waitIdle(t, b)
	b.DeviceFrame(textEvent("write me a long story"))
	<-gate
	b.DeviceFrame(link.Frame{Type: link.TypeEvent, Payload: []byte(`{"type":"stop"}`)})
	deadline := time.Now().Add(3 * time.Second)
	for !b.tryTurn() { // the stopped turn lets go of the slot
		if time.Now().After(deadline) {
			t.Fatal("the stopped turn still holds the slot")
		}
		time.Sleep(5 * time.Millisecond)
	}
	b.endTurn()
	b.DeviceFrame(textEvent("what's my favourite colour?"))
	dev.waitFor(t, "third reply", isSay("reply 3"))
	if c := th.Calls(); len(c) != 3 || c[2].Session != "sess-1" {
		t.Fatalf("after a stop the next turn should continue sess-1: %+v", c)
	}
	waitIdle(t, b)
	b.DeviceFrame(textEvent("okay, let's start over"))
	dev.waitFor(t, "fourth reply", isSay("reply 4"))
	calls := th.Calls()
	if last := calls[len(calls)-1]; last.Session != "" {
		t.Errorf("\"start over\" should begin a fresh conversation, got session %q", last.Session)
	}
}

func TestStartOverPhrases(t *testing.T) {
	for s, want := range map[string]bool{
		"let's start over":         true,
		"Forget all that.":         true,
		"new conversation please":  true,
		"start the timer":          false,
		"don't forget my medicine": false,
		"what did we talk about?":  false,
	} {
		if got := startOver.MatchString(s); got != want {
			t.Errorf("startOver(%q) = %v, want %v", s, got, want)
		}
	}
}

// Only the answer to the request in progress ends what is going on on the
// device; reminders, "noted" and "one moment" are asides.
func TestAsidesAndCaptionCap(t *testing.T) {
	long := strings.Repeat("The light blue suit is lovely. ", 30) // ~930 characters
	b, dev := newBrain(t, replyThinker(long), nil)
	b.DeviceFrame(textEvent("find wedding outfits"))
	dev.waitFor(t, "the last sentence", func(r rec) bool { return r.Kind == "say" && !r.Aside })
	var says []rec
	for _, r := range dev.all() {
		if r.Kind == "say" {
			says = append(says, r)
		}
	}
	var texts []string
	for i, r := range says {
		if r.Aside != (i < len(says)-1) {
			t.Errorf("sentence %d: aside %v (only the last one isn't)", i, r.Aside)
		}
		texts = append(texts, r.Text)
	}
	if len(says) < 2 || strings.Join(texts, " ") != strings.TrimSpace(long) {
		t.Errorf("the reply should come sentence by sentence, all of it: %d says", len(says))
	}
	if c := caption(strings.Repeat("word ", 200)); utf8.RuneCountInString(c) > captionMax+1 || !strings.HasSuffix(c, "…") {
		t.Errorf("a single long sentence is still capped: %d characters", utf8.RuneCountInString(c))
	}

	b.speakAside(context.Background(), MsgNoted)
	if r := dev.waitFor(t, "noted", isSay(MsgNoted)); !r.Aside || r.Listen {
		t.Errorf("noted should be an aside without listening: %+v", r)
	}
	b.reminderFired(remind.Reminder{ID: "r9", Text: "Drink water"})
	if r := dev.waitFor(t, "reminder", isSay("Reminder: Drink water.")); !r.Aside {
		t.Errorf("a reminder announcement should be an aside: %+v", r)
	}
	if got := caption("short"); got != "short" {
		t.Errorf("caption(short) = %q", got)
	}
}

type fakeWatcher struct {
	release chan struct{}
	conv    chan string
}

func (w fakeWatcher) WaitForFo(ctx context.Context, conv string) (string, string, error) {
	w.conv <- conv
	<-w.release
	return "complete", "Booked for 7 pm at Toit.", nil
}

func (w fakeWatcher) CheckFo(ctx context.Context, conv string) (string, error) { return "", nil }

// a call errand: Fo reports "complete" when it dials, and the outcome a little later
type callWatcher struct{ checks *int }

func (w callWatcher) WaitForFo(ctx context.Context, conv string) (string, string, error) {
	return "complete", "Calling Karan now with your reminder. I'll report back as soon as the call wraps.", nil
}

func (w callWatcher) CheckFo(ctx context.Context, conv string) (string, error) {
	*w.checks++
	if *w.checks < 2 {
		return "Calling Karan now with your reminder. I'll report back as soon as the call wraps.", nil
	}
	return "Calling Karan now with your reminder. I'll report back as soon as the call wraps. Done: Karan picked up and said he'll set an alarm.", nil
}

func TestCallErrandWaitsForTheOutcome(t *testing.T) {
	defer func(e time.Duration) { followEvery = e }(followEvery)
	followEvery = 10 * time.Millisecond
	checks := 0
	th := &fakeThinker{fn: func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error) {
		if n == 1 {
			onEvent(Event{Kind: "tool_call", Tool: "ask_fo", Text: "Asking Fo: Call Karan about his flight"})
			onEvent(Event{Kind: "fo_progress", Tool: "ask_fo", Text: "Working", Status: "working", FoConversation: "c9"})
			return Result{Reply: "Fo's calling him.", SessionID: "s"}, nil
		}
		return Result{Reply: "Karan picked up and will set an alarm.", SessionID: "s"}, nil
	}}
	b, dev := newBrain(t, th, func(c *Config) { c.FoWatcher = callWatcher{&checks} })
	b.DeviceFrame(textEvent("call Karan about his flight"))
	dev.waitFor(t, "report", isSay("Karan picked up and will set an alarm."))
	calls := th.Calls()
	if len(calls) != 2 || !strings.Contains(calls[1].Text, "Done: Karan picked up and said he'll set an alarm.") {
		t.Errorf("the report should carry the call's outcome: %+v", calls)
	}
	if checks != 2 {
		t.Errorf("checked back %d times, want 2", checks)
	}
}

// A Fo errand still working when the turn ends goes on in the background;
// when Fo settles, the brain starts a turn to report it.
func TestFoErrandInTheBackground(t *testing.T) {
	th := &fakeThinker{fn: func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error) {
		if n == 1 {
			onEvent(Event{Kind: "tool_call", Tool: "ask_fo", Text: "Asking Fo: Book a table for 4 at Toit tonight"})
			onEvent(Event{Kind: "fo_progress", Tool: "ask_fo", Text: "Working for 40s", Status: "working", FoConversation: "c1"})
			return Result{Reply: "Fo's on it, I'll tell you when it's done.", SessionID: "s"}, nil
		}
		return Result{Reply: "Your table at Toit is booked for 7.", SessionID: "s"}, nil
	}}
	w := fakeWatcher{release: make(chan struct{}), conv: make(chan string, 1)}
	b, dev := newBrain(t, th, func(c *Config) { c.FoWatcher = w })
	b.DeviceFrame(textEvent("book a table at Toit"))

	away := dev.waitFor(t, "fo away", func(r rec) bool { return r.Kind == "command" && r.Command["state"] == "away" })
	if away.Command["id"] != "f1" || away.Command["task"] == "" {
		t.Errorf("away command = %v", away.Command)
	}
	dev.waitFor(t, "first reply", isSay("Fo's on it, I'll tell you when it's done."))
	if c := <-w.conv; c != "c1" {
		t.Errorf("watching %q, want c1", c)
	}
	if s := b.Status(); s.Errands != 1 || !s.Busy {
		t.Errorf("status while Fo is out = %+v", s)
	}

	close(w.release) // Fo is done
	dev.waitFor(t, "report", isSay("Your table at Toit is booked for 7."))
	recs := dev.all()
	back, report := -1, -1
	for i, r := range recs {
		if r.Kind == "command" && r.Command["state"] == "back" && r.Command["id"] == "f1" && r.Command["ok"] == true {
			back = i
		}
		if r.Kind == "say" && r.Text == "Your table at Toit is booked for 7." {
			report = i
		}
	}
	if back < 0 || back > report {
		t.Errorf("fo back (%d) should come before the report (%d): %+v", back, report, recs)
	}
	calls := th.Calls()
	if len(calls) != 2 || !strings.Contains(calls[1].Text, "[Fo has finished an errand from earlier (Book a table for 4 at Toit tonight). Status: complete. Fo says: Booked for 7 pm at Toit.]") || calls[1].Session != "s" {
		t.Errorf("report turn = %+v", calls)
	}
	deadline := time.Now().Add(3 * time.Second)
	for b.Status().Errands != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if b.Status().Errands != 0 {
		t.Error("the errand is still counted after its report")
	}
}

func TestIntroduceYourself(t *testing.T) {
	th := replyThinker("should not be asked")
	b, dev := newBrain(t, th, nil)
	b.DeviceFrame(textEvent("Hey Fammy, introduce yourself."))
	dev.waitFor(t, "last line", isSay(Intro[len(Intro)-1]))
	var says []rec
	for _, r := range dev.all() {
		if r.Kind == "say" {
			says = append(says, r)
		}
	}
	if len(says) != len(Intro) {
		t.Fatalf("said %d lines, want %d: %+v", len(says), len(Intro), says)
	}
	for i, r := range says {
		last := i == len(Intro)-1
		if r.Text != Intro[i] || r.Aside == last || r.Listen != last || r.Bytes == 0 {
			t.Errorf("line %d = %+v", i, r)
		}
	}
	if len(th.Calls()) != 0 {
		t.Error("the introduction is scripted: Claude shouldn't be asked")
	}
	for s, want := range map[string]bool{"introduce yourself": true, "please introduce your self to the judges": true, "introduce me to Riya": false} {
		if introRequest.MatchString(s) != want {
			t.Errorf("introRequest(%q) != %v", s, want)
		}
	}
}

// Progressive speech: a line Claude writes between tool calls is said right
// away (an aside of this turn), and the final reply says only what's new.
func TestPiecesAreSpokenAsTheyCome(t *testing.T) {
	said := make(chan struct{})
	var dev *fakeDevice
	th := &fakeThinker{fn: func(ctx context.Context, n int, text string, onEvent func(Event)) (Result, error) {
		onEvent(Event{Kind: "text", Text: "Your reminder's set."})
		// the piece reaches the device while the turn is still going
		dev.waitFor(t, "the piece", isSay("Your reminder's set."))
		close(said)
		onEvent(Event{Kind: "tool_call", Tool: "look_at_camera", Text: "Using femi look_at_camera"})
		return Result{Reply: "Your reminder's set. You look great in blue! Don't worry, I don't keep the photo.", SessionID: "s"}, nil
	}}
	b, d := newBrain(t, th, nil)
	dev = d
	b.DeviceFrame(textEvent("set a reminder and take my photo"))
	<-said
	dev.waitFor(t, "the end", func(r rec) bool { return r.Kind == "say" && !r.Aside })
	var says []rec
	for _, r := range dev.all() {
		if r.Kind == "say" {
			says = append(says, r)
		}
	}
	if len(says) < 2 || says[0].Text != "Your reminder's set." || !says[0].Aside || says[0].Turn != 1 {
		t.Fatalf("first say = %+v", says)
	}
	for _, r := range says[1:] {
		if strings.Contains(r.Text, "reminder's set") {
			t.Errorf("the piece was said twice: %q", r.Text)
		}
		if r.Turn != 1 {
			t.Errorf("say %q isn't tagged with its turn", r.Text)
		}
	}
	if last := says[len(says)-1]; !last.Listen || last.Aside {
		t.Errorf("the last sentence should end the turn and listen: %+v", last)
	}
}

func TestSentences(t *testing.T) {
	got := sentences("Hi! It's sunny and 28 degrees in Bengaluru today. The suit is $189.99 at Melrose. Want me to order it?")
	want := []string{"Hi! It's sunny and 28 degrees in Bengaluru today.", "The suit is $189.99 at Melrose. Want me to order it?"}
	if !slices.Equal(got, want) {
		t.Errorf("sentences = %q", got)
	}
	if got := sentences("One line"); !slices.Equal(got, []string{"One line"}) {
		t.Errorf("sentences(one) = %q", got)
	}
}

// waitIdle waits until the brain has finished the turn in progress (its last
// SAY can reach the device a moment before the turn lets go of its slot).
func waitIdle(t *testing.T, b *Brain) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for b.Status().Turn {
		if time.Now().After(deadline) {
			t.Fatal("the brain stayed busy")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// The mic can catch Famyy's own voice; those sentences are dropped and what
// the person said is kept.
func TestEchoOfFamyyIsDropped(t *testing.T) {
	b, dev := newBrain(t, replyThinker("The outfit results are the same ones I just shared, and they're still on the screen."), func(c *Config) {
		c.Ears = fakeEars{text: "Any update on the call with Karan? The outfit results are the same ones I just shared and they're still on the screen.", got: make(chan int, 10)}
	})
	b.DeviceFrame(textEvent("show the outfits again"))
	dev.waitFor(t, "first reply", func(r rec) bool { return r.Kind == "say" && !r.Aside })
	waitIdle(t, b)
	pcm := make([]byte, minAudioBytes*2)
	b.DeviceFrame(link.Frame{Type: link.TypeAudioStart, Payload: []byte(`{"rate":16000,"trigger":"followup"}`)})
	b.DeviceFrame(link.Frame{Type: link.TypeAudioChunk, Payload: pcm})
	b.DeviceFrame(link.Frame{Type: link.TypeAudioEnd, Payload: []byte(`{"reason":"silence"}`)})
	deadline := time.Now().Add(3 * time.Second)
	for len(b.cfg.Thinker.(*fakeThinker).Calls()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	calls := b.cfg.Thinker.(*fakeThinker).Calls()
	if len(calls) != 2 || !strings.HasSuffix(calls[1].Text, "] Any update on the call with Karan?") {
		t.Errorf("the echo should be gone and the question kept: %+v", calls)
	}
	if got := b.dropEcho("Tell me a joke about cats."); got != "Tell me a joke about cats." {
		t.Errorf("a fresh request was changed: %q", got)
	}
}
