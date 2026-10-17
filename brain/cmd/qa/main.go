// Command qa drives the brain exactly like Femi's ESP32 would and checks every
// feature end to end, with real speech (Cartesia), real Claude and real Fo.
// Start the brain first, then run the scenarios:
//
//	FEMI_ACK_MINUTES=1 go run ./cmd/femi-brain
//	go run ./cmd/qa [-only name1,name2] [-call "+91 81717 64392"] [-skip-call]
//
// Prints PASS/FAIL per scenario and writes the full report to /home/leog/femi/qa/report.md.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"femi/brain/cartesia"
	"femi/brain/link"
)

// ---------- the fake device ----------

type frame struct {
	t  byte
	p  []byte
	at time.Time
}

type device struct {
	conn   net.Conn
	wmu    sync.Mutex
	frames chan frame
}

func dial(addr string) (*device, error) {
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return nil, err
	}
	d := &device{conn: c, frames: make(chan frame, 4096)}
	go func() {
		for {
			f, err := link.ReadFrame(c)
			if err != nil {
				close(d.frames)
				return
			}
			if f.Type == link.TypePing {
				continue
			}
			d.frames <- frame{f.Type, f.Payload, time.Now()}
		}
	}()
	d.send(link.TypeHello, `{"device":"femi","fw":"qa"}`)
	return d, nil
}

func (d *device) send(t byte, payload string) { d.sendBytes(t, []byte(payload)) }

func (d *device) sendBytes(t byte, payload []byte) {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if err := link.WriteFrame(d.conn, t, payload); err != nil {
		fmt.Println("write:", err)
	}
}

func (d *device) text(s string) {
	b, _ := json.Marshal(map[string]string{"type": "text", "text": s})
	d.sendBytes(link.TypeEvent, b)
}

func (d *device) audio(pcm []byte, reason string) {
	d.send(link.TypeAudioStart, `{"rate":16000,"trigger":"tap"}`)
	for i := 0; i < len(pcm); i += 2048 {
		d.sendBytes(link.TypeAudioChunk, pcm[i:min(i+2048, len(pcm))])
	}
	d.send(link.TypeAudioEnd, fmt.Sprintf(`{"reason":%q}`, reason))
}

func (d *device) event(v map[string]any) {
	b, _ := json.Marshal(v)
	d.sendBytes(link.TypeEvent, b)
}

// drain throws away anything left over from the previous scenario
func (d *device) drain() {
	for {
		select {
		case <-d.frames:
		case <-time.After(300 * time.Millisecond):
			return
		}
	}
}

// ---------- what came back ----------

type say struct {
	id, text string
	bytes    int
	at       time.Duration
}

type result struct {
	states    []string
	says      []say
	commands  []map[string]any
	images    int
	imageData int
	firstMsg  time.Duration
	elapsed   time.Duration
	approvals []string
}

type policy struct {
	approve   string        // "yes", "no" or "" (don't answer)
	answers   []string      // replies to Femi's questions, in order
	untilSays int           // stop after this many SAYs (default 1)
	quiet     time.Duration // ...and this long with nothing new (default 4 s)
	timeout   time.Duration // give up after this (default 90 s)
	ackRemind bool          // tap reminders when they appear
	stopWhen  func(*result) bool
}

func (d *device) collect(p policy) *result {
	if p.untilSays == 0 {
		p.untilSays = 1
	}
	if p.quiet == 0 {
		p.quiet = 4 * time.Second
	}
	if p.timeout == 0 {
		p.timeout = 90 * time.Second
	}
	r := &result{}
	start := time.Now()
	deadline := time.After(p.timeout)
	answered := 0
	var cur *say
	for {
		quiet := time.After(p.quiet)
		if len(r.says) < p.untilSays && (p.stopWhen == nil || !p.stopWhen(r)) {
			quiet = nil
		}
		select {
		case f, ok := <-d.frames:
			if !ok {
				r.states = append(r.states, "LINK CLOSED")
				r.elapsed = time.Since(start)
				return r
			}
			if r.firstMsg == 0 {
				r.firstMsg = time.Since(start)
			}
			var m map[string]any
			switch f.t {
			case link.TypeState:
				json.Unmarshal(f.p, &m)
				r.states = append(r.states, fmt.Sprintf("%5.1fs %s %q", time.Since(start).Seconds(), m["face"], m["line"]))
			case link.TypeSayStart:
				json.Unmarshal(f.p, &m)
				s := say{id: fmt.Sprint(m["id"]), text: fmt.Sprint(m["text"]), at: time.Since(start)}
				r.says = append(r.says, s)
				cur = &r.says[len(r.says)-1]
			case link.TypeSayChunk:
				if cur != nil {
					cur.bytes += len(f.p)
				}
			case link.TypeSayEnd:
				if cur != nil {
					d.event(map[string]any{"type": "played", "id": cur.id})
					text := cur.text
					cur = nil
					if strings.HasSuffix(strings.TrimSpace(text), "?") && answered < len(p.answers) {
						time.Sleep(500 * time.Millisecond)
						d.text(p.answers[answered])
						r.states = append(r.states, fmt.Sprintf("%5.1fs (qa answered %q)", time.Since(start).Seconds(), p.answers[answered]))
						answered++
						p.untilSays++
					}
				}
			case link.TypeCommand:
				json.Unmarshal(f.p, &m)
				r.commands = append(r.commands, m)
				switch m["type"] {
				case "approve":
					r.approvals = append(r.approvals, fmt.Sprint(m["text"]))
					if p.approve != "" {
						time.Sleep(1500 * time.Millisecond) // a person reading the card
						d.event(map[string]any{"type": "approval", "id": m["id"], "ok": p.approve == "yes"})
					}
				case "reminder":
					if p.ackRemind {
						time.Sleep(2 * time.Second)
						d.event(map[string]any{"type": "ack", "id": m["id"]})
						p.untilSays++
					}
				case "image":
					r.images++
				}
			case link.TypeImageChunk:
				r.imageData += len(f.p)
			}
		case <-quiet:
			r.elapsed = time.Since(start)
			return r
		case <-deadline:
			r.states = append(r.states, "TIMEOUT")
			r.elapsed = time.Since(start)
			return r
		}
	}
}

