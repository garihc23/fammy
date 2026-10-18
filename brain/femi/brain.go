// Package femi is the brain's pipeline: device frames in, speech-to-text, a
// Claude turn (with Fo and Femi's own device tools), text-to-speech out,
// plus approvals, photo slideshows and reminders.
package femi

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"femi/brain/link"
	"femi/brain/remind"
)

// SampleRate of all audio, both directions.
const SampleRate = 16000

const (
	maxAudioBytes = 32 * SampleRate * 2     // 32 s of pcm_s16le mono: the device sends at most 30 s
	minAudioBytes = SampleRate * 2 * 3 / 10 // 0.3 s
	lineMax       = 90                      // STATE line limit (shown in a speech bubble)
	heardMax      = 300                     // longest transcript sent to the device in a "heard" command
)

// Spoken fixed phrases.
const (
	MsgBusy       = "One moment, I'm still on the last thing."
	MsgDidntCatch = "Sorry, I didn't catch that."
	MsgNoted      = "Thank you, noted."
	MsgError      = "Sorry, something went wrong. Please try again."
	MsgTimeout    = "Sorry, that took too long. Please try again."
	MsgNoEars     = "Sorry, I can't hear right now. You can type to me instead."
)

// Device is what the brain needs from the link (link.Server implements it).
type Device interface {
	Connected() bool
	State(face, line string) error
	Say(ctx context.Context, id, text string, pcm []byte, opts link.SayOptions) error
	Command(v any) error
	Image(caption string, w, h int, rgb565 []byte) error
	Snapshot(caption string, w, h int, rgb565 []byte) error // a camera photo, shown as a small polaroid
}

// Camera takes one photo, as JPEG, kept in memory only (camera.Webcam implements it).
type Camera interface {
	Snap(ctx context.Context) ([]byte, error)
}

// Event is streamed by a Thinker while a turn runs (mirrors agent.Event).
type Event struct {
	Kind   string // "tool_call" | "fo_progress" | "fo_done" | "text"
	Tool   string // short tool name, e.g. "ask_fo", "set_reminder"
	Text   string
	Status string

	FoConversation string // fo_progress/fo_done: which Fo errand
}

// FoWatcher waits, outside the conversation, for a Fo errand to settle and
// returns Fo's final status ("complete", "failed", or Fo waiting on the
// person) and response.
type FoWatcher interface {
	WaitForFo(ctx context.Context, conversationID string) (status, text string, err error)
	// CheckFo asks once for the errand's latest response (Fo adds to it, e.g. a call's outcome after dialling).
	CheckFo(ctx context.Context, conversationID string) (text string, err error)
}

// Result of one Claude turn.
type Result struct {
	Reply     string
	SessionID string
	CostUSD   float64
}

// Thinker runs one Claude turn; sessionID "" starts a new conversation.
type Thinker interface {
	Think(ctx context.Context, sessionID, text string, onEvent func(Event)) (Result, error)
}

// VoiceAs is a Voice that can also speak in a chosen voice (call replays).
type VoiceAs interface {
	SpeakAs(ctx context.Context, voiceID, text string, sampleRate int) ([]byte, error)
}

// Ears is speech-to-text (cartesia.Client implements it).
type Ears interface {
	Transcribe(ctx context.Context, pcm []byte, sampleRate int) (string, error)
}

// Voice is text-to-speech returning raw pcm_s16le (cartesia.Client implements it).
type Voice interface {
	Speak(ctx context.Context, text string, sampleRate int) ([]byte, error)
}

// Config wires the brain. Device, Thinker and Reminders are required.
type Config struct {
	Device    Device
	Thinker   Thinker
	Escalator Thinker // runs reminder escalations; default Thinker
	Ears      Ears    // nil: voice input is answered with MsgNoEars
	Voice     Voice   // nil: replies are sent as captions only (SAY with 0 bytes)
	Reminders *remind.Scheduler
	Profile   *Profile
	FamilyDir string
	FoWatcher FoWatcher                        // optional: Fo errands still working when a turn ends carry on in the background
	Camera    Camera                           // optional: look_at_camera
	FoVoice   string                           // replay_call: voice for Fo's side of a call
	CallVoice string                           // replay_call: voice for the person Fo called
	RecordDir string                           // optional: the newest requests are saved here as WAV files
	Logf      func(format string, args ...any) // default log.Printf

	TurnTimeout     time.Duration // default 5 min
	IdleReset       time.Duration // new conversation after this much quiet; default 30 min
	ApprovalTimeout time.Duration // default 62 s (device gives up at 60 s)
	SlideInterval   time.Duration // default 6 s
	Now             func() time.Time
}

// Brain implements link.Handler and the Femi MCP tools.
type Brain struct {
	cfg  Config
	turn chan struct{} // one Claude turn at a time
	out  chan func()   // ordered, non-blocking STATE updates from event callbacks

	mu         sync.Mutex
	recording  bool
	audio      []byte
	overflowed bool
	session    string
	lastTurn   time.Time
	turnN      int
	sayN       int
	approvalN  int
	approvals  map[string]chan string
	slideStop  context.CancelFunc
	phrases    map[string][]byte
	trigger    string             // what started the recording: "wake", "tap" or "followup"
	stopActive context.CancelFunc // stops the request in progress (a tap on the device)
	activeCtx  context.Context    // that request's context, for audio its tools start
	lastPhoto  time.Time          // when look_at_camera last took a photo
	errandN    int
	errands    map[string]*errand // Fo errands running in the background, by id
	speechQ    chan *speech       // everything Famyy says, in order (speech.go)
	said       []saidLine         // what Famyy said lately, to tell its own voice in a recording
}

type saidLine struct {
	at    time.Time
	grams []string
}

