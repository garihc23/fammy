package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"femi/brain/agent"
	"femi/brain/femi"
)

// This file is the one place that adapts package agent (claude -p runner)
// to the femi.Thinker interface.

const escalationPrompt = `## This turn is an automatic reminder check-in
Femi started this turn by itself because a reminder was not confirmed; nobody is at the desk to approve anything and your reply is only logged, never spoken. The family set this check-in up in advance, so do NOT call request_approval: use Fo to send exactly the one short, calm, kind text message asked for (say it is from Femi), and confirm with Fo if it asks. Do not send anything else, call, email, book or buy.`

type agentThinker struct{ opts agent.Options }

func (t agentThinker) Think(ctx context.Context, sessionID, text string, onEvent func(femi.Event)) (femi.Result, error) {
	res, err := agent.Run(ctx, t.opts, sessionID, text, func(e agent.Event) {
		onEvent(femi.Event{Kind: e.Kind, Tool: e.Tool, Text: e.Text, Status: e.Status, FoConversation: e.FoConversation})
	})
	return femi.Result{Reply: res.Reply, SessionID: res.SessionID, CostUSD: res.CostUSD}, err
}

// MCPConfig is the --mcp-config that points Claude at Femi's tool server.
func mcpConfig(toolsPort int) string {
	return fmt.Sprintf(`{"mcpServers":{"femi":{"type":"http","url":"http://127.0.0.1:%d/mcp"}}}`, toolsPort)
}

// newThinkers returns the thinker for normal turns and the one for quiet
// reminder escalations (same tools, extra instructions).
func newThinkers(system string, cfg config) (*warmThinker, femi.Thinker, femi.FoWatcher) {
	var allowed []string
	for _, n := range femi.ToolNames() {
		allowed = append(allowed, "mcp__femi__"+n)
	}
	opts := agent.Options{
		SystemPrompt:      system,
		MCPConfigJSON:     mcpConfig(cfg.toolsPort),
		ExtraAllowedTools: allowed,
		Model:             cfg.model,
		Effort:            cfg.effort,
		Env:               cfg.claudeEnv(),
		NoPersist:         true,                  // a family's conversations and camera photos stay off the disk
		BuiltinTools:      []string{"WebSearch"}, // no files, no shell: only Famyy's tools, Fo and web search
		WorkDir:           workDir,
		Timeout:           turnBudget,
	}
	esc := opts
	esc.SystemPrompt = system + "\n\n" + escalationPrompt
	// escalations are rare and nobody waits on them: a one-off run is fine there
	return newWarmThinker(opts), agentThinker{esc}, foWatcher{opts}
}

const watcherPrompt = `You only wait for one Fo errand that is already running. Call get_fo_reply with the conversationId you are given, and again each time its status is "working". As soon as the status is anything else, stop and reply with the single word: done. Never call ask_fo or any other tool, and never start anything new.`

// foWatcher waits for a Fo errand in its own one-off Claude run, so the
// conversation is free meanwhile (the brain can't reach Fo itself: Fo's
// sign-in lives in Claude Code).
type foWatcher struct{ opts agent.Options }

func (w foWatcher) WaitForFo(ctx context.Context, conv string) (string, string, error) {
	opts := w.opts
	opts.SystemPrompt = watcherPrompt
	opts.MCPConfigJSON = "" // Fo only, not Famyy's device tools
	opts.ExtraAllowedTools = nil
	opts.BuiltinTools = nil
	opts.Timeout = 30 * time.Minute
	var status, text string
	_, err := agent.Run(ctx, opts, "", "conversationId: "+conv, func(e agent.Event) {
		if e.Kind == agent.KindFoDone && (e.FoConversation == "" || e.FoConversation == conv) {
			status, text = e.Status, e.Text
		}
	})
	if status != "" {
		return status, text, nil
	}
	if err == nil {
		err = errors.New("the watcher stopped before Fo settled")
	}
	return "", "", err
}

const checkPrompt = `Call get_fo_reply exactly once with the conversationId you are given, then reply with the single word: done. Never call any other tool.`