func (r *result) lastSay() string {
	if len(r.says) == 0 {
		return ""
	}
	return r.says[len(r.says)-1].text
}

func (r *result) allSays() string {
	var b []string
	for _, s := range r.says {
		b = append(b, s.text)
	}
	return strings.Join(b, " | ")
}

func (r *result) command(typ string) map[string]any {
	for _, c := range r.commands {
		if c["type"] == typ {
			return c
		}
	}
	return nil
}

// ---------- checks ----------

var urlRe = regexp.MustCompile(`https?://|www\.`)

// spoken replies must read like speech: short, no markdown, links or emoji
func styleIssues(s string) []string {
	var out []string
	if strings.ContainsAny(s, "*#`") || strings.Contains(s, "\n- ") || strings.HasPrefix(s, "- ") {
		out = append(out, "markdown")
	}
	if urlRe.MatchString(s) {
		out = append(out, "url")
	}
	for _, r := range s {
		if r >= 0x1F300 || (r >= 0x2600 && r <= 0x27BF) {
			out = append(out, "emoji")
			break
		}
	}
	sentences := 0
	for i, r := range s {
		if (r == '.' || r == '!' || r == '?') && (i+1 == len(s) || unicode.IsSpace(rune(s[i+1]))) {
			sentences++
		}
	}
	if sentences > 4 {
		out = append(out, fmt.Sprintf("%d sentences", sentences))
	}
	if len(s) > 420 {
		out = append(out, fmt.Sprintf("%d chars", len(s)))
	}
	return out
}

type check struct {
	ok   bool
	what string
}

func want(ok bool, what string) check { return check{ok, what} }

// ---------- scenarios ----------

type scenario struct {
	name string
	run  func(d *device, voice *cartesia.Client) (*result, []check)
}

func say1(r *result) []check {
	var cs []check
	cs = append(cs, want(len(r.says) > 0 && r.lastSay() != "", "Femi replied"))
	for _, s := range r.says {
		if iss := styleIssues(s.text); len(iss) > 0 {
			cs = append(cs, want(false, "spoken style: "+strings.Join(iss, ", ")))
		}
	}
	return cs
}

func hasVoice(r *result) check {
	for _, s := range r.says {
		if s.bytes > 0 {
			return want(true, fmt.Sprintf("reply has audio (%.1f s)", float64(s.bytes)/32000))
		}
	}
	return want(false, "reply has audio")
}

func contains(s string, any ...string) bool {
	s = strings.ToLower(s)
	for _, a := range any {
		if strings.Contains(s, strings.ToLower(a)) {
			return true
		}
	}
	return false
}