// New returns a brain; call it before the link starts serving.
func New(cfg Config) *Brain {
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	if cfg.Escalator == nil {
		cfg.Escalator = cfg.Thinker
	}
	if cfg.TurnTimeout <= 0 {
		cfg.TurnTimeout = 5 * time.Minute
	}
	if cfg.IdleReset <= 0 {
		cfg.IdleReset = 5 * time.Minute // a family shares Femi: after a pause, the next speaker is likely someone else
	}
	if cfg.ApprovalTimeout <= 0 {
		cfg.ApprovalTimeout = 62 * time.Second
	}
	if cfg.SlideInterval <= 0 {
		cfg.SlideInterval = 6 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	b := &Brain{
		cfg:       cfg,
		turn:      make(chan struct{}, 1),
		out:       make(chan func(), 64),
		approvals: map[string]chan string{},
		phrases:   map[string][]byte{},
		errands:   map[string]*errand{},
		speechQ:   make(chan *speech, 64),
	}
	go b.speaker()
	go func() {
		for f := range b.out {
			f()
		}
	}()
	if cfg.Voice != nil {
		go func() { // the introduction plays the moment it's asked for
			for _, line := range Intro {
				b.speakCache(line)
			}
		}()
	}
	if cfg.Reminders != nil {
		cfg.Reminders.OnFire = b.reminderFired
		cfg.Reminders.OnEscalate = b.reminderUnacked
	}
	return b
}

func (b *Brain) logf(format string, args ...any) { b.cfg.Logf(format, args...) }

// ---- link.Handler ----

// DeviceConnected implements link.Handler.
func (b *Brain) DeviceConnected(remote string) {
	b.mu.Lock()
	b.recording, b.audio = false, nil
	b.mu.Unlock()
}

// DeviceDisconnected implements link.Handler: drops a half-heard utterance
// and answers pending approvals (the approve screen is gone).
func (b *Brain) DeviceDisconnected() {
	b.mu.Lock()
	b.recording, b.audio = false, nil
	for id, ch := range b.approvals {
		select {
		case ch <- "device disconnected (treat as declined)":
		default:
		}
		delete(b.approvals, id)
	}
	if b.slideStop != nil {
		b.slideStop()
		b.slideStop = nil
	}
	b.mu.Unlock()
}

type deviceEvent struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	OK   bool   `json:"ok"`
	Text string `json:"text"`
}

// DeviceFrame implements link.Handler. It never blocks: slow work and all
// writes back to the device happen on goroutines.
func (b *Brain) DeviceFrame(f link.Frame) {
	switch f.Type {
	case link.TypeHello:
		b.logf("device: HELLO %s", f.Payload)
		go b.hello()
	case link.TypeAudioStart:
		var p struct {
			Trigger string `json:"trigger"`
		}
		_ = json.Unmarshal(f.Payload, &p)
		b.mu.Lock()
		b.recording, b.audio, b.overflowed = true, make([]byte, 0, 64<<10), false
		b.trigger = p.Trigger
		b.mu.Unlock()
		b.logf("device: AUDIO_START %s", f.Payload)
	case link.TypeAudioChunk:
		b.mu.Lock()
		if b.recording {
			room := maxAudioBytes - len(b.audio)
			if len(f.Payload) > room {
				if !b.overflowed {
					b.overflowed = true
					b.logf("device: utterance longer than %d s, keeping the first %d s", maxAudioBytes/(SampleRate*2), maxAudioBytes/(SampleRate*2))
				}
				b.audio = append(b.audio, f.Payload[:max(room, 0)]...)
			} else {
				b.audio = append(b.audio, f.Payload...)
			}
		}
		b.mu.Unlock()
	case link.TypeAudioEnd:
		var p struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(f.Payload, &p)
		b.mu.Lock()
		was, pcm, trigger := b.recording, b.audio, b.trigger
		b.recording, b.audio = false, nil
		b.mu.Unlock()
		if !was {
			b.logf("device: AUDIO_END without AUDIO_START, ignored")
			return
		}
		q, mid, loud := loudness(pcm)
		b.logf("device: AUDIO_END reason=%s, %.1f s of audio, loudness quiet %d / middle %d / loud %d",
			p.Reason, float64(len(pcm))/(2*SampleRate), q, mid, loud)
		if dir := b.cfg.RecordDir; dir != "" {
			go func() {
				if err := saveRecording(dir, pcm, p.Reason); err != nil {
					b.logf("record: %v", err)
				}
			}()
		}
		if p.Reason == "cancel" {
			go b.cfg.Device.State("idle", "")
			return
		}
		go b.utterance(pcm, trigger)
	case link.TypeEvent:
		var e deviceEvent
		if err := json.Unmarshal(f.Payload, &e); err != nil {
			b.logf("device: bad EVENT %q: %v", f.Payload, err)
			return
		}
		switch e.Type {
		case "approval":
			b.resolveApproval(e.ID, e.OK)
		case "ack":
			go b.ack(e.ID)
		case "text":
			b.logf("device: text %q", e.Text)
			go b.textRequest(e.Text)
		case "played":
			b.logf("device: played %s", e.ID)
		case "stop":
			b.stopRequest()
		default:
			b.logf("device: unknown event %s", f.Payload)
		}
	default:
		b.logf("device: unexpected frame %s (%d bytes)", link.TypeName(f.Type), len(f.Payload))
	}
}

func (b *Brain) hello() {
	_ = b.cfg.Device.State("idle", "Brain connected")
	b.mu.Lock()
	var out []*errand
	for _, e := range b.errands {
		out = append(out, e)
	}
	b.mu.Unlock()
	for _, e := range out { // the device may have rebooted: Fo is still out with these
		_ = b.cfg.Device.Command(foCommand{Type: "fo", State: "away", Task: e.task, ID: e.id})
	}
	if b.cfg.Reminders == nil {
		return
	}
	// The device may have rebooted: show reminders still waiting for a tap.
	for _, r := range b.cfg.Reminders.Pending() {
		_ = b.cfg.Device.Command(reminderCommand(r))
	}
}

// ---- turns ----

// Status says whether anything is in flight: a recording arriving, a turn
// (Claude thinking, Fo on an errand, the reply being spoken). Restart the
// brain only when Busy is false, or that work is lost.
type Status struct {
	Busy      bool      `json:"busy"`
	Recording bool      `json:"recording"`
	Turn      bool      `json:"turn"`
	Errands   int       `json:"errands"` // Fo errands still out (a restart would lose their reports)
	LastTurn  time.Time `json:"last_turn"`
}

