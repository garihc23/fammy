package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func parseFile(t *testing.T, name string) ([]Event, Result, error) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var events []Event
	res, err := ParseStream(f, func(e Event) { events = append(events, e) })
	return events, res, err
}

func kinds(events []Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

func TestParsePlain(t *testing.T) {
	events, res, err := parseFile(t, "plain.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	// The only assistant text is the final answer, which must not be echoed as an event.
	// The capture also contains hook, init, rate-limit and model-fallback system events.
	if len(events) != 0 {
		t.Errorf("events = %+v, want none", events)
	}
	want := Result{
		Reply:     "Paris is the capital of France.",
		SessionID: "95f59f33-bf6e-4626-bdc0-97f8e0a0ff01",
		Duration:  3903 * time.Millisecond,
		CostUSD:   0.3335043,
	}
	if res != want {
		t.Errorf("result = %+v, want %+v", res, want)
	}
}

func TestParseFoWeather(t *testing.T) {
	events, res, err := parseFile(t, "fo-weather.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	want := []Event{
		{Kind: KindToolCall, Tool: "ToolSearch", Text: "Loading tools"},
		{Kind: KindToolCall, Tool: "ask_fo", Text: "Asking Fo: What is the weather in Mumbai today?"},
		{Kind: KindFoDone, Tool: "ask_fo", Status: "complete",
			Text: "Sunny in Mumbai right now, 32°C but it feels like 41 with the humidity. Only a small chance of rain, though thunderstorms are possible later."},
	}
	if !slices.Equal(events, want) {
		t.Errorf("events:\n got %+v\nwant %+v", events, want)
	}
	if res.Reply != "Fo says Mumbai is sunny at 32°C (feels like 41°C), with low rain chance but possible thunderstorms later." {
		t.Errorf("reply = %q", res.Reply)
	}
	if res.SessionID != "bf5c7502-fdde-4f94-9a71-712da914465c" {
		t.Errorf("session = %q", res.SessionID)
	}
	if res.Duration != 19627*time.Millisecond || res.CostUSD != 0.0343356 {
		t.Errorf("duration/cost = %v / %v", res.Duration, res.CostUSD)
	}
}

func TestParseFoResearch(t *testing.T) {
	// A long Fo turn: ask_fo heartbeats at 30 s, returns "working" at 40 s,
	// then Claude polls get_fo_reply five times until "complete".
	events, res, err := parseFile(t, "fo-research.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := []string{
		KindToolCall, KindToolCall, // ToolSearch, ask_fo
		KindFoProgress, KindFoProgress, // heartbeat, ask_fo "working"
		KindText, KindToolCall, KindFoProgress,
		KindText, KindToolCall, KindFoProgress,
		KindText, KindToolCall, KindFoProgress,
		KindText, KindToolCall, KindFoProgress,
		KindToolCall, KindFoDone,
	}
	if got := kinds(events); !slices.Equal(got, wantKinds) {
		t.Fatalf("kinds:\n got %v\nwant %v", got, wantKinds)
	}

	if e := events[2]; e.Tool != "ask_fo" || e.Status != "working" || e.Text != "Still waiting on Fo (30s)" {
		t.Errorf("heartbeat event = %+v", e)
	}
	if e := events[3]; e.Tool != "ask_fo" || !strings.HasPrefix(e.Text, "Working for 40s: researching family-friendly restaurants in Bengaluru") {
		t.Errorf("first progress event = %+v", e)
	}
	if e := events[6]; e.Tool != "get_fo_reply" || e.Text != "Working for 55s: researching (Comparing Bengaluru restaurant prices), 17 steps so far" {
		t.Errorf("get_fo_reply progress event = %+v", e)
	}
	if e := events[4]; e.Text != "Fo researching now, comparing sources and prices." {
		t.Errorf("first text event = %+v", e)
	}
	if e := events[5]; e.Tool != "get_fo_reply" || e.Text != "Waiting for Fo's reply" {
		t.Errorf("poll tool_call = %+v", e)
	}
	done := events[len(events)-1]
	if done.Tool != "get_fo_reply" || done.Status != "complete" || !strings.HasPrefix(done.Text, "Done, and nothing was booked or contacted.") {
		t.Errorf("fo_done = %+v", done)
	}
	for _, e := range events {
		if e.Kind == KindText && strings.Contains(e.Text, "Karavalli") {
			t.Errorf("final reply leaked as a text event: %q", e.Text)
		}
	}
	if !strings.HasPrefix(res.Reply, "Fo's top pick is Karavalli") {
		t.Errorf("reply = %q", res.Reply)
	}
	if res.SessionID != "51492147-137d-4b1b-ace2-82901f7b156b" || res.CostUSD <= 0 || res.Duration <= 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestParseRobustness(t *testing.T) {
	stream := strings.Join([]string{
		`not json at all`,
		`{"type":"brand_new_event","whatever":[1,2,3]}`,
		`{"type":"system","subtype":"init","session_id":"s-1"}`,
		`{"type":"user","message":{"role":"user","content":"a plain string prompt"}}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":""},{"type":"text","text":"Let me check with Fo."},{"type":"tool_use","id":"t1","name":"mcp__fo__ask_fo","input":{"message":"What's on my calendar today?"}}]},"parent_tool_use_id":null}`,
		// Content as a list of blocks holding JSON text.
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"{\"status\":\"working\",\"elapsedSeconds\":12,\"response\":\"poll please\"}"}]}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"mcp__fo__get_fo_reply","input":{}}]}}`,
		// Not JSON in content; fall back to structuredContent.
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"see structured"}]},"tool_use_result":{"structuredContent":{"status":"failed","response":"Calendar is not connected."}}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t3","name":"mcp__claude_ai_fo__ask_fo","input":{"message":"retry"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t3","is_error":true,"content":"Claude requested permissions to use mcp__claude_ai_fo__ask_fo, but you haven't granted it yet."}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t4","name":"WebSearch","input":{"query":"mumbai weather"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t4","content":"{\"status\":\"complete\"}"}]}}`, // non-Fo: ignored
		`{"type":"tool_progress","tool_name":"WebSearch","elapsed_time_seconds":30}`,                                              // non-Fo: ignored
		`{"type":"assistant","message":{"content":[{"type":"text","text":"subagent chatter"}]},"parent_tool_use_id":"t9"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Fo could not reach your calendar."}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"Fo could not reach your calendar.","session_id":"s-1","duration_ms":1500,"total_cost_usd":0.01}`,
		``,
	}, "\n")

	var events []Event
	res, err := ParseStream(strings.NewReader(stream), func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	want := []Event{
		{Kind: KindText, Text: "Let me check with Fo."},
		{Kind: KindToolCall, Tool: "ask_fo", Text: "Asking Fo: What's on my calendar today?"},
		{Kind: KindFoProgress, Tool: "ask_fo", Status: "working", Text: "Fo is working (12s)"},
		{Kind: KindToolCall, Tool: "get_fo_reply", Text: "Waiting for Fo's reply"},
		{Kind: KindFoDone, Tool: "get_fo_reply", Status: "failed", Text: "Calendar is not connected."},
		{Kind: KindToolCall, Tool: "ask_fo", Text: "Asking Fo: retry"},
		{Kind: KindFoDone, Tool: "ask_fo", Status: "failed", Text: "Claude requested permissions to use mcp__claude_ai_fo__ask_fo, but you haven't granted it yet."},
		{Kind: KindToolCall, Tool: "WebSearch", Text: "Using WebSearch: mumbai weather"},
	}
	if !slices.Equal(events, want) {
		t.Errorf("events:\n got %+v\nwant %+v", events, want)
	}
	if res != (Result{Reply: "Fo could not reach your calendar.", SessionID: "s-1", Duration: 1500 * time.Millisecond, CostUSD: 0.01}) {
		t.Errorf("result = %+v", res)
	}
}

