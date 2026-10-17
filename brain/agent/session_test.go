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