func (b *Brain) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	turn := len(b.turn) > 0
	return Status{Busy: turn || b.recording || len(b.errands) > 0, Recording: b.recording, Turn: turn,
		Errands: len(b.errands), LastTurn: b.lastTurn}
}

func (b *Brain) tryTurn() bool {
	select {
	case b.turn <- struct{}{}:
		return true
	default:
		return false
	}
}

func (b *Brain) endTurn() { <-b.turn }

// turnWait is how long a request waits for the turn in progress (an errand's
// report, say) before Famyy answers "one moment" instead.
var turnWait = 15 * time.Second

// awaitTurn takes the turn slot, waiting up to turnWait.
func (b *Brain) awaitTurn() bool {
	if b.tryTurn() {
		return true
	}
	t := time.NewTimer(turnWait)
	defer t.Stop()
	select {
	case b.turn <- struct{}{}:
		return true
	case <-t.C:
		return false
	}
}

// reportTurn takes the turn slot for a report Famyy starts itself, never
// while the person is speaking: their request goes first.
func (b *Brain) reportTurn() {
	for {
		b.mu.Lock()
		rec := b.recording
		b.mu.Unlock()
		if !rec && b.tryTurn() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// activity starts a request the device can stop with a tap (stopRequest).
func (b *Brain) activity() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.stopActive, b.activeCtx = cancel, ctx
	b.mu.Unlock()
	return ctx, func() {
		b.mu.Lock()
		b.stopActive, b.activeCtx = nil, nil
		b.mu.Unlock()
		cancel()
	}
}

// requestCtx is the context of the request in progress (a tap stops it), or
// Background outside of one.
func (b *Brain) requestCtx() context.Context {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.activeCtx != nil {
		return b.activeCtx
	}
	return context.Background()
}

// stopRequest drops the request in progress: its Claude turn and whatever it
// was about to say. Fo errands already handed over keep going.
func (b *Brain) stopRequest() {
	b.mu.Lock()
	stop := b.stopActive
	b.mu.Unlock()
	if stop == nil {
		b.logf("device: stop (nothing in progress)")
		return
	}
	b.logf("device: stop: dropping the request in progress")
	stop()
}

func stopped(ctx context.Context) bool { return errors.Is(ctx.Err(), context.Canceled) }

func (b *Brain) utterance(pcm []byte, trigger string) {
	if !b.awaitTurn() {
		b.logf("busy: dropping utterance")
		b.speakAside(context.Background(), MsgBusy)
		return
	}
	defer b.endTurn()
	ctx, done := b.activity() // only now: a dropped utterance must not take over the running turn's stop
	defer done()
	// after a reply the device listens on by itself; if nobody spoke up, go quiet instead of "didn't catch that"
	quietMiss := trigger == "followup"
	didntCatch := func() {
		if quietMiss {
			_ = b.cfg.Device.State("idle", "")
			return
		}
		b.speak(ctx, MsgDidntCatch, true, link.SayOptions{Listen: true}) // listen again: they wanted something
	}
	if b.cfg.Ears == nil {
		b.speakPhrase(ctx, MsgNoEars)
		return
	}
	if len(pcm) < minAudioBytes {
		b.logf("utterance too short (%d bytes)", len(pcm))
		didntCatch()
		return
	}
	_ = b.cfg.Device.State("thinking", "Listening…")
	start := time.Now()
	text, err := b.cfg.Ears.Transcribe(ctx, pcm, SampleRate)
	stt := time.Since(start)
	if stopped(ctx) {
		b.logf("stt: stopped")
		return
	}
	if err != nil {
		b.logf("stt failed after %dms: %v", stt.Milliseconds(), err)
		_ = b.cfg.Device.State("sad", "Couldn't hear that")
		didntCatch()
		return
	}
	if t := b.dropEcho(text); t != text {
		b.logf("stt %dms: dropped what was Famyy's own voice from %q", stt.Milliseconds(), clip(text, 160))
		text = t
	}
	if t := dropLoops(text); t != text {
		b.logf("stt %dms: dropped a repeated phrase (transcriber looping on noise) from %q", stt.Milliseconds(), clip(text, 160))
		text = t
	}
	if tooShort(text) {
		b.logf("stt %dms: %q is too short", stt.Milliseconds(), text)
		didntCatch()
		return
	}
	b.runTurn(ctx, "voice", text, stt)
}

func (b *Brain) textRequest(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if !b.awaitTurn() {
		b.logf("busy: dropping text %q", text)
		b.speakAside(context.Background(), MsgBusy)
		return
	}
	defer b.endTurn()
	ctx, done := b.activity()
	defer done()
	b.runTurn(ctx, "text", text, 0)
}

// tooShort: fewer than two letters or digits.
// soundTags are the transcriber's notes for sounds that aren't words: "*ding*",
// "[music]", "(coughs)". Whisper writes "foreign" for speech it can't read.
var soundTags = regexp.MustCompile(`\*[^*]*\*|\[[^\]]*\]|\([^)]*\)|` +
	`(?i)^\s*(?:foreign|you|thanks for watching|thank you for watching)[.!]?\s*$`) // what Whisper writes for near-silence

func tooShort(s string) bool {
	s = soundTags.ReplaceAllString(s, "")
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n++
		}
	}
	return n < 2
}

// runTurn runs one Claude turn and speaks the reply. The caller holds the
// turn slot.
func (b *Brain) runTurn(ctx context.Context, source, text string, stt time.Duration) {
	b.turnWith(ctx, source, text, stt, nil)
}

