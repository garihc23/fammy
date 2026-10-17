package femi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"femi/brain/link"
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
func (d *fakeDevice) Say(ctx context.Context, id, text string, pcm []byte) error {
	return d.add(rec{Kind: "say", ID: id, Text: text, Bytes: len(pcm)})
}
func (d *fakeDevice) Command(v any) error {
	raw, _ := json.Marshal(v)
	var m map[string]any
	json.Unmarshal(raw, &m)
	return d.add(rec{Kind: "command", Command: m})
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
		"thinking|what's the weather in M…",
		"working|Asking Fo…",
		"working|Comparing sources and p…",
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

	// Over 15 s is capped.
	audio(b, 20*32000, "max")
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
	dev.waitFor(t, "medicine spoken", isSay("Asha, it's time for your Metformin 500 mg. Tap me when you've taken it."))
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
	if !strings.Contains(list, med.ID+": Asha, Metformin 500 mg — went off at 09:30, waiting for a tap") {
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
	if len(ToolNames()) != 9 {
		t.Fatalf("ToolNames = %v", ToolNames())
	}
}