func scenarios(callNumber string) []scenario {
	return []scenario{
		{"voice-weather", func(d *device, v *cartesia.Client) (*result, []check) {
			pcm, err := v.Speak(context.Background(), "Hey Femi, what's the weather in Bengaluru right now?", 16000)
			if err != nil {
				return &result{}, []check{want(false, "make test speech: "+err.Error())}
			}
			d.audio(pcm, "silence")
			r := d.collect(policy{timeout: 120 * time.Second})
			cs := append(say1(r), hasVoice(r))
			cs = append(cs, want(contains(r.allSays(), "degree", "°", "sunny", "cloud", "rain", "humid", "weather"), "answer is about the weather"))
			return r, cs
		}},
		{"kid-learning", func(d *device, v *cartesia.Client) (*result, []check) {
			d.text("Hi Femi, I'm Riya. Tell me one fun fact about the moon.")
			r := d.collect(policy{})
			return r, append(say1(r), want(contains(r.allSays(), "moon"), "talks about the moon"))
		}},
		{"timer", func(d *device, v *cartesia.Client) (*result, []check) {
			d.text("Set a timer for 5 minutes.")
			r := d.collect(policy{})
			c := r.command("timer")
			return r, append(say1(r), want(c != nil && fmt.Sprint(c["minutes"]) == "5", "timer command for 5 minutes"))
		}},
		{"memory", func(d *device, v *cartesia.Client) (*result, []check) {
			d.text("How many minutes was the timer I just asked for?")
			r := d.collect(policy{})
			return r, append(say1(r), want(contains(r.allSays(), "5", "five"), "remembers it was 5 minutes"))
		}},
		{"timer-cancel", func(d *device, v *cartesia.Client) (*result, []check) {
			d.text("Cancel the timer.")
			r := d.collect(policy{})
			c := r.command("timer")
			return r, append(say1(r), want(c != nil && c["action"] == "cancel", "timer cancel command"))
		}},
		{"game", func(d *device, v *cartesia.Client) (*result, []check) {
			d.text("Let's play a game!")
			r := d.collect(policy{})
			c := r.command("app")
			return r, append(say1(r), want(c != nil && c["name"] == "game", "starts the game app"))
		}},
		{"morse", func(d *device, v *cartesia.Client) (*result, []check) {
			d.text("I want to practise Morse code.")
			r := d.collect(policy{})
			c := r.command("app")
			return r, append(say1(r), want(c != nil && c["name"] == "morse", "starts the morse app"))
		}},
		{"theme", func(d *device, v *cartesia.Client) (*result, []check) {
			d.text("Switch to hacker mode.")
			r1 := d.collect(policy{})
			d.text("Now go back to normal mode.")
			r2 := d.collect(policy{})
			c1, c2 := r1.command("theme"), r2.command("theme")
			r1.says = append(r1.says, r2.says...)
			r1.commands = append(r1.commands, r2.commands...)
			r1.states = append(r1.states, r2.states...)
			return r1, append(say1(r1),
				want(c1 != nil && c1["name"] == "hacker", "hacker theme command"),
				want(c2 != nil && c2["name"] == "normal", "normal theme command"))
		}},
		{"photos", func(d *device, v *cartesia.Client) (*result, []check) {
			d.text("Show me the demo photos.")
			r := d.collect(policy{quiet: 9 * time.Second}) // photos arrive ~6 s apart
			return r, append(say1(r),
				want(r.images >= 1, fmt.Sprintf("photos sent (%d)", r.images)),
				want(r.images > 0 && r.imageData == r.images*320*240*2, fmt.Sprintf("photo pixels complete (%d bytes)", r.imageData)))
		}},
		{"reminder", func(d *device, v *cartesia.Client) (*result, []check) {
			d.text("Remind me to drink a glass of water in 1 minute.")
			r := d.collect(policy{untilSays: 1, ackRemind: true, timeout: 150 * time.Second,
				stopWhen: func(r *result) bool { return r.command("reminder") == nil }})
			// keep collecting until the reminder fired, was tapped and thanked
			if r.command("reminder") == nil {
				r2 := d.collect(policy{untilSays: 2, ackRemind: true, timeout: 120 * time.Second, quiet: 5 * time.Second})
				r.says = append(r.says, r2.says...)
				r.commands = append(r.commands, r2.commands...)
				r.states = append(r.states, r2.states...)
			}
			happy := false
			for _, s := range r.states {
				if strings.Contains(s, "happy") {
					happy = true
				}
			}
			return r, append(say1(r),
				want(r.command("reminder") != nil, "reminder fired on the device"),
				want(happy, "acknowledging it makes Femi happy"))
		}},
		{"approval-declined", func(d *device, v *cartesia.Client) (*result, []check) {
			d.text("Email Rahul Sharma that dinner is at 8 tonight.")
			r := d.collect(policy{approve: "no", timeout: 120 * time.Second})
			return r, append(say1(r),
				want(len(r.approvals) > 0, "asked for approval before emailing"),
				want(contains(r.lastSay(), "won't", "will not", "didn't", "did not", "not send", "haven't", "cancel", "no problem", "okay", "ok", "all right", "alright"), "accepts the no"))
		}},
		{"calendar-read", func(d *device, v *cartesia.Client) (*result, []check) {
			d.text("What's on my calendar today?")
			r := d.collect(policy{timeout: 150 * time.Second})
			return r, append(say1(r), want(len(r.approvals) == 0, "reading needs no approval"))
		}},
		{"silence", func(d *device, v *cartesia.Client) (*result, []check) {
			d.audio(make([]byte, 16000*2*3/2), "silence")
			r := d.collect(policy{timeout: 30 * time.Second})
			return r, append(say1(r), want(contains(r.lastSay(), "catch", "didn't hear", "sorry", "say that again"), "says it didn't catch that"))
		}},
		{"cancelled", func(d *device, v *cartesia.Client) (*result, []check) {
			d.audio(make([]byte, 4096), "cancel")
			r := d.collect(policy{timeout: 6 * time.Second})
			return r, []check{want(len(r.says) == 0, "a cancelled request is dropped silently")}
		}},
		{"busy", func(d *device, v *cartesia.Client) (*result, []check) {
			d.text("What's the weather in Delhi right now?")
			time.Sleep(1500 * time.Millisecond)
			d.text("And what's two plus two?")
			r := d.collect(policy{untilSays: 2, timeout: 150 * time.Second})
			return r, append(say1(r), want(contains(r.allSays(), "moment", "still on", "one sec"), "a second request while busy gets a polite wait"))
		}},
		{"call", func(d *device, v *cartesia.Client) (*result, []check) {
			d.text(fmt.Sprintf("Please call %s and tell them: this is a test call from Femi, the family assistant, and everything is working. Then hang up.", callNumber))
			r := d.collect(policy{approve: "yes", answers: []string{"Yes, go ahead."}, timeout: 6 * time.Minute, quiet: 8 * time.Second})
			working := 0
			for _, s := range r.states {
				if strings.Contains(s, "working") {
					working++
				}
			}
			return r, append(say1(r),
				want(len(r.approvals) > 0, "asked for approval before calling"),
				want(working > 0, fmt.Sprintf("showed Fo's progress while calling (%d updates)", working)))
		}},
	}
}