// turnWith runs a turn; beforeSpeak (may be nil) runs once Claude is done,
// just before anything is said.
func (b *Brain) turnWith(ctx context.Context, source, text string, stt time.Duration, beforeSpeak func()) {
	b.mu.Lock()
	b.turnN++
	n := b.turnN
	now := b.cfg.Now()
	if b.session != "" && now.Sub(b.lastTurn) > b.cfg.IdleReset && source != "fo" { // an errand's report belongs to its conversation
		b.logf("turn %d: idle for %s, starting a fresh conversation", n, now.Sub(b.lastTurn).Round(time.Minute))
		b.session = ""
	}
	if b.session != "" && startOver.MatchString(text) {
		b.logf("turn %d: asked to start over, starting a fresh conversation", n)
		b.session = ""
	}
	session := b.session
	b.mu.Unlock()

	b.logf("turn %d [%s] %q (stt %dms, session %q)", n, source, text, stt.Milliseconds(), session)
	if source != "fo" && introRequest.MatchString(text) {
		b.logf("turn %d: introducing myself (scripted)", n)
		b.introduce(ctx)
		b.mu.Lock()
		b.lastTurn = b.cfg.Now()
		b.mu.Unlock()
		return
	}
	// the full transcript first, then the STATE: same connection, so the device sees them in this order
	_ = b.cfg.Device.Command(heardCommand{Type: "heard", Text: clip(text, heardMax)})
	_ = b.cfg.Device.State("thinking", clip(text, lineMax)) // the device prints its own "> " prompt

	tctx, cancel := context.WithTimeout(ctx, b.cfg.TurnTimeout)
	defer cancel()
	var events []string
	lastLine := ""
	lastAsk := ""
	// what Claude says between tool calls ("Your reminder's set.") is spoken right away
	var pieceMu sync.Mutex
	var pieces []string
	saidSoFar := func() []string {
		pieceMu.Lock()
		defer pieceMu.Unlock()
		return append([]string(nil), pieces...)
	}
	open := map[string]*errand{} // Fo errands still working, by Fo conversation
	onEvent := func(e Event) {
		switch {
		case e.Kind == "tool_call" && e.Tool == "ask_fo":
			lastAsk = strings.TrimPrefix(e.Text, "Asking Fo: ")
		case e.Kind == "fo_progress" && e.Status == "working" && e.FoConversation != "":
			if open[e.FoConversation] == nil {
				open[e.FoConversation] = &errand{conv: e.FoConversation, asked: lastAsk, task: foTask(lastAsk)}
			}
		case e.Kind == "fo_done" && e.FoConversation != "":
			delete(open, e.FoConversation)
		case e.Kind == "text":
			if piece := Spoken(e.Text); !tooShort(piece) && !quietReply(piece) {
				pieceMu.Lock()
				pieces = append(pieces, piece)
				pieceMu.Unlock()
				b.enqueue(ctx, piece, false, link.SayOptions{Aside: true, Turn: n})
			}
		}
		b.logf("turn %d: %s %s %q", n, e.Kind, e.Tool, clip(e.Text, 160))
		events = append(events, e.Kind+":"+e.Tool)
		line := ""
		face := "working"
		switch e.Kind {
		case "fo_progress":
			line = foLine(e.Text)
		case "tool_call":
			if e.Tool == "ask_fo" {
				line = "Asking Fo…"
				b.commandAsync(foCommand{Type: "fo", State: "start", Task: foTask(e.Text)}) // Fo walks onto the screen
			}
		case "fo_done":
			face, line = "thinking", "Fo is done"
			b.commandAsync(foCommand{Type: "fo", State: "done", OK: e.Status != "failed"}) // hands over the result and leaves
		}
		if line != "" && line != lastLine {
			lastLine = line
			b.stateAsync(face, line)
		}
	}
	start := time.Now()
	stamped := fmt.Sprintf("[%s%s%s] %s", now.Format("Mon 2 Jan 2006, 15:04"), b.waitingNote(), b.photoNote(now), text)
	res, err := b.cfg.Thinker.Think(tctx, session, stamped, onEvent)
	took := time.Since(start)
	b.background(open) // Fo keeps going with whatever it was still working on; Famyy reports when it's done
	b.flushStates()
	if beforeSpeak != nil {
		beforeSpeak()
	}

	if err != nil {
		b.mu.Lock()
		if res.SessionID != "" { // a stop or a failure keeps the conversation; only idle time or "start over" ends it
			b.session = res.SessionID
		}
		b.lastTurn = b.cfg.Now()
		b.mu.Unlock()
		if stopped(ctx) {
			b.logf("turn %d stopped after %dms (events %s)", n, took.Milliseconds(), strings.Join(events, ","))
			return
		}
		b.logf("turn %d FAILED after %dms: %v (events %s)", n, took.Milliseconds(), err, strings.Join(events, ","))
		_ = b.cfg.Device.State("sad", "Something went wrong")
		msg := MsgError
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(tctx.Err(), context.DeadlineExceeded) {
			msg = MsgTimeout
		}
		b.speakPhrase(ctx, msg)
		return
	}
	b.mu.Lock()
	if res.SessionID != "" {
		b.session = res.SessionID
	}
	b.lastTurn = b.cfg.Now()
	b.mu.Unlock()

	reply := Spoken(res.Reply)
	if quietReply(reply) { // the device already said it ("Okay, I won't."): end the turn without a word
		b.logf("turn %d done: quiet | turn %dms, $%.4f | events [%s]", n, took.Milliseconds(), res.CostUSD, strings.Join(events, ","))
		<-b.enqueueAudio(ctx, "", nil, link.SayOptions{Turn: n}).done // an empty reply still ends the device's busy state
		return
	}
	said := saidSoFar()
	if reply == "" && len(said) == 0 {
		reply = "Done."
	}
	listen := keepListening(text, reply)
	reply = withoutSpoken(reply, said) // only what wasn't said mid-turn
	tts := b.speakReply(ctx, reply, link.SayOptions{Listen: listen, Turn: n})
	if stopped(ctx) {
		b.logf("turn %d stopped while speaking", n)
	}
	b.logf("turn %d done: reply %q | stt %dms, turn %dms, tts %dms, $%.4f | events [%s]",
		n, reply, stt.Milliseconds(), took.Milliseconds(), tts.Milliseconds(), res.CostUSD, strings.Join(events, ","))
}

// foCommand tells the device about Fo's visit: "start" (with the kind of job,
// for the icon Fo works under) and "done".
// heardCommand shows what Famyy understood. "type" stays first and "text" last:
// the device reads control frames from a small buffer and keys on that order.
type heardCommand struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type foCommand struct {
	Type  string `json:"type"`
	State string `json:"state"` // start | done (within a turn) | away | back (an errand in the background)
	Task  string `json:"task,omitempty"`
	OK    bool   `json:"ok"`
	ID    string `json:"id,omitempty"` // away/back: which errand
}