func TestParseErrorResult(t *testing.T) {
	stream := `{"type":"system","subtype":"init","session_id":"s-2"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Working on it."}]}}
{"type":"result","subtype":"error_max_turns","is_error":true,"session_id":"s-2","duration_ms":10}`
	var events []Event
	res, err := ParseStream(strings.NewReader(stream), func(e Event) { events = append(events, e) })
	if err == nil || !strings.Contains(err.Error(), "error_max_turns") {
		t.Fatalf("err = %v, want error_max_turns", err)
	}
	if res.SessionID != "s-2" {
		t.Errorf("session = %q", res.SessionID)
	}
	if len(events) != 1 || events[0].Text != "Working on it." {
		t.Errorf("events = %+v", events)
	}
}

func TestParseNoResult(t *testing.T) {
	stream := `{"type":"system","subtype":"init","session_id":"s-3"}
{"type":"assistant","message":{"content":[{"type":"text","text":"partial"}]}}`
	var events []Event
	res, err := ParseStream(strings.NewReader(stream), func(e Event) { events = append(events, e) })
	if !errors.Is(err, ErrNoResult) {
		t.Fatalf("err = %v, want ErrNoResult", err)
	}
	if res.SessionID != "s-3" || len(events) != 1 || events[0].Text != "partial" {
		t.Errorf("res = %+v events = %+v", res, events)
	}
}