// ---------- main ----------

func loadKey() string {
	if k := os.Getenv("CARTESIA_API_KEY"); k != "" {
		return k
	}
	b, _ := os.ReadFile("/home/leog/femi/brain/.env")
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && k == "CARTESIA_API_KEY" {
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "brain address")
	only := flag.String("only", "", "comma-separated scenario names to run")
	call := flag.String("call", "+91 81717 64392", "number Fo may call in the call scenario")
	skipCall := flag.Bool("skip-call", false, "skip the real phone call")
	flag.Parse()

	voice := cartesia.New(loadKey())
	d, err := dial(*addr)
	if err != nil {
		fmt.Println("can't reach the brain:", err)
		os.Exit(1)
	}
	d.drain()

	var report strings.Builder
	fmt.Fprintf(&report, "# Femi QA — %s\n\n", time.Now().Format("Mon 2 Jan 15:04"))
	passed, failed := 0, 0
	for _, s := range scenarios(*call) {
		if *only != "" && !strings.Contains(","+*only+",", ","+s.name+",") {
			continue
		}
		if s.name == "call" && *skipCall {
			continue
		}
		fmt.Printf("\n=== %s\n", s.name)
		r, checks := s.run(d, voice)
		ok := true
		for _, c := range checks {
			ok = ok && c.ok
		}
		mark := "PASS"
		if !ok {
			mark = "FAIL"
			failed++
		} else {
			passed++
		}
		fmt.Fprintf(&report, "## %s %s  (%.1f s, first response %.1f s)\n\n", mark, s.name, r.elapsed.Seconds(), r.firstMsg.Seconds())
		for _, c := range checks {
			sym := "✓"
			if !c.ok {
				sym = "✗"
			}
			fmt.Fprintf(&report, "- %s %s\n", sym, c.what)
		}
		for _, a := range r.approvals {
			fmt.Fprintf(&report, "- approval card: %q\n", a)
		}
		for _, sy := range r.says {
			fmt.Fprintf(&report, "- Femi (%.1fs, %.1f s audio): %q\n", sy.at.Seconds(), float64(sy.bytes)/32000, sy.text)
		}
		for _, c := range r.commands {
			b, _ := json.Marshal(c)
			fmt.Fprintf(&report, "- command: %s\n", b)
		}
		fmt.Fprintf(&report, "\n<details><summary>states</summary>\n\n```\n%s\n```\n</details>\n\n", strings.Join(r.states, "\n"))
		fmt.Printf("%s %s (%.1f s)\n", mark, s.name, r.elapsed.Seconds())
		d.drain()
	}
	fmt.Fprintf(&report, "**%d passed, %d failed**\n", passed, failed)

	out := "/home/leog/femi/qa/report.md"
	os.MkdirAll(filepath.Dir(out), 0o755)
	os.WriteFile(out, []byte(report.String()), 0o644)
	fmt.Printf("\n%d passed, %d failed — report: %s\n", passed, failed, out)
}