// foTasks maps words in a request to Fo onto the icon for that kind of job;
// the first match wins, anything else is a web look-up.
var foTasks = []struct {
	task string
	re   *regexp.Regexp
}{
	{"call", regexp.MustCompile(`(?i)\b(call|phone|ring|dial)\b`)},
	{"message", regexp.MustCompile(`(?i)\b(text|sms|whatsapp|message)\b`)},
	{"email", regexp.MustCompile(`(?i)\b(e-?mail|mail|inbox)\b`)},
	{"shop", regexp.MustCompile(`(?i)\b(order|buy|purchase|shop|cart|deliver\w*)\b`)},
	{"calendar", regexp.MustCompile(`(?i)\b(calendar|book|booking|appointment|schedule|meeting|reserv\w*)\b`)},
}

func foTask(request string) string {
	for _, t := range foTasks {
		if t.re.MatchString(request) {
			return t.task
		}
	}
	return "web"
}

// commandAsync queues a COMMAND in order with STATE updates, without blocking the caller.
func (b *Brain) commandAsync(v any) {
	select {
	case b.out <- func() { _ = b.cfg.Device.Command(v) }:
	default:
		b.logf("state queue full, dropping command %+v", v)
	}
}

// stateAsync queues a STATE update without blocking the caller.
func (b *Brain) stateAsync(face, line string) {
	select {
	case b.out <- func() { _ = b.cfg.Device.State(face, line) }:
	default:
		b.logf("state queue full, dropping %s %q", face, line)
	}
}

// flushStates waits (briefly) until queued STATE updates were written, so
// they cannot land after what the caller sends next.
func (b *Brain) flushStates() {
	done := make(chan struct{})
	select {
	case b.out <- func() { close(done) }:
	case <-time.After(time.Second):
		return
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

var progressPrefix = regexp.MustCompile(`^(?i)working for [0-9hms ]+:\s*`)

// foLine turns Fo's progress text into a ≤24-char STATE line.
func foLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if t := progressPrefix.ReplaceAllString(s, ""); t != "" {
		s = t
	}
	s = strings.TrimPrefix(s, "Still ")
	if s == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(s)
	s = string(unicode.ToUpper(r)) + s[size:]
	return clip(s, lineMax)
}

// clip shortens s to at most n runes, ending in "…" when cut.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimRight(string(r[:n-1]), " ") + "…"
}

var (
	urlRe      = regexp.MustCompile(`\(?https?://\S+\)?`)
	markdownRe = regexp.MustCompile("[*_`#>]+")
	bulletRe   = regexp.MustCompile(`(?m)^\s*(?:[-•]|\d+[.)])\s+`)
	spaceRe    = regexp.MustCompile(`\s+([,.!?;:])`)
)

// Spoken cleans a reply for TTS and the caption: no markdown, list
// markers, URLs or line breaks.
func Spoken(s string) string {
	s = urlRe.ReplaceAllString(s, "")
	s = bulletRe.ReplaceAllString(s, "")
	s = markdownRe.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), " ")
	s = spaceRe.ReplaceAllString(s, "$1")
	s = strings.TrimSpace(strings.TrimRight(s, ":;, "))
	return s
}

// ---- speaking ----

// speakPhrase speaks a fixed phrase, synthesizing it only once.
func (b *Brain) speakPhrase(ctx context.Context, text string) {
	b.speak(ctx, text, true, link.SayOptions{})
}

// speakAside says a canned phrase that isn't the answer to the request in
// progress ("One moment...", "Thank you, noted."): the device keeps that going.
func (b *Brain) speakAside(ctx context.Context, text string) {
	b.speak(ctx, text, true, link.SayOptions{Aside: true})
}

// speak sends text as a SAY (with audio when TTS is configured) and returns
// the TTS time.
// speakCache synthesises a phrase ahead of time into the phrase cache.
func (b *Brain) speakCache(text string) {
	b.mu.Lock()
	_, ok := b.phrases[text]
	b.mu.Unlock()
	if ok {
		return
	}
	pcm, err := b.cfg.Voice.Speak(context.Background(), spoken(text), SampleRate)
	if err != nil {
		b.logf("tts: couldn't prepare %q: %v", clip(text, 40), err)
		return
	}
	b.mu.Lock()
	b.phrases[text] = pcm
	b.mu.Unlock()
}

// speak says text through the speech queue and returns once it has been
// handed to the device, with its synthesis time.
func (b *Brain) speak(ctx context.Context, text string, cache bool, opts link.SayOptions) time.Duration {
	s := b.enqueue(ctx, text, cache, opts)
	<-s.done
	return s.tts
}

var (
	sentence = regexp.MustCompile(`[^.!?]+[.!?]*`)
	nonWord  = regexp.MustCompile(`[^\p{L}\p{N}']+`)
)

func words(s string) []string {
	return strings.Fields(strings.ToLower(nonWord.ReplaceAllString(s, " ")))
}

// dropLoops removes a short sentence the transcriber wrote three or more times
// ("He will kill you. He will kill you. ..."): Whisper does that on noise and
// background talk. What the person really said around it is kept; a
// transcript that is nothing but the loop becomes "".
func dropLoops(s string) string {
	parts := sentence.FindAllString(s, -1)
	keys := make([]string, len(parts))
	count := map[string]int{}
	for i, p := range parts {
		if w := words(p); len(w) > 0 && len(w) <= 10 {
			keys[i] = strings.Join(w, " ")
			count[keys[i]]++
		}
	}
	var kept []string
	looped := false
	for i, p := range parts {
		if keys[i] != "" && count[keys[i]] >= 3 {
			looped = true
			continue
		}
		kept = append(kept, strings.TrimSpace(p))
	}
	if looped {
		return strings.Join(kept, " ")
	}
	// the same loop without punctuation: "thank you thank you thank you thank you"
	w := words(s)
	for unit := 2; unit <= 8 && unit*3 <= len(w) && len(w) >= 8; unit++ {
		periodic := true
		for i := unit; i < len(w) && periodic; i++ {
			periodic = w[i] == w[i%unit]
		}
		if periodic {
			return ""
		}
	}
	return s
}

