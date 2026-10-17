// Package agent runs one Claude Code turn headless (`claude -p`), streams
// progress events while it works (tool calls, Fo progress), and returns the
// final reply.
package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Event kinds.
const (
	KindToolCall   = "tool_call"   // Claude called a tool; Text describes what is being done
	KindFoProgress = "fo_progress" // Fo is still working; Text is Fo's progress line
	KindFoDone     = "fo_done"     // a Fo call settled; Text is Fo's response, Status says how
	KindText       = "text"        // interim assistant narration (the final answer is Result.Reply)
)

// DefaultTimeout bounds a whole turn when Options.Timeout is zero.
const DefaultTimeout = 3 * time.Minute

// FoTools are the read-side Fo tools allowed in every turn. Fo is reachable
// both as the user-scope MCP server "fo" and as the claude.ai connector
// "claude.ai fo" (tool prefix mcp__claude_ai_fo__); whichever is connected
// and authenticated in the headless child gets used, so both are allowed.
// start_fo_call is left out: it returns a browser link, useless on a voice device.
var FoTools = []string{
	"mcp__fo__ask_fo",
	"mcp__fo__get_fo_reply",
	"mcp__fo__list_fo_conversations",
	"mcp__fo__get_fo_conversation",
	"mcp__claude_ai_fo__ask_fo",
	"mcp__claude_ai_fo__get_fo_reply",
	"mcp__claude_ai_fo__list_fo_conversations",
	"mcp__claude_ai_fo__get_fo_conversation",
}

// Options configures one turn.
type Options struct {
	SystemPrompt      string   // appended to Claude Code's system prompt (--append-system-prompt)
	MCPConfigJSON     string   // optional extra MCP servers (--mcp-config), e.g. the device's own tool server
	ExtraAllowedTools []string // allowed in addition to FoTools
	Model             string   // optional (--model)
	WorkDir           string   // cwd for the child; default <tmp>/femi-agent. Keep it stable: --resume finds sessions per cwd.
	Timeout           time.Duration
	ClaudePath        string   // claude binary; default "claude" from PATH
	ExtraArgs         []string // appended verbatim, e.g. {"--setting-sources", "project,local"}
}

// Event is streamed to the caller while the turn runs.
type Event struct {
	Kind   string // KindToolCall | KindFoProgress | KindFoDone | KindText
	Tool   string // short tool name when relevant, e.g. "ask_fo"
	Text   string // short human-readable line
	Status string // fo_progress/fo_done: Fo's status ("working", "complete", "failed", …)
}

// Result of a finished turn.
type Result struct {
	Reply     string
	SessionID string // pass back to Run to continue the conversation
	Duration  time.Duration
	CostUSD   float64 // if reported
}

// Run runs one Claude turn. sessionID "" starts a new conversation; otherwise
// the conversation is continued with --resume. onEvent (may be nil) is called
// synchronously, in stream order, from Run's goroutine; it should not block.
//
// The child is killed when ctx is done or the timeout expires. On failure the
// returned Result still carries whatever was seen (e.g. SessionID).
func Run(ctx context.Context, opts Options, sessionID string, userText string, onEvent func(Event)) (Result, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	workDir, err := resolveWorkDir(opts.WorkDir)
	if err != nil {
		return Result{}, err
	}
	bin := opts.ClaudePath
	if bin == "" {
		bin = "claude"
	}

	cmd := exec.CommandContext(ctx, bin, buildArgs(opts, sessionID)...)
	cmd.Dir = workDir
	cmd.Env = childEnv(os.Environ())
	cmd.Stdin = strings.NewReader(userText) // prompt via stdin: --allowedTools/--mcp-config are variadic and would swallow a positional prompt
	stderr := &tailBuffer{max: 16 << 10}
	cmd.Stderr = stderr
	configureProcess(cmd)
	cmd.WaitDelay = 5 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("agent: stdout pipe: %w", err)
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("agent: start %s: %w", bin, err)
	}

	res, perr := ParseStream(stdout, onEvent)
	werr := cmd.Wait()
	if res.Duration == 0 {
		res.Duration = time.Since(start)
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		what := "canceled"
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			what = fmt.Sprintf("timed out after %s", timeout)
		}
		return res, fmt.Errorf("agent: turn %s: %w%s", what, ctxErr, stderr.suffix())
	}
	if werr != nil {
		if perr != nil && !errors.Is(perr, ErrNoResult) {
			return res, fmt.Errorf("%w (claude exited: %v)%s", perr, werr, stderr.suffix())
		}
		return res, fmt.Errorf("agent: claude exited: %w%s", werr, stderr.suffix())
	}
	if perr != nil {
		return res, fmt.Errorf("%w%s", perr, stderr.suffix())
	}
	return res, nil
}

func buildArgs(opts Options, sessionID string) []string {
	// "--flag=value" form throughout so values starting with "-" are safe and
	// variadic flags cannot swallow anything that follows.
	args := []string{"-p", "--output-format=stream-json", "--verbose"}
	allowed := append(append([]string{}, FoTools...), opts.ExtraAllowedTools...)
	args = append(args, "--allowedTools="+strings.Join(allowed, ","))
	if opts.MCPConfigJSON != "" {
		args = append(args, "--mcp-config="+opts.MCPConfigJSON)
	}
	if opts.SystemPrompt != "" {
		args = append(args, "--append-system-prompt="+opts.SystemPrompt)
	}
	if opts.Model != "" {
		args = append(args, "--model="+opts.Model)
	}
	if sessionID != "" {
		args = append(args, "--resume="+sessionID)
	}
	return append(args, opts.ExtraArgs...)
}

// childEnv drops variables inherited from a surrounding Claude desktop/Code
// session (CLAUDE*, CLAUDECODE, ANTHROPIC_BASE_URL); passing them makes the
// child fail with "OAuth session expired". MCP_CONNECTION_NONBLOCKING is dropped
// too, so a turn never starts before Fo has connected. ANTHROPIC_API_KEY is kept.
func childEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "CLAUDE") || k == "ANTHROPIC_BASE_URL" || k == "MCP_CONNECTION_NONBLOCKING" {
			continue
		}
		out = append(out, kv)
	}
	// load MCP tool schemas up front instead of through a ToolSearch step: saves 2-5 s
	// per turn and stops Claude guessing argument names before it has seen the schema
	return append(out, "ENABLE_TOOL_SEARCH=false")
}

func resolveWorkDir(dir string) (string, error) {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "femi-agent")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("agent: work dir: %w", err)
	}
	return dir, nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) suffix() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := strings.TrimSpace(string(t.buf))
	if s == "" {
		return ""
	}
	return "; stderr: " + s
}
