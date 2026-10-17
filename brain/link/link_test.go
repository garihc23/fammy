package link

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"testing/iotest"
	"time"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	frames := []Frame{
		{TypeHello, []byte(`{"device":"femi","fw":"0.1"}`)},
		{TypeAudioChunk, bytes.Repeat([]byte{1, 2, 3}, 5000)},
		{TypeAudioEnd, []byte(`{"reason":"silence"}`)},
		{TypePing, []byte{}},
	}
	for _, f := range frames {
		if err := WriteFrame(&buf, f.Type, f.Payload); err != nil {
			t.Fatal(err)
		}
	}
	// One byte at a time: every read is a split read.
	r := iotest.OneByteReader(bytes.NewReader(buf.Bytes()))
	for i, want := range frames {
		got, err := ReadFrame(r)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if got.Type != want.Type || !bytes.Equal(got.Payload, want.Payload) {
			t.Fatalf("frame %d: got type %x len %d, want type %x len %d", i, got.Type, len(got.Payload), want.Type, len(want.Payload))
		}
	}
	if _, err := ReadFrame(r); err != io.EOF {
		t.Fatalf("after last frame: got %v, want io.EOF", err)
	}
}

func TestFrameHeaderLayout(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, TypeState, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x81, 3, 0, 0, 0, 'a', 'b', 'c'}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got % x, want % x", buf.Bytes(), want)
	}
}

func TestFrameErrors(t *testing.T) {
	hdr := make([]byte, HeaderSize)
	hdr[0] = TypeAudioChunk
	binary.LittleEndian.PutUint32(hdr[1:], MaxPayload+1)
	if _, err := ReadFrame(bytes.NewReader(hdr)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized: got %v", err)
	}
	binary.LittleEndian.PutUint32(hdr[1:], 10)
	if _, err := ReadFrame(bytes.NewReader(append(hdr, 1, 2, 3))); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated payload: got %v", err)
	}
	if _, err := ReadFrame(bytes.NewReader(hdr[:3])); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated header: got %v", err)
	}
	if err := WriteFrame(io.Discard, TypeSayChunk, make([]byte, MaxPayload+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("write oversized: got %v", err)
	}
}

type recHandler struct {
	mu     sync.Mutex
	events []string
	frames chan Frame
}

func (h *recHandler) add(s string) {
	h.mu.Lock()
	h.events = append(h.events, s)
	h.mu.Unlock()
}
func (h *recHandler) DeviceConnected(string) { h.add("connected") }
func (h *recHandler) DeviceFrame(f Frame)    { h.frames <- f }
func (h *recHandler) DeviceDisconnected()    { h.add("disconnected") }