func (w foWatcher) CheckFo(ctx context.Context, conv string) (string, error) {
	opts := w.opts
	opts.SystemPrompt = checkPrompt
	opts.MCPConfigJSON = ""
	opts.ExtraAllowedTools = nil
	opts.BuiltinTools = nil
	opts.Timeout = 2 * time.Minute
	var text string
	_, err := agent.Run(ctx, opts, "", "conversationId: "+conv, func(e agent.Event) {
		if e.Kind == agent.KindFoDone || e.Kind == agent.KindFoProgress {
			text = e.Text
		}
	})
	if text == "" && err == nil {
		err = errors.New("no reply from Fo")
	}
	return text, err
}

// Spare and finished Claude processes are replaced after this long, so their
// Fo connection and the date in Claude's context never go stale.
const (
	spareMaxAge  = 10 * time.Minute
	finishedIdle = 30 * time.Minute
)

// warmThinker answers on a Claude process that is already running: the one
// holding the conversation in progress, or, for a new conversation, a spare
// started ahead of time. A turn then costs only Claude's answer, not Claude
// Code starting up and connecting to Fo (~3-4 s).
type warmThinker struct {
	opts agent.Options

	mu    sync.Mutex
	cur   *agent.Session // the conversation in progress
	spare *agent.Session // ready for the next new conversation
}

func newWarmThinker(opts agent.Options) *warmThinker { return &warmThinker{opts: opts} }

// start warms up the first spare and keeps the processes fresh. Call it once
// Femi's tool server is listening: Claude connects to it as it starts.
func (t *warmThinker) start() {
	t.refill()
	go func() {
		for range time.Tick(time.Minute) {
			t.recycle()
		}
	}()
}

func (t *warmThinker) Think(ctx context.Context, sessionID, text string, onEvent func(femi.Event)) (femi.Result, error) {
	s, err := t.session(sessionID)
	if err != nil {
		log.Printf("claude: can't keep a session running (%v): one-off turn", err)
		return agentThinker{t.opts}.Think(ctx, sessionID, text, onEvent)
	}
	res, err := s.Turn(ctx, text, func(e agent.Event) {
		onEvent(femi.Event{Kind: e.Kind, Tool: e.Tool, Text: e.Text, Status: e.Status, FoConversation: e.FoConversation})
	})
	return femi.Result{Reply: res.Reply, SessionID: res.SessionID, CostUSD: res.CostUSD}, err
}

// session returns the process for this conversation, switching to the spare
// for a new one.
func (t *warmThinker) session(sessionID string) (*agent.Session, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur != nil && t.cur.Alive() && sessionID != "" && t.cur.ID() == sessionID {
		return t.cur, nil
	}
	if t.cur != nil {
		go t.cur.Close()
		t.cur = nil
	}
	if sessionID != "" && !t.opts.NoPersist { // a conversation no running process holds (its process died): pick it back up
		s, err := agent.StartSession(t.opts, sessionID)
		t.cur = s
		return s, err
	}
	s := t.spare
	t.spare = nil
	if s == nil || !s.Alive() {
		if s != nil {
			go s.Close()
		}
		var err error
		if s, err = agent.StartSession(t.opts, ""); err != nil {
			return nil, err
		}
	}
	t.cur = s
	go t.refill()
	return s, nil
}

// refill starts a spare if there is none.
func (t *warmThinker) refill() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.spare != nil && t.spare.Alive() {
		return
	}
	s, err := agent.StartSession(t.opts, "")
	if err != nil {
		log.Printf("claude: spare session: %v", err)
		return
	}
	t.spare = s
}

// recycle replaces an old spare and ends a conversation nobody has continued.
func (t *warmThinker) recycle() {
	t.mu.Lock()
	if t.spare != nil && (!t.spare.Alive() || t.spare.Age() > spareMaxAge) {
		go t.spare.Close()
		t.spare = nil
	}
	if t.cur != nil && (!t.cur.Alive() || t.cur.Idle() > finishedIdle) {
		go t.cur.Close()
		t.cur = nil
	}
	t.mu.Unlock()
	t.refill()
}

// Close ends every Claude process.
func (t *warmThinker) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range []*agent.Session{t.cur, t.spare} {
		if s != nil {
			s.Close()
		}
	}
	t.cur, t.spare = nil, nil
}
