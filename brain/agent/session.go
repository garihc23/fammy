package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// ErrSessionClosed is returned by Turn once the session's process has exited.
var ErrSessionClosed = errors.New("agent: session closed")

// Session keeps one Claude Code process running across turns
// (`claude -p --input-format stream-json`). Claude starts, loads its MCP
// servers and connects to Fo once; after that a turn is one message on stdin,
// so it costs only Claude's answer (~1.5 s) instead of a fresh start (~5 s).
// Turns run one at a time, and the conversation carries on from turn to turn.
type Session struct {
	opts    Options
	cmd     *exec.Cmd
	end     context.CancelFunc // kills the process (its whole process group on unix)
	stdin   io.WriteCloser
	lines   chan []byte   // stdout, one stream-json object per line; closed when the process has exited
	exited  chan struct{} // closed when the process has exited
	stderr  *tailBuffer
	started time.Time
	closed  atomic.Bool // set the moment it is killed or closed, before the process is gone
	drain   sync.Once

	mu       sync.Mutex // one turn at a time (held for the whole turn)
	lastCost float64    // the result's total_cost_usd is a running total for the process

	meta     sync.Mutex // id and lastUsed: readable while a turn runs, so a long turn never blocks ID or Idle
	id       string     // Claude's session id, known after the first turn
	lastUsed time.Time
}

// StartSession starts Claude now, so its MCP servers are connected by the time
// the first turn arrives. resumeID "" starts a new conversation; otherwise it
// continues that one (--resume).
func StartSession(opts Options, resumeID string) (*Session, error) {
	workDir, err := resolveWorkDir(opts.WorkDir)
	if err != nil {
		return nil, err
	}
	bin := opts.ClaudePath
	if bin == "" {
		bin = "claude"
	}
	life, end := context.WithCancel(context.Background()) // the process lives until kill ends it
	cmd := exec.CommandContext(life, bin, append(buildArgs(opts, resumeID), "--input-format=stream-json")...)
	cmd.Dir = workDir
	cmd.Env = append(childEnv(os.Environ()), opts.Env...)
	s := &Session{
		opts:    opts,
		cmd:     cmd,
		end:     end,
		lines:   make(chan []byte, 256),
		exited:  make(chan struct{}),
		stderr:  &tailBuffer{max: 16 << 10},
		started: time.Now(),
		id:      resumeID,
	}
	cmd.Stderr = s.stderr
	configureProcess(cmd)
	if s.stdin, err = cmd.StdinPipe(); err != nil {
		end()
		return nil, fmt.Errorf("agent: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		end()
		return nil, fmt.Errorf("agent: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		end()
		return nil, fmt.Errorf("agent: start %s: %w", bin, err)
	}
	s.lastUsed = s.started
	go func() {
		br := bufio.NewReaderSize(stdout, 64<<10)
		for {
			line, err := br.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				s.lines <- line
			}
			if err != nil {
				break
			}
		}
		_ = cmd.Wait() // only after every read from the pipe
		end()
		close(s.exited)
		close(s.lines)
	}()
	return s, nil
}

// ID is Claude's session id: the resume id it was started with, or the id
// Claude reported on the first turn. "" before then.
func (s *Session) ID() string {
	s.meta.Lock()
	defer s.meta.Unlock()
	return s.id
}

// Alive reports whether the session can still take turns.
func (s *Session) Alive() bool {
	if s.closed.Load() {
		return false
	}
	select {
	case <-s.exited:
		return false
	default:
		return true
	}
}

// Age is how long the process has been running; Idle how long since its last turn
// ended (or since it started).
func (s *Session) Age() time.Duration { return time.Since(s.started) }

func (s *Session) Idle() time.Duration {
	s.meta.Lock()
	defer s.meta.Unlock()
	return time.Since(s.lastUsed)
}

// Turn sends one message and waits for Claude's reply, streaming events as in
// Run. A turn that times out or is cancelled kills the process: the session is
// then closed and the caller should start a new one.
func (s *Session) Turn(ctx context.Context, text string, onEvent func(Event)) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.ID()
	if !s.Alive() {
		return Result{SessionID: id}, fmt.Errorf("%w%s", ErrSessionClosed, s.stderr.suffix())
	}
	timeout := s.opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	msg, _ := json.Marshal(map[string]any{
		"type":    "user",
		"message": map[string]any{"role": "user", "content": text},
	})
	start := time.Now()
	defer func() {
		s.meta.Lock()
		s.lastUsed = time.Now()
		s.meta.Unlock()
	}()
	if _, err := s.stdin.Write(append(msg, '\n')); err != nil {
		s.kill()
		return Result{SessionID: id}, fmt.Errorf("agent: send turn: %w%s", err, s.stderr.suffix())
	}

	p := newParser(onEvent)
	for !p.gotResult {
		select {
		case line, ok := <-s.lines:
			if !ok {
				p.flushPending()
				return p.res, fmt.Errorf("agent: claude exited mid-turn: %w%s", ErrNoResult, s.stderr.suffix())
			}
			p.handleLine(line)
		case <-ctx.Done():
			s.kill()
			p.flushPending()
			what := "canceled"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				what = fmt.Sprintf("timed out after %s", timeout)
			}
			return p.res, fmt.Errorf("agent: turn %s: %w%s", what, ctx.Err(), s.stderr.suffix())
		}
	}
	res := p.res
	res.Duration = time.Since(start)
	if total := res.CostUSD; total >= s.lastCost {
		res.CostUSD = total - s.lastCost
		s.lastCost = total
	}
	if res.SessionID != "" {
		s.meta.Lock()
		s.id = res.SessionID
		s.meta.Unlock()
	}
	return res, p.turnErr
}

// Close ends the process: politely (end of input) and then by force.
func (s *Session) Close() {
	s.closed.Store(true)
	s.discardOutput()
	_ = s.stdin.Close()
	select {
	case <-s.exited:
	case <-time.After(3 * time.Second):
		s.kill()
	}
}

func (s *Session) kill() {
	s.closed.Store(true)
	s.discardOutput()
	s.end()
}

// discardOutput reads whatever the process still prints, so the reader never
// blocks on a full channel and the process can be waited for.
func (s *Session) discardOutput() {
	s.drain.Do(func() {
		go func() {
			for range s.lines {
			}
		}()
	})
}
