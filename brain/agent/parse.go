package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrNoResult is returned when the stream ends without a terminal "result" event.
var ErrNoResult = errors.New("agent: stream ended without a result event")

// Event shapes observed from Claude Code 2.1.x with
// `-p --output-format stream-json --verbose` (one JSON object per line):
//
//	{"type":"system","subtype":"init","session_id":"…",…}            // also hook_*, thinking_tokens, …
//	{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_…","name":"mcp__claude_ai_fo__ask_fo","input":{"message":"…"}}]},"parent_tool_use_id":null}
//	{"type":"assistant","message":{"content":[{"type":"text","text":"…"}]}}   // each content block arrives as its own line
//	{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_…","content":"{\"status\":\"working\",\"progress\":\"Working for 55s: …\",\"elapsedSeconds\":55,…}"}]},
//	 "tool_use_result":{"content":"…","structuredContent":{…}}}
//	{"type":"tool_progress","tool_name":"mcp__claude_ai_fo__ask_fo","elapsed_time_seconds":30,"heartbeat":true}
//	{"type":"result","subtype":"success","is_error":false,"result":"…","session_id":"…","duration_ms":19627,"total_cost_usd":0.034}
//
// Anything else (rate_limit_event, other system subtypes, unknown types) is ignored.
type streamLine struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	SessionID       string          `json:"session_id"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	Message         *streamMessage  `json:"message"`
	ToolUseResult   json.RawMessage `json:"tool_use_result"`
	MCPServers      json.RawMessage `json:"mcp_servers"` // system/init: [{"name":"claude.ai fo","status":"connected",…}]

	// tool_progress
	ToolName       string  `json:"tool_name"`
	ElapsedSeconds float64 `json:"elapsed_time_seconds"`

	// result
	IsError      bool            `json:"is_error"`
	Result       string          `json:"result"`
	DurationMS   float64         `json:"duration_ms"`
	TotalCostUSD *float64        `json:"total_cost_usd"`
	Errors       json.RawMessage `json:"errors"`
}

type streamMessage struct {
	// Content is a list of blocks for assistant/tool-result messages, but can be
	// a plain string for user prompts, so it is decoded lazily.
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`

	Text string `json:"text"` // text

	ID    string          `json:"id"`    // tool_use
	Name  string          `json:"name"`  // tool_use
	Input json.RawMessage `json:"input"` // tool_use

	ToolUseID string          `json:"tool_use_id"` // tool_result
	Content   json.RawMessage `json:"content"`     // tool_result: string or []block
	IsError   bool            `json:"is_error"`    // tool_result
}

// foReply is the JSON object Fo's ask_fo / get_fo_reply return as tool-result text,
// e.g. {"response":"…","status":"working","conversationId":"…","progress":"Working for 55s: …","elapsedSeconds":55}.
type foReply struct {
	Status         string
	Response       string
	Progress       string
	ElapsedSeconds float64
}

type parser struct {
	emit    func(Event)
	tools   map[string]string // tool_use_id -> full tool name
	pending []string          // assistant text held back until we know it is not the final reply

	res       Result
	gotResult bool
	turnErr   error
}

// ParseStream reads Claude Code stream-json output line by line as it arrives,
// calls onEvent for each Event in stream order, and returns the Result taken
// from the terminal "result" event.
//
// Assistant text is held back by one step so the final answer (which also
// arrives as Result.Reply) is not emitted twice as a "text" event.
//
// The error is non-nil if the stream could not be read, ended without a
// result event (ErrNoResult), or the result reported an error.
func ParseStream(r io.Reader, onEvent func(Event)) (Result, error) {
	if onEvent == nil {
		onEvent = func(Event) {}
	}
	p := &parser{emit: onEvent, tools: map[string]string{}}
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			p.handleLine(line)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			p.flushPending()
			return p.res, fmt.Errorf("agent: reading stream: %w", err)
		}
	}
	p.flushPending()
	if !p.gotResult {
		return p.res, ErrNoResult
	}
	return p.res, p.turnErr
}

