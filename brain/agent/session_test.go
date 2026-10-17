package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// a fake claude that answers each stdin line like stream-json mode does, with a running cost total
const fakeSessionClaude = `echo started >> "$FAKE_DIR/starts"; printf '%s\n' "$@" > "$FAKE_DIR/args"; n=0
while IFS= read -r line; do
  n=$((n+1))
  echo '{"type":"system","subtype":"init","session_id":"s-1","mcp_servers":[{"name":"fo","status":"connected"}]}'
  echo '{"type":"assistant","message":{"content":[{"type":"text","text":"reply '$n'"}]}}'
  echo '{"type":"result","subtype":"success","is_error":false,"result":"reply '$n'","session_id":"s-1","duration_ms":10,"total_cost_usd":0.0'$n'}'
done`

func TestSessionKeepsOneProcess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAKE_DIR", dir)
	s, err := StartSession(Options{ClaudePath: fakeClaude(t, fakeSessionClaude), WorkDir: t.TempDir()}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i, want := range []string{"reply 1", "reply 2"} {
		res, err := s.Turn(context.Background(), "hi", nil)
		if err != nil {
			t.Fatalf("turn %d: %v", i+1, err)
		}
		if res.Reply != want || res.SessionID != "s-1" {
			t.Errorf("turn %d = %+v, want reply %q in s-1", i+1, res, want)
		}
		if d := res.CostUSD - 0.01; d < -1e-9 || d > 1e-9 {
			t.Errorf("turn %d cost %v, want this turn's 0.01 of the running total", i+1, res.CostUSD)
		}
	}
	if s.ID() != "s-1" {
		t.Errorf("ID = %q", s.ID())
	}
	starts, _ := os.ReadFile(filepath.Join(dir, "starts"))
	if n := strings.Count(string(starts), "started"); n != 1 {
		t.Errorf("claude started %d times, want once", n)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if !strings.Contains(string(args), "--input-format=stream-json") {
		t.Errorf("args %q lack --input-format=stream-json", args)
	}
}

func TestSessionTimeoutClosesIt(t *testing.T) {
	t.Setenv("FAKE_DIR", t.TempDir())
	defer func(w time.Duration) { interruptWait = w }(interruptWait)
	interruptWait = 200 * time.Millisecond // this fake ignores the interrupt
	bin := fakeClaude(t, `read -r line; sleep 30`)
	s, err := StartSession(Options{ClaudePath: bin, WorkDir: t.TempDir(), Timeout: 300 * time.Millisecond}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := time.Now()
	if _, err := s.Turn(context.Background(), "hi", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("timeout took %s", time.Since(start))
	}
	if s.Alive() {
		t.Error("session still alive after a timed-out turn")
	}
	if _, err := s.Turn(context.Background(), "again", nil); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("next turn err = %v, want ErrSessionClosed", err)
	}
}

func TestSessionExitMidTurn(t *testing.T) {
	t.Setenv("FAKE_DIR", t.TempDir())
	bin := fakeClaude(t, `read -r line; echo "Failed to authenticate" >&2; exit 1`)
	s, err := StartSession(Options{ClaudePath: bin, WorkDir: t.TempDir()}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Turn(context.Background(), "hi", nil)
	if !errors.Is(err, ErrNoResult) || !strings.Contains(err.Error(), "Failed to authenticate") {
		t.Fatalf("err = %v, want ErrNoResult with stderr", err)
	}
	if s.Alive() {
		t.Error("session alive after claude exited")
	}
}

func TestSessionResumeID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAKE_DIR", dir)
	s, err := StartSession(Options{ClaudePath: fakeClaude(t, fakeSessionClaude), WorkDir: t.TempDir()}, "old-7")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ID() != "old-7" {
		t.Errorf("ID before the first turn = %q, want the resume id", s.ID())
	}
	s.Turn(context.Background(), "hi", nil)
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if !strings.Contains(string(args), "--resume=old-7") {
		t.Errorf("args %q lack --resume=old-7", args)
	}
}

// A long turn (a Fo errand) must not block ID or Idle: the brain's spare
// recycler reads them, and shutdown waits on the recycler's lock.
func TestSessionMetaDuringATurn(t *testing.T) {
	t.Setenv("FAKE_DIR", t.TempDir())
	defer func(w time.Duration) { interruptWait = w }(interruptWait)
	interruptWait = 200 * time.Millisecond
	s, err := StartSession(Options{ClaudePath: fakeClaude(t, `read -r line; sleep 30`), WorkDir: t.TempDir(), Timeout: 5 * time.Second}, "s-9")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	go s.Turn(context.Background(), "a long errand", nil)
	time.Sleep(200 * time.Millisecond) // the turn is now waiting on Claude
	done := make(chan struct{})
	go func() {
		_ = s.ID()
		_ = s.Idle()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ID/Idle blocked behind a running turn")
	}
}

// a fake that stops a turn on an interrupt request, like stream-json mode does
const fakeInterruptibleClaude = `while IFS= read -r line; do
  case "$line" in
    *control_request*)
      echo '{"type":"control_response","response":{"subtype":"success","request_id":"stop-1"}}'
      echo '{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"s-1","total_cost_usd":0.02}';;
    *"long errand"*)
      echo '{"type":"system","subtype":"init","session_id":"s-1"}'
      echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"mcp__fo__ask_fo","input":{"message":"browse"}}]}}';;
    *)
      echo '{"type":"result","subtype":"success","is_error":false,"result":"still here","session_id":"s-1","total_cost_usd":0.03}';;
  esac
done`

func TestSessionStopKeepsTheConversation(t *testing.T) {
	t.Setenv("FAKE_DIR", t.TempDir())
	s, err := StartSession(Options{ClaudePath: fakeClaude(t, fakeInterruptibleClaude), WorkDir: t.TempDir()}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, stop := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); stop() }() // a tap on the device
	res, err := s.Turn(ctx, "a long errand", nil)
	if !errors.Is(err, context.Canceled) || res.SessionID != "s-1" {
		t.Fatalf("stopped turn = %+v, %v", res, err)
	}
	if !s.Alive() || s.ID() != "s-1" {
		t.Fatal("the session should survive a stop")
	}
	res, err = s.Turn(context.Background(), "and then?", nil)
	if err != nil || res.Reply != "still here" {
		t.Fatalf("next turn = %+v, %v", res, err)
	}
	if d := res.CostUSD - 0.01; d < -1e-9 || d > 1e-9 {
		t.Errorf("next turn cost %v, want 0.01 (the stopped turn's 0.02 counted already)", res.CostUSD)
	}
}
