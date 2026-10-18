package femi

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"femi/brain/link"
)

// Everything Famyy says goes through one ordered queue: the pieces of a
// reply as they come, the reply itself sentence by sentence, reminders, Fo's
// reports. Each item's voice is synthesised the moment it is queued, while
// earlier speech is still playing, and items go to the device one at a time,
// so they never overlap and come out in the order they were queued.

type speech struct {
	ctx  context.Context
	id   string
	text string
	opts link.SayOptions
	pcm  chan []byte   // the audio, filled in ahead of time
	done chan struct{} // closed once it has been handed to the device
	tts  time.Duration
}

// enqueue queues text to be said and starts its synthesis now.
func (b *Brain) enqueue(ctx context.Context, text string, cache bool, opts link.SayOptions) *speech {
	s := b.newSpeech(ctx, text, opts)
	go func() {
		pcm, took := b.synth(ctx, text, cache)
		s.tts = took
		s.pcm <- pcm
	}()
	b.speechQ <- s
	return s
}

// enqueueAudio queues audio that is ready already (a call replay, an empty reply).
func (b *Brain) enqueueAudio(ctx context.Context, text string, pcm []byte, opts link.SayOptions) *speech {
	s := b.newSpeech(ctx, text, opts)
	s.pcm <- pcm
	b.speechQ <- s
	return s
}

func (b *Brain) newSpeech(ctx context.Context, text string, opts link.SayOptions) *speech {
	b.mu.Lock()
	b.sayN++
	s := &speech{ctx: ctx, id: fmt.Sprintf("s%d", b.sayN), text: text, opts: opts, pcm: make(chan []byte, 1), done: make(chan struct{})}
	b.mu.Unlock()
	return s
}

// speaker hands queued speech to the device, one item at a time. A stopped
// request's items are dropped.
func (b *Brain) speaker() {
	for s := range b.speechQ {
		pcm := <-s.pcm
		if s.ctx.Err() == nil {
			if err := b.cfg.Device.Say(s.ctx, s.id, caption(s.text), pcm, s.opts); err != nil && !stopped(s.ctx) {
				b.logf("say %s %q: %v", s.id, clip(s.text, 80), err)
			}
		}
		close(s.done)
	}
}

// synth returns text's audio (nil without a voice or on failure: the device
// then shows the caption only).
func (b *Brain) synth(ctx context.Context, text string, cache bool) ([]byte, time.Duration) {
	b.mu.Lock()
	pcm, cached := b.phrases[text]
	b.mu.Unlock()
	if b.cfg.Voice == nil || cached || text == "" {
		return pcm, 0
	}
	start := time.Now()
	pcm, err := b.cfg.Voice.Speak(ctx, spoken(text), SampleRate)
	took := time.Since(start)
	if err != nil {
		if !stopped(ctx) {
			b.logf("tts failed after %dms, sending caption only: %v", took.Milliseconds(), err)
		}
		return nil, took
	}
	if cache {
		b.mu.Lock()
		b.phrases[text] = pcm
		b.mu.Unlock()
	}
	return pcm, took
}

// sentenceEnd ends a sentence: . ! ? or … (and any closing quote) before a space.
var sentenceEnd = regexp.MustCompile(`[.!?…]+["'”’)\]]*\s+`)

// sentences splits a reply for speaking it piece by piece; a short sentence
// joins the next one so the speech doesn't sound choppy.
func sentences(s string) []string {
	var parts []string
	rest := strings.TrimSpace(s)
	for rest != "" {
		loc := sentenceEnd.FindStringIndex(rest)
		if loc == nil {
			parts = append(parts, rest)
			break
		}
		parts = append(parts, strings.TrimSpace(rest[:loc[1]]))
		rest = strings.TrimSpace(rest[loc[1]:])
	}
	var out []string
	for _, p := range parts {
		if n := len(out); n > 0 && len(out[n-1]) < 40 {
			out[n-1] += " " + p
		} else {
			out = append(out, p)
		}
	}
	return out
}

// speakReply says a turn's reply sentence by sentence: every part but the
// last is an aside, and the last one listens if listen. It returns once
// everything has been handed to the device, with the first part's synthesis time.
func (b *Brain) speakReply(ctx context.Context, reply string, opts link.SayOptions) time.Duration {
	parts := sentences(reply)
	if len(parts) == 0 {
		<-b.enqueueAudio(ctx, "", nil, opts).done
		return 0
	}
	var first, last *speech
	for i, p := range parts {
		o := opts
		if i < len(parts)-1 {
			o.Aside, o.Listen = true, false
		}
		last = b.enqueue(ctx, p, false, o)
		if i == 0 {
			first = last
		}
	}
	<-last.done
	return first.tts
}

// withoutSpoken removes from the final reply what was already said mid-turn.
func withoutSpoken(reply string, said []string) string {
	for _, p := range said {
		if i := strings.Index(reply, p); i >= 0 {
			reply = reply[:i] + reply[i+len(p):]
		}
	}
	return strings.TrimSpace(reply)
}