func (p *parser) handleLine(line []byte) {
	var l streamLine
	if err := json.Unmarshal(line, &l); err != nil {
		slog.Debug("agent: skipping malformed stream line", "err", err, "line", clip(string(line), 200))
		return
	}
	if l.SessionID != "" {
		p.res.SessionID = l.SessionID
	}
	switch l.Type {
	case "assistant":
		p.handleAssistant(&l)
	case "user":
		p.handleUser(&l)
	case "tool_progress":
		p.handleToolProgress(&l)
	case "result":
		p.handleResult(&l)
	case "system":
		if l.Subtype == "init" {
			checkFoConnected(l.MCPServers)
		}
	default:
		// system/init, hooks, rate_limit_event, thinking_tokens, unknown types: ignore.
	}
}

func (p *parser) blocks(l *streamLine) []json.RawMessage {
	if l.Message == nil || len(l.Message.Content) == 0 {
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(l.Message.Content, &raw); err != nil {
		return nil // string content (e.g. a replayed user prompt)
	}
	return raw
}

func (p *parser) handleAssistant(l *streamLine) {
	subagent := l.ParentToolUseID != nil && *l.ParentToolUseID != ""
	for _, raw := range p.blocks(l) {
		var b contentBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			slog.Debug("agent: skipping malformed content block", "err", err)
			continue
		}
		switch b.Type {
		case "text":
			if subagent || strings.TrimSpace(b.Text) == "" {
				continue
			}
			p.pending = append(p.pending, b.Text)
		case "tool_use":
			if b.ID != "" {
				p.tools[b.ID] = b.Name
			}
			p.send(Event{Kind: KindToolCall, Tool: shortToolName(b.Name), Text: describeToolCall(b.Name, b.Input)})
		}
	}
}

func (p *parser) handleUser(l *streamLine) {
	blocks := p.blocks(l)
	var results []contentBlock
	for _, raw := range blocks {
		var b contentBlock
		if err := json.Unmarshal(raw, &b); err != nil || b.Type != "tool_result" {
			continue
		}
		results = append(results, b)
	}
	for _, b := range results {
		name := p.tools[b.ToolUseID]
		tool := shortToolName(name)
		if !isFoStatusTool(tool) {
			continue
		}
		text := toolResultText(b.Content)
		reply, ok := decodeFoReply(text)
		if !ok && len(results) == 1 {
			// MCP results also carry the parsed object as tool_use_result.structuredContent.
			reply, ok = decodeStructured(l.ToolUseResult)
		}
		switch {
		case ok && reply.Status == "working":
			msg := reply.Progress
			if msg == "" {
				msg = "Fo is working"
				if reply.ElapsedSeconds > 0 {
					msg = fmt.Sprintf("Fo is working (%ds)", int(reply.ElapsedSeconds))
				}
			}
			p.send(Event{Kind: KindFoProgress, Tool: tool, Text: msg, Status: reply.Status})
		case ok:
			// complete, failed, or any other settled state (e.g. Fo waiting on the user).
			p.send(Event{Kind: KindFoDone, Tool: tool, Text: reply.Response, Status: reply.Status})
		case b.IsError:
			p.send(Event{Kind: KindFoDone, Tool: tool, Text: text, Status: "failed"})
		case strings.TrimSpace(text) != "":
			// Not the JSON shape we know; treat a non-error reply as settled.
			p.send(Event{Kind: KindFoDone, Tool: tool, Text: text, Status: "complete"})
		}
	}
}

func (p *parser) handleToolProgress(l *streamLine) {
	// Claude Code heartbeats a long-running tool call every ~30 s. ask_fo can
	// block for ~40 s before it returns "working", so surface these for Fo.
	tool := shortToolName(l.ToolName)
	if !isFoStatusTool(tool) {
		return
	}
	p.send(Event{
		Kind:   KindFoProgress,
		Tool:   tool,
		Text:   fmt.Sprintf("Still waiting on Fo (%ds)", int(l.ElapsedSeconds)),
		Status: "working",
	})
}

func (p *parser) handleResult(l *streamLine) {
	p.gotResult = true
	p.res.Reply = l.Result
	p.res.Duration = time.Duration(l.DurationMS * float64(time.Millisecond))
	if l.TotalCostUSD != nil {
		p.res.CostUSD = *l.TotalCostUSD
	}
	// Drop held-back text that is part of the final reply; emit the rest.
	reply := strings.TrimSpace(l.Result)
	pending := p.pending
	p.pending = nil
	for _, t := range pending {
		if reply != "" && strings.Contains(reply, strings.TrimSpace(t)) {
			continue
		}
		p.emit(Event{Kind: KindText, Text: t})
	}

	p.turnErr = nil
	if l.IsError || (l.Subtype != "" && l.Subtype != "success") {
		msg := strings.TrimSpace(l.Result)
		if msg == "" && len(l.Errors) > 0 && string(l.Errors) != "null" {
			msg = clip(string(l.Errors), 500)
		}
		if msg == "" {
			msg = "no details"
		}
		sub := l.Subtype
		if sub == "" {
			sub = "error"
		}
		p.turnErr = fmt.Errorf("agent: claude turn failed (%s): %s", sub, msg)
	}
}