// startOver: the person wants a clean slate ("start over", "new conversation", "forget all that").
var startOver = regexp.MustCompile(`(?i)\b(?:start (?:over|again|afresh|from scratch)|new conversation|fresh start|clean slate|` +
	`forget (?:that|this|everything|all (?:of )?that|what i said))\b`)

// captionMax caps the text sent with a reply (the device shows it word by
// word); the audio always has all of it.
const captionMax = 400

func caption(s string) string {
	if utf8.RuneCountInString(s) <= captionMax {
		return s
	}
	r := []rune(s)[:captionMax]
	if i := strings.LastIndexByte(string(r), ' '); i > captionMax/2 {
		return strings.TrimRight(string(r)[:i], " ,;:") + "…"
	}
	return string(r) + "…"
}

// Intro is what Famyy says, word for word, when asked to introduce itself
// (the demo for the jury): one sentence per SAY so the bubbles keep up, and
// synthesised at start so it plays at once.
var Intro = []string{
	"Hi jury! I'm Famyy, your AI family member, powered by Fo and Claude.",
	"I'm here for everyone in the house.",
	"For the little ones, I tell poems, play games and help with homework.",
	"For busy parents, I take care of everyday tasks, like booking clothes and ordering food.",
	"And for grandparents, I remind them about their medicines, tell jokes, and keep them company when the house feels quiet.",
	"I was made by Karan and Chirag. Nice to meet you!",
}

var introRequest = regexp.MustCompile(`(?i)\bintroduce\s+(?:your\s*self|famyy|fammy|famy|femi)\b`)

// introduce says Intro: every sentence but the last is an aside, and Famyy
// listens after the last one. A tap stops it.
func (b *Brain) introduce(ctx context.Context) {
	var last *speech
	for i, line := range Intro {
		end := i == len(Intro)-1
		last = b.enqueue(ctx, line, true, link.SayOptions{Aside: !end, Listen: end})
	}
	<-last.done
}

// echoWindow is how long what Famyy said counts as something the mic may pick up again.
const echoWindow = 2 * time.Minute

// trigrams are a text's word triples, the unit for spotting Famyy's own voice.
func trigrams(s string) []string {
	w := words(s)
	var out []string
	for i := 0; i+3 <= len(w); i++ {
		out = append(out, strings.Join(w[i:i+3], " "))
	}
	return out
}

// noteSaid remembers what Famyy just said.
func (b *Brain) noteSaid(text string) {
	g := trigrams(text)
	if len(g) == 0 {
		return
	}
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	keep := b.said[:0]
	for _, l := range b.said {
		if now.Sub(l.at) < echoWindow {
			keep = append(keep, l)
		}
	}
	b.said = append(keep, saidLine{now, g})
}

// dropEcho removes the sentences of a transcript that are mostly Famyy's own
// recent words (the mic heard the speaker) and keeps what the person said.
func (b *Brain) dropEcho(text string) string {
	recent := map[string]bool{}
	b.mu.Lock()
	for _, l := range b.said {
		if time.Since(l.at) < echoWindow {
			for _, g := range l.grams {
				recent[g] = true
			}
		}
	}
	b.mu.Unlock()
	if len(recent) == 0 {
		return text
	}
	var kept []string
	echo := false
	for _, s := range sentence.FindAllString(text, -1) {
		g := trigrams(s)
		hits := 0
		for _, x := range g {
			if recent[x] {
				hits++
			}
		}
		if len(g) > 0 && hits*2 >= len(g) {
			echo = true
			continue
		}
		kept = append(kept, strings.TrimSpace(s))
	}
	if !echo {
		return text
	}
	return strings.Join(kept, " ")
}

// quietReply is Claude's way to say nothing at all: "[quiet]".
func quietReply(s string) bool {
	return strings.EqualFold(strings.Trim(strings.TrimSpace(s), "[]. "), "quiet")
}

func (b *Brain) nextSayID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sayN++
	return fmt.Sprintf("s%d", b.sayN)
}

// closing is a whole request that just ends the conversation: "thanks", "that's all", "bye Famyy", "no".
var closing = func() *regexp.Regexp {
	done := `(?:thanks?(?: you)?(?: so much| a lot)?|bye(?: bye)?|goodbye|good ?night|that'?s (?:all|it|fine|enough)|that will be all|` +
		`stop|enough|nothing(?: else)?|no(?: thanks?| thank you)?|nope|never ?mind|cancel|be quiet|quiet|shut up|sleep|go to sleep)`
	filler := `(?:ok(?:ay)?|no|nah|fine|great|cool|alright|all right|perfect|famyy|famy|fammy|faimy|femy|femmy|femi|for now|then|please)`
	return regexp.MustCompile(`(?i)^\W*(?:` + filler + `\W+)*` + done + `(?:\W+(?:` + done + `|` + filler + `))*\W*$`)
}()

// farewell in Famyy's reply also ends the conversation.
var farewell = regexp.MustCompile(`(?i)\b(?:bye|good ?night|take care|talk (?:to you )?(?:soon|later))\b`)

// keepListening: after replying, listen for the next thing without the wake
// phrase, unless the person or Famyy has just ended the conversation.
func keepListening(request, reply string) bool {
	if closing.MatchString(request) || strings.Contains(strings.ToLower(request), "go to sleep") {
		return false
	}
	return !farewell.MatchString(reply)
}

// ---- reminders ----

// Command payloads: "type" first and free text last, because the device
// reads control frames from a fixed-size buffer.
type reminderCmd struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Urgent bool   `json:"urgent"`
	Text   string `json:"text"`
}

type approveCmd struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Text string `json:"text"`
}

func reminderCommand(r remind.Reminder) reminderCmd {
	text := r.Text
	if r.Who != "" {
		text = r.Who + ": " + r.Text
	}
	return reminderCmd{Type: "reminder", ID: r.ID, Urgent: true, Text: clip(text, 120)}
}

