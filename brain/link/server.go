package link

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"
)

// ErrNotConnected is returned by the writers when no device is connected.
var ErrNotConnected = errors.New("link: no device connected")

// Handler receives what the device sends. Calls come from the connection's
// read loop, one at a time and in order: DeviceConnected, then DeviceFrame
// for every frame, then DeviceDisconnected. A replaced connection is fully
// disconnected before the new one is announced. Implementations must not
// block: start goroutines for slow work, or no further frames (approvals,
// acks) are read.
type Handler interface {
	DeviceConnected(remote string)
	DeviceFrame(f Frame)
	DeviceDisconnected()
}

// Server accepts one device at a time; a new connection replaces the old one.
// All writer methods are safe for concurrent use. Multi-frame messages (a SAY,
// an IMAGE) are written atomically, so frames of different messages never
// interleave. TCP back-pressure is the flow control: Say returns roughly when
// the device has buffered the last chunk.
type Server struct {
	Handler      Handler
	Logf         func(format string, args ...any) // optional
	PingInterval time.Duration                    // default 10 s
	WriteTimeout time.Duration                    // per frame; default 20 s

	mu  sync.Mutex
	cur *conn
}

type conn struct {
	nc        net.Conn
	remote    string
	wmu       sync.Mutex // held for a whole multi-frame message
	timeout   time.Duration
	done      chan struct{} // closed by close()
	finished  chan struct{} // closed when the read loop has returned and the handler was told
	closeOnce sync.Once
}

func (c *conn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.nc.Close()
	})
}

// frame writes one frame; any write error breaks the connection (a partial
// frame would desync the stream), so the device reconnects cleanly.
func (c *conn) frame(typ byte, payload []byte) error {
	select {
	case <-c.done:
		return ErrNotConnected
	default:
	}
	_ = c.nc.SetWriteDeadline(time.Now().Add(c.timeout))
	if err := WriteFrame(c.nc, typ, payload); err != nil {
		c.close()
		return err
	}
	return nil
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// ListenAndServe listens on addr (e.g. ":8787") and serves until ctx is done.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve accepts devices on ln until ctx is done.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	stop := context.AfterFunc(ctx, func() {
		ln.Close()
		s.mu.Lock()
		c := s.cur
		s.mu.Unlock()
		if c != nil {
			c.close()
		}
	})
	defer stop()

	for {
		nc, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return err
		}
		if tc, ok := nc.(*net.TCPConn); ok {
			_ = tc.SetNoDelay(true)
			_ = tc.SetKeepAlive(true)
			_ = tc.SetKeepAlivePeriod(15 * time.Second)
		}
		timeout := s.WriteTimeout
		if timeout <= 0 {
			timeout = 20 * time.Second
		}
		c := &conn{
			nc:       nc,
			remote:   nc.RemoteAddr().String(),
			timeout:  timeout,
			done:     make(chan struct{}),
			finished: make(chan struct{}),
		}
		s.mu.Lock()
		old := s.cur
		s.cur = c
		s.mu.Unlock()
		if old != nil {
			s.logf("link: %s replaces %s", c.remote, old.remote)
			old.close()
			select {
			case <-old.finished:
			case <-time.After(2 * time.Second):
				s.logf("link: old connection %s slow to finish", old.remote)
			}
		}
		go s.serveConn(c)
	}
}

func (s *Server) serveConn(c *conn) {
	defer close(c.finished)
	s.logf("link: device connected from %s", c.remote)
	if s.Handler != nil {
		s.Handler.DeviceConnected(c.remote)
	}
	go s.pingLoop(c)

	br := bufio.NewReaderSize(c.nc, 64<<10)
	var err error
	for {
		var f Frame
		f, err = ReadFrame(br)
		if err != nil {
			break
		}
		if s.Handler != nil {
			s.Handler.DeviceFrame(f)
		}
	}
	c.close()
	s.mu.Lock()
	if s.cur == c {
		s.cur = nil
	}
	s.mu.Unlock()
	s.logf("link: device %s disconnected (%v)", c.remote, err)
	if s.Handler != nil {
		s.Handler.DeviceDisconnected()
	}
}

func (s *Server) pingLoop(c *conn) {
	every := s.PingInterval
	if every <= 0 {
		every = 10 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			c.wmu.Lock()
			err := c.frame(TypePing, []byte("{}"))
			c.wmu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

// Connected reports whether a device is connected right now.
func (s *Server) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur != nil
}

// send runs fn with the current connection's write lock held.
func (s *Server) send(fn func(c *conn) error) error {
	s.mu.Lock()
	c := s.cur
	s.mu.Unlock()
	if c == nil {
		return ErrNotConnected
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return fn(c)
}

func (s *Server) sendJSON(typ byte, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.send(func(c *conn) error { return c.frame(typ, payload) })
}

// State sets Femi's face and terminal line (keep line ≤ 24 characters).
func (s *Server) State(face, line string) error {
	return s.sendJSON(TypeState, struct {
		Face string `json:"face"`
		Line string `json:"line"`
	}{face, line})
}

// Command sends a COMMAND frame with v marshalled as JSON.
func (s *Server) Command(v any) error { return s.sendJSON(TypeCommand, v) }

// Say sends SAY_START (caption + total bytes), the pcm in ChunkSize pieces,
// then SAY_END. pcm may be empty (caption only). If ctx is cancelled midway
// the remaining audio is dropped and SAY_END is still sent.
func (s *Server) Say(ctx context.Context, id, text string, pcm []byte) error {
	// "bytes" goes before the free text: the device parses control frames
	// from a fixed-size buffer, so a very long caption must not push it out.
	start, err := json.Marshal(struct {
		ID    string `json:"id"`
		Bytes int    `json:"bytes"`
		Text  string `json:"text"`
	}{id, len(pcm), text})
	if err != nil {
		return err
	}
	end, _ := json.Marshal(struct {
		ID string `json:"id"`
	}{id})
	return s.send(func(c *conn) error {
		if err := c.frame(TypeSayStart, start); err != nil {
			return err
		}
		for off := 0; off < len(pcm); off += ChunkSize {
			if err := ctx.Err(); err != nil {
				_ = c.frame(TypeSayEnd, end)
				return err
			}
			if err := c.frame(TypeSayChunk, pcm[off:min(off+ChunkSize, len(pcm))]); err != nil {
				return err
			}
		}
		return c.frame(TypeSayEnd, end)
	})
}

// Image sends the image COMMAND followed by IMAGE_CHUNK frames carrying
// rgb565 (little-endian, row-major, w*h*2 bytes).
func (s *Server) Image(caption string, w, h int, rgb565 []byte) error {
	cmd, err := json.Marshal(struct {
		Type    string `json:"type"`
		W       int    `json:"w"`
		H       int    `json:"h"`
		Bytes   int    `json:"bytes"`
		Caption string `json:"caption"`
	}{"image", w, h, len(rgb565), caption})
	if err != nil {
		return err
	}
	return s.send(func(c *conn) error {
		if err := c.frame(TypeCommand, cmd); err != nil {
			return err
		}
		for off := 0; off < len(rgb565); off += ChunkSize {
			if err := c.frame(TypeImageChunk, rgb565[off:min(off+ChunkSize, len(rgb565))]); err != nil {
				return err
			}
		}
		return nil
	})
}
