package femi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"femi/brain/link"
)

// CallLine is one line of a phone call Fo made, as replay_call takes it.
type CallLine struct {
	Speaker string `json:"speaker"` // "fo" | "other"
	Text    string `json:"text"`
}

const (
	listRows      = 8
	listRowChars  = 24
	maxCallLines  = 40
	callLineChars = 300
	callGapMs     = 350 // pause between the two sides
)

// ShowList puts a short list on the screen until it is tapped.
func (b *Brain) ShowList(title string, items []string) (string, error) {
	title = strings.TrimSpace(title)
	var rows []string
	for _, it := range items {
		it = strings.TrimSpace(strings.ReplaceAll(it, "|", "/"))
		if it != "" && len(rows) < listRows {
			rows = append(rows, clip(it, listRowChars))
		}
	}
	if len(rows) == 0 {
		return "", errors.New(`"items" is empty: give at least one row`)
	}
	cmd := struct {
		Type  string `json:"type"`
		Title string `json:"title"`
		Items string `json:"items"`
	}{"list", clip(title, listRowChars), strings.Join(rows, "|")}
	ok := fmt.Sprintf("Showing %d rows on the screen until it is tapped.", len(rows))
	if len(items) > len(rows) {
		ok += fmt.Sprintf(" Only the first %d fit.", listRows)
	}
	return b.command(cmd, ok)
}

// ReplayCall reads a call back in two voices and starts playing it. It
// returns once the audio is ready; a tap on the device stops it.
func (b *Brain) ReplayCall(ctx context.Context, title string, lines []CallLine) (string, error) {
	if !b.cfg.Device.Connected() {
		return "", errNoScreen
	}
	voice, ok := b.cfg.Voice.(VoiceAs)
	if !ok || b.cfg.FoVoice == "" || b.cfg.CallVoice == "" {
		return "", errors.New("calls can't be played back right now: no voice to read them")
	}
	var keep []CallLine
	for _, l := range lines {
		l.Text = clip(strings.TrimSpace(l.Text), callLineChars)
		if tooShort(l.Text) {
			continue
		}
		keep = append(keep, l)
		if len(keep) == maxCallLines {
			break
		}
	}
	if len(keep) == 0 {
		return "", errors.New(`"lines" has nothing to play`)
	}

	// each line in its speaker's voice, a few at a time
	pcms := make([][]byte, len(keep))
	errs := make([]error, len(keep))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, l := range keep {
		id := b.cfg.CallVoice
		if l.Speaker == "fo" {
			id = b.cfg.FoVoice
		}
		wg.Add(1)
		go func(i int, id, text string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pcms[i], errs[i] = voice.SpeakAs(ctx, id, text, SampleRate)
		}(i, id, l.Text)
	}
	wg.Wait()
	gap := make([]byte, SampleRate*2*callGapMs/1000)
	var pcm []byte
	for i, p := range pcms {
		if errs[i] != nil {
			return "", fmt.Errorf("couldn't voice line %d of the call: %v", i+1, errs[i])
		}
		if i > 0 {
			pcm = append(pcm, gap...)
		}
		pcm = append(pcm, p...)
	}

	b.mu.Lock()
	b.sayN++
	id := fmt.Sprintf("s%d", b.sayN)
	b.mu.Unlock()
	title = "Call: " + clip(strings.TrimSpace(title), 40)
	play := b.requestCtx() // not the tool call's: the call keeps playing after the tool returns
	go func() {
		if err := b.cfg.Device.Say(play, id, title, pcm, link.SayOptions{Aside: true}); err != nil && !stopped(play) {
			b.logf("replay %s: %v", id, err)
		}
	}()
	secs := len(pcm) / (SampleRate * 2)
	b.logf("replay %s: %q, %d lines, %d s", id, title, len(keep), secs)
	return fmt.Sprintf("Playing the call now (%d lines, about %d seconds). Reply with one very short line only, "+
		"or nothing more than \"That's the call.\"", len(keep), secs), nil
}