func (b *Brain) reminderFired(r remind.Reminder) {
	b.logf("reminder %s fired: %s", r.ID, remind.Describe(r, b.cfg.Now()))
	go func() {
		if err := b.cfg.Device.Command(reminderCommand(r)); err != nil {
			b.logf("reminder %s: not shown: %v", r.ID, err)
			return
		}
		spoken := "Reminder: " + r.Text + "."
		if r.Medication {
			who := r.Who
			if who == "" {
				who = "Hello"
			}
			spoken = fmt.Sprintf("%s, it's time for your %s.", who, r.Text) // the device then says how to acknowledge ("say done, or tap me")
		}
		b.speak(context.Background(), strings.ReplaceAll(spoken, "..", "."), false, link.SayOptions{Aside: true})
	}()
}

func (b *Brain) ack(id string) {
	r, ok := b.cfg.Reminders.Ack(id)
	if !ok {
		b.logf("ack for %s: nothing waiting", id)
		return
	}
	b.logf("reminder %s acknowledged", r.ID)
	_ = b.cfg.Device.State("happy", "Noted")
	b.speakAside(context.Background(), MsgNoted)
}

// photoNote tells Claude how old the last camera photo in the conversation
// is, so it takes a fresh one instead of describing an old one as now.
func (b *Brain) photoNote(now time.Time) string {
	b.mu.Lock()
	at := b.lastPhoto
	b.mu.Unlock()
	if at.IsZero() || now.Sub(at) > time.Hour {
		return ""
	}
	return fmt.Sprintf(" | last camera photo %s (%d min ago)", at.Format("15:04"), int(now.Sub(at).Minutes()))
}

// ---- background Fo errands ----

const errandMax = 30 * time.Minute

type errand struct {
	id, conv, asked, task string
}

// background hands Fo errands that were still working when the turn ended to
// the FoWatcher: Fo goes "away" with each, and comes "back" with a report turn.
func (b *Brain) background(open map[string]*errand) {
	if b.cfg.FoWatcher == nil || len(open) == 0 {
		return
	}
	for _, e := range open {
		b.mu.Lock()
		b.errandN++
		e.id = fmt.Sprintf("f%d", b.errandN)
		b.errands[e.id] = e
		b.mu.Unlock()
		b.logf("errand %s: Fo is still on %q, waiting in the background", e.id, clip(e.asked, 120))
		b.commandAsync(foCommand{Type: "fo", State: "away", Task: e.task, ID: e.id})
		go b.watchErrand(e)
	}
}

func (b *Brain) watchErrand(e *errand) {
	ctx, cancel := context.WithTimeout(context.Background(), errandMax)
	defer cancel()
	status, text, err := b.cfg.FoWatcher.WaitForFo(ctx, e.conv)
	if err != nil {
		status, text = "failed", "Fo didn't report back ("+err.Error()+")"
	}
	b.logf("errand %s settled: %s %q", e.id, status, clip(text, 160))
	if err == nil && status != "failed" && underway.MatchString(text) {
		text = b.followCall(ctx, e, text)
	}

	b.reportTurn() // after whatever is going on now, and not while someone is talking
	defer b.endTurn()
	b.mu.Lock()
	delete(b.errands, e.id)
	b.mu.Unlock()
	ctx, done := b.activity()
	defer done()
	msg := fmt.Sprintf("[Fo has finished an errand from earlier (%s). Status: %s. Fo says: %s] "+
		"Tell the person the outcome in a sentence or two; if Fo needs an answer or an approval, ask for it.",
		clip(e.asked, 200), status, text)
	b.turnWith(ctx, "fo", msg, 0, func() {
		_ = b.cfg.Device.Command(foCommand{Type: "fo", State: "back", Task: e.task, OK: status != "failed", ID: e.id}) // Fo walks in with it
	})
}

// underway: Fo calls an errand complete once it has dialled ("Calling Karan
// now, I'll report back when the call wraps"); the outcome comes later.
var underway = regexp.MustCompile(`(?i)\b(?:calling\b.*\bnow|call (?:is )?(?:going out|underway|in progress|ringing)|ringing|` +
	`i'll (?:report|post|let you know|update you)|once the call (?:ends|wraps|is done)|when the call (?:ends|wraps|is done))`)

var (
	followEvery = 45 * time.Second
	followTries = 12 // ~9 minutes
)

// followCall checks back with Fo until the errand's response grows past what
// it said when it dialled, and returns the whole response (or what there is).
func (b *Brain) followCall(ctx context.Context, e *errand, dialled string) string {
	b.logf("errand %s: Fo is on the call; checking back for how it went", e.id)
	for i := 0; i < followTries; i++ {
		select {
		case <-time.After(followEvery):
		case <-ctx.Done():
			return dialled
		}
		latest, err := b.cfg.FoWatcher.CheckFo(ctx, e.conv)
		if err != nil {
			b.logf("errand %s: check %d: %v", e.id, i+1, err)
			continue
		}
		if news := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(latest), strings.TrimSpace(dialled))); news != "" && latest != dialled {
			b.logf("errand %s: the call's outcome: %q", e.id, clip(news, 160))
			return latest
		}
	}
	b.logf("errand %s: no outcome after %d checks", e.id, followTries)
	return dialled + " (Fo hasn't said yet how the call went.)"
}

// waitingNote tells Claude, inside the time stamp, which reminders went off
// and are still waiting to be confirmed, so "I've taken it" can confirm one.
func (b *Brain) waitingNote() string {
	if b.cfg.Reminders == nil {
		return ""
	}
	var parts []string
	for _, r := range b.cfg.Reminders.Pending() {
		what := r.Text
		if r.Who != "" {
			what = r.Who + ": " + r.Text
		}
		parts = append(parts, fmt.Sprintf("%s %s (went off %s)", r.ID, what, r.FiredAt.Format("15:04")))
	}
	if len(parts) == 0 {
		return ""
	}
	return " | waiting to be confirmed: " + strings.Join(parts, "; ")
}