// send emits held-back text first so events stay in stream order.
func (p *parser) send(e Event) {
	p.flushPending()
	p.emit(e)
}

func (p *parser) flushPending() {
	pending := p.pending
	p.pending = nil
	for _, t := range pending {
		p.emit(Event{Kind: KindText, Text: t})
	}
}

// toolResultText returns the text of a tool_result's content, which is either
// a JSON string or a list of content blocks ({"type":"text","text":"…"}).
func toolResultText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var blocks []contentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func decodeFoReply(text string) (foReply, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "{") {
		return foReply{}, false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		return foReply{}, false
	}
	return foReplyFromMap(m)
}

func decodeStructured(raw json.RawMessage) (foReply, bool) {
	if len(raw) == 0 {
		return foReply{}, false
	}
	var v struct {
		StructuredContent map[string]any `json:"structuredContent"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.StructuredContent == nil {
		return foReply{}, false
	}
	return foReplyFromMap(v.StructuredContent)
}

func foReplyFromMap(m map[string]any) (foReply, bool) {
	str := func(k string) string { s, _ := m[k].(string); return s }
	r := foReply{
		Status:   strings.ToLower(strings.TrimSpace(str("status"))),
		Response: str("response"),
		Progress: str("progress"),
	}
	if n, ok := m["elapsedSeconds"].(float64); ok {
		r.ElapsedSeconds = n
	}
	return r, r.Status != ""
}

// shortToolName turns "mcp__<server>__<tool>" into "<tool>"; built-in names are returned as is.
func shortToolName(name string) string {
	_, tool := splitToolName(name)
	return tool
}

func splitToolName(name string) (server, tool string) {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return "", name
	}
	server, tool, ok = strings.Cut(rest, "__")
	if !ok {
		return "", name
	}
	return server, tool
}

// checkFoConnected logs a warning when the init event shows no connected Fo
// server ("fo" or the claude.ai connector "claude.ai fo"); Claude will then
// answer without Fo, typically saying it is not connected.
func checkFoConnected(raw json.RawMessage) {
	var servers []struct{ Name, Status string }
	if len(raw) == 0 || json.Unmarshal(raw, &servers) != nil {
		return
	}
	var seen []string
	for _, s := range servers {
		if s.Name == "fo" || strings.HasSuffix(s.Name, " fo") {
			if s.Status == "connected" {
				return
			}
			seen = append(seen, s.Name+"="+s.Status)
		}
	}
	slog.Warn("agent: no connected Fo MCP server at startup", "fo_servers", seen)
}

// isFoStatusTool reports whether a tool's result carries Fo's status object.
func isFoStatusTool(tool string) bool {
	return tool == "ask_fo" || tool == "get_fo_reply"
}

func describeToolCall(name string, input json.RawMessage) string {
	server, tool := splitToolName(name)
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	str := func(k string) string { s, _ := in[k].(string); return s }

	switch tool {
	case "ask_fo":
		if m := str("message"); m != "" {
			return "Asking Fo: " + clip(m, 120)
		}
		return "Asking Fo"
	case "get_fo_reply":
		return "Waiting for Fo's reply"
	case "list_fo_conversations":
		return "Listing Fo conversations"
	case "get_fo_conversation":
		return "Reading an earlier Fo conversation"
	case "start_fo_call":
		return "Starting a Fo call"
	case "ToolSearch":
		return "Loading tools"
	}
	label := "Using " + tool
	if server != "" {
		label = "Using " + server + " " + tool
	}
	for _, k := range []string{"description", "query", "message", "prompt", "url", "command", "file_path"} {
		if s := str(k); s != "" {
			return label + ": " + clip(s, 80)
		}
	}
	return label
}

// clip collapses whitespace and truncates to at most n runes.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