func TestChildEnv(t *testing.T) {
	in := []string{
		"HOME=/home/x", "PATH=/bin", "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=abc",
		"CLAUDE_PID=1", "ANTHROPIC_BASE_URL=http://proxy", "ANTHROPIC_API_KEY=sk-test", "NOT_CLAUDE=ok",
		"MCP_CONNECTION_NONBLOCKING=true",
	}
	got := childEnv(in)
	want := []string{"HOME=/home/x", "PATH=/bin", "ANTHROPIC_API_KEY=sk-test", "NOT_CLAUDE=ok", "ENABLE_TOOL_SEARCH=false"}
	if !slices.Equal(got, want) {
		t.Errorf("childEnv = %v, want %v", got, want)
	}
}

func TestBuildArgs(t *testing.T) {
	args := buildArgs(Options{
		SystemPrompt:      "-be brief",
		MCPConfigJSON:     `{"mcpServers":{}}`,
		ExtraAllowedTools: []string{"mcp__device__set_led"},
		Model:             "sonnet",
		ExtraArgs:         []string{"--setting-sources", "project,local"},
	}, "sess-1")
	want := []string{
		"-p", "--output-format=stream-json", "--verbose",
		"--allowedTools=" + strings.Join(append(append([]string{}, FoTools...), "mcp__device__set_led"), ","),
		`--mcp-config={"mcpServers":{}}`,
		"--append-system-prompt=-be brief",
		"--model=sonnet",
		"--resume=sess-1",
		"--setting-sources", "project,local",
	}
	if !slices.Equal(args, want) {
		t.Errorf("args:\n got %q\nwant %q", args, want)
	}
	if a := buildArgs(Options{}, ""); slices.ContainsFunc(a, func(s string) bool { return strings.HasPrefix(s, "--resume") }) {
		t.Errorf("new session must not pass --resume: %q", a)
	}
}

// fakeClaude writes a shell script standing in for the claude binary.
func fakeClaude(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake needs a unix shell")
	}
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunWithFakeClaude(t *testing.T) {
	dir := t.TempDir()
	stream, _ := filepath.Abs(filepath.Join("testdata", "fo-weather.jsonl"))
	t.Setenv("FAKE_DIR", dir)
	t.Setenv("FAKE_STREAM", stream)
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("ANTHROPIC_BASE_URL", "http://should-not-leak")
	bin := fakeClaude(t, `cat > "$FAKE_DIR/stdin"; printf '%s\n' "$@" > "$FAKE_DIR/args"; env > "$FAKE_DIR/env"; pwd > "$FAKE_DIR/pwd"; cat "$FAKE_STREAM"`)

	work := filepath.Join(dir, "work")
	var events []Event
	res, err := Run(context.Background(), Options{ClaudePath: bin, WorkDir: work}, "sess-9", "what's the weather?", func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "bf5c7502-fdde-4f94-9a71-712da914465c" || !strings.HasPrefix(res.Reply, "Fo says Mumbai") {
		t.Errorf("result = %+v", res)
	}
	if got := kinds(events); !slices.Equal(got, []string{KindToolCall, KindToolCall, KindFoDone}) {
		t.Errorf("kinds = %v", got)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read("stdin"); got != "what's the weather?" {
		t.Errorf("stdin = %q", got)
	}
	if args := read("args"); !strings.Contains(args, "--resume=sess-9\n") || !strings.Contains(args, "--output-format=stream-json\n") {
		t.Errorf("args = %q", args)
	}
	env := read("env")
	if strings.Contains(env, "CLAUDECODE=") || strings.Contains(env, "ANTHROPIC_BASE_URL=") {
		t.Errorf("leaked env: %s", env)
	}
	if pwd := strings.TrimSpace(read("pwd")); pwd != work {
		t.Errorf("pwd = %q, want %q", pwd, work)
	}
}

func TestRunFailureIncludesStderr(t *testing.T) {
	bin := fakeClaude(t, `echo "Failed to authenticate: OAuth session expired" >&2; exit 1`)
	_, err := Run(context.Background(), Options{ClaudePath: bin, WorkDir: t.TempDir()}, "", "hi", nil)
	if err == nil || !strings.Contains(err.Error(), "OAuth session expired") {
		t.Fatalf("err = %v, want stderr in error", err)
	}
}

func TestRunTimeoutKillsChild(t *testing.T) {
	bin := fakeClaude(t, `echo '{"type":"system","subtype":"init","session_id":"s-slow"}'; sleep 30; echo never`)
	start := time.Now()
	res, err := Run(context.Background(), Options{ClaudePath: bin, WorkDir: t.TempDir(), Timeout: 300 * time.Millisecond}, "", "hi", nil)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Errorf("Run took %v after timeout; child not killed", el)
	}
	if res.SessionID != "s-slow" {
		t.Errorf("partial session = %q", res.SessionID)
	}
}