// AcknowledgeReminder confirms a reminder that went off, the spoken way of
// tapping its card: id "" picks the only one waiting. The device closes the card.
func (b *Brain) AcknowledgeReminder(id string) (string, error) {
	if b.cfg.Reminders == nil {
		return "", errors.New("reminders aren't available")
	}
	if id = strings.TrimSpace(id); id == "" {
		waiting := b.cfg.Reminders.Pending()
		switch len(waiting) {
		case 0:
			return "Nothing is waiting to be confirmed right now.", nil
		case 1:
			id = waiting[0].ID
		default:
			var ids []string
			for _, r := range waiting {
				ids = append(ids, remind.Describe(r, b.cfg.Now()))
			}
			return "", fmt.Errorf("several reminders are waiting; ask which one and pass its id:\n%s", strings.Join(ids, "\n"))
		}
	}
	r, ok := b.cfg.Reminders.Ack(id)
	if !ok {
		return "", fmt.Errorf("reminder %q isn't waiting to be confirmed; call list_reminders to see what is", id)
	}
	b.logf("reminder %s acknowledged by voice", r.ID)
	b.commandAsync(reminderDoneCmd{Type: "reminder_done", ID: r.ID})
	out := "Confirmed: " + r.Text
	if r.Who != "" {
		out += " (" + r.Who + ")"
	}
	if r.Repeat == remind.Daily {
		out += fmt.Sprintf(". It stays scheduled daily at %s", r.Clock)
	}
	return out + ".", nil
}

type reminderDoneCmd struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func (b *Brain) reminderUnacked(r remind.Reminder) {
	go b.escalate(r)
}

// escalate runs a quiet Claude turn asking Fo to text r.EscalateTo. Nothing
// is spoken: the person at the desk only sees the STATE line.
func (b *Brain) escalate(r remind.Reminder) {
	select {
	case b.turn <- struct{}{}:
	case <-time.After(b.cfg.TurnTimeout):
		b.logf("escalation %s: gave up waiting for the current turn", r.ID)
		return
	}
	defer b.endTurn()

	mins := int(b.cfg.Reminders.AckWindow.Round(time.Minute) / time.Minute)
	var prompt string
	if r.Medication {
		prompt = fmt.Sprintf("Reminder check-in: %s hasn't confirmed the %s medicine (%s) %d minutes after the reminder went off. Use Fo to text %s a short, calm heads-up.",
			b.cfg.Profile.Label(r.Who), r.FiredAt.Format("15:04"), r.Text, mins, r.EscalateTo)
	} else {
		prompt = fmt.Sprintf("Reminder check-in: nobody has confirmed the reminder %q that went off at %s, %d minutes ago. Use Fo to text %s a short, calm heads-up.",
			r.Text, r.FiredAt.Format("15:04"), mins, r.EscalateTo)
	}
	b.logf("escalation %s: %s", r.ID, prompt)
	_ = b.cfg.Device.State("working", clip("Texting "+r.EscalateTo+"…", lineMax))
	ctx, cancel := context.WithTimeout(context.Background(), b.cfg.TurnTimeout)
	defer cancel()
	start := time.Now()
	res, err := b.cfg.Escalator.Think(ctx, "", prompt, func(e Event) {
		b.logf("escalation %s: %s %s %q", r.ID, e.Kind, e.Tool, clip(e.Text, 160))
	})
	if err != nil {
		b.logf("escalation %s FAILED after %dms: %v", r.ID, time.Since(start).Milliseconds(), err)
		_ = b.cfg.Device.State("sad", "Couldn't reach family")
		return
	}
	b.logf("escalation %s done in %dms: %q ($%.4f)", r.ID, time.Since(start).Milliseconds(), res.Reply, res.CostUSD)
	_ = b.cfg.Device.State("idle", clip(r.EscalateTo+" was told", lineMax))
}

// ---- approvals ----

func (b *Brain) resolveApproval(id string, ok bool) {
	answer := "declined"
	if ok {
		answer = "approved"
	}
	b.mu.Lock()
	ch, found := b.approvals[id]
	delete(b.approvals, id)
	b.mu.Unlock()
	if !found {
		b.logf("approval %s=%s: nobody waiting (late answer?)", id, answer)
		return
	}
	b.logf("approval %s: %s", id, answer)
	ch <- answer // buffered, one answer per id
}

// RequestApproval shows summary on the device and waits for tap/hold.
func (b *Brain) RequestApproval(ctx context.Context, summary string) string {
	const noDevice = "no device connected (treat as declined)"
	if !b.cfg.Device.Connected() {
		return noDevice
	}
	b.mu.Lock()
	b.approvalN++
	id := fmt.Sprintf("a%d", b.approvalN)
	ch := make(chan string, 1)
	b.approvals[id] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.approvals, id)
		b.mu.Unlock()
	}()

	if err := b.cfg.Device.Command(approveCmd{Type: "approve", ID: id, Text: summary}); err != nil {
		b.logf("approval %s: %v", id, err)
		return noDevice
	}
	b.logf("approval %s asked: %q", id, summary)
	t := time.NewTimer(b.cfg.ApprovalTimeout)
	defer t.Stop()
	select {
	case answer := <-ch:
		return answer
	case <-t.C:
		b.logf("approval %s: no answer", id)
		return "no answer (treat as declined)"
	case <-ctx.Done():
		return "cancelled (treat as declined)"
	}
}

// spoken adjusts text for the voice only (captions keep the original): the name is
// written "Famyy" but should sound like "Fammy", short for family.
func spoken(text string) string {
	return strings.NewReplacer("Famyy", "Fammy", "FAMYY", "Fammy", "famyy", "fammy").Replace(text)
}

// loudness returns the 10th, 50th and 90th percentile RMS of the recording's
// 30 ms frames: the same frames and scale as the device's silence detector,
// so "quiet" is the room and "loud" is the speaker.
func loudness(pcm []byte) (quiet, middle, loud int) {
	const frame = 480 * 2 // bytes per 30 ms at 16 kHz
	var rms []int
	for i := 0; i+frame <= len(pcm); i += frame {
		var sum float64
		for j := i; j < i+frame; j += 2 {
			v := float64(int16(binary.LittleEndian.Uint16(pcm[j:])))
			sum += v * v
		}
		rms = append(rms, int(math.Sqrt(sum/(frame/2))))
	}
	if len(rms) == 0 {
		return 0, 0, 0
	}
	sort.Ints(rms)
	at := func(p int) int { return rms[(len(rms)-1)*p/100] }
	return at(10), at(50), at(90)
}