func startServer(t *testing.T, h Handler) (*Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Handler: h, PingInterval: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Serve(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, ln.Addr().String()
}

func waitConnected(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !s.Connected() {
		if time.Now().After(deadline) {
			t.Fatal("device never connected")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSayChunking(t *testing.T) {
	s, addr := startServer(t, &recHandler{frames: make(chan Frame, 10)})
	if err := s.Say(context.Background(), "s0", "x", nil, false); err != ErrNotConnected {
		t.Fatalf("say with no device: got %v", err)
	}
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	waitConnected(t, s)

	pcm := make([]byte, 2*ChunkSize+1808)
	for i := range pcm {
		pcm[i] = byte(i)
	}
	errc := make(chan error, 1)
	go func() { errc <- s.Say(context.Background(), "s1", "Hello there", pcm, true) }()

	br := bufio.NewReader(nc)
	f, err := ReadFrame(br)
	if err != nil || f.Type != TypeSayStart {
		t.Fatalf("first frame: %v %v", f.Type, err)
	}
	var start struct {
		ID    string `json:"id"`
		Text  string `json:"text"`
		Bytes int    `json:"bytes"`
	}
	if err := json.Unmarshal(f.Payload, &start); err != nil {
		t.Fatal(err)
	}
	if start.ID != "s1" || start.Text != "Hello there" || start.Bytes != len(pcm) {
		t.Fatalf("SAY_START = %+v", start)
	}
	var got []byte
	var sizes []int
	for {
		f, err := ReadFrame(br)
		if err != nil {
			t.Fatal(err)
		}
		if f.Type == TypeSayEnd {
			if string(f.Payload) != `{"id":"s1"}` {
				t.Fatalf("SAY_END payload %s", f.Payload)
			}
			break
		}
		if f.Type != TypeSayChunk {
			t.Fatalf("unexpected frame %s", TypeName(f.Type))
		}
		sizes = append(sizes, len(f.Payload))
		got = append(got, f.Payload...)
	}
	if !bytes.Equal(got, pcm) {
		t.Fatal("audio mismatch")
	}
	if len(sizes) != 3 || sizes[0] != ChunkSize || sizes[1] != ChunkSize || sizes[2] != 1808 {
		t.Fatalf("chunk sizes %v", sizes)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestImageAndControlFrames(t *testing.T) {
	h := &recHandler{frames: make(chan Frame, 10)}
	s, addr := startServer(t, h)
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	waitConnected(t, s)

	go func() {
		s.State("thinking", "Asking Fo…")
		s.Image("Demo", 4, 3, make([]byte, 4*3*2))
		s.Command(map[string]any{"type": "timer", "minutes": 5})
		s.Snapshot("", 2, 2, make([]byte, 8))
	}()
	br := bufio.NewReader(nc)
	expect := []byte{TypeState, TypeCommand, TypeImageChunk, TypeCommand, TypeCommand, TypeImageChunk}
	for i, want := range expect {
		f, err := ReadFrame(br)
		if err != nil {
			t.Fatal(err)
		}
		if f.Type != want {
			t.Fatalf("frame %d: got %s want %s", i, TypeName(f.Type), TypeName(want))
		}
		if i == 0 && string(f.Payload) != `{"face":"thinking","line":"Asking Fo…"}` {
			t.Fatalf("state payload %s", f.Payload)
		}
		if i == 1 && string(f.Payload) != `{"type":"image","w":4,"h":3,"bytes":24,"caption":"Demo"}` {
			t.Fatalf("image payload %s", f.Payload)
		}
		if i == 4 && string(f.Payload) != `{"type":"image","kind":"snap","w":2,"h":2,"bytes":8,"caption":""}` {
			t.Fatalf("snapshot payload %s", f.Payload)
		}
	}

	// Device → brain frames reach the handler.
	WriteFrame(nc, TypeEvent, []byte(`{"type":"text","text":"hi"}`))
	select {
	case f := <-h.frames:
		if f.Type != TypeEvent || string(f.Payload) != `{"type":"text","text":"hi"}` {
			t.Fatalf("handler got %s %s", TypeName(f.Type), f.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler got nothing")
	}
}

func TestNewConnectionReplacesOld(t *testing.T) {
	h := &recHandler{frames: make(chan Frame, 10)}
	s, addr := startServer(t, h)
	first, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	waitConnected(t, s)
	second, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	// The old connection gets closed by the server.
	first.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := ReadFrame(first); err != io.EOF {
		t.Fatalf("old connection: got %v, want EOF", err)
	}
	// Writes now go to the new one.
	go s.State("idle", "")
	second.SetReadDeadline(time.Now().Add(2 * time.Second))
	f, err := ReadFrame(second)
	if err != nil || f.Type != TypeState {
		t.Fatalf("new connection: %v %v", f.Type, err)
	}
	var ev []string
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		h.mu.Lock()
		ev = append([]string(nil), h.events...)
		h.mu.Unlock()
		if len(ev) >= 3 {
			break
		}
	}
	if len(ev) != 3 || ev[0] != "connected" || ev[1] != "disconnected" || ev[2] != "connected" {
		t.Fatalf("handler events %v", ev)
	}

	// Device hangs up: writers report not connected.
	second.Close()
	deadline := time.Now().Add(2 * time.Second)
	for s.Connected() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := s.State("idle", ""); err != ErrNotConnected {
		t.Fatalf("after disconnect: got %v", err)
	}
}

func TestPing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{PingInterval: 20 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Serve(ctx, ln)
	nc, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	nc.SetReadDeadline(time.Now().Add(2 * time.Second))
	f, err := ReadFrame(nc)
	if err != nil || f.Type != TypePing || string(f.Payload) != "{}" {
		t.Fatalf("ping: %v %q %v", f.Type, f.Payload, err)
	}
}
