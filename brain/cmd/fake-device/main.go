// Command fake-device pretends to be Femi's ESP32 so the brain can be tested
// without hardware. It connects, sends HELLO, turns every stdin line into a
// text event and prints every frame it receives.
//
//	go run ./cmd/fake-device [-addr 127.0.0.1:8787] [-pings]
//
// stdin:
//
//	any text          sent as {"type":"text","text":...}
//	y / n             answers a pending approval (tap / hold)
//	/ack [id]         acknowledges the last (or given) reminder
//	/audio file.raw   sends a raw pcm_s16le 16 kHz mono file as an utterance
//	/quit
//
// SAY audio is saved to /tmp/femi-say-<id>.raw (play: aplay -f S16_LE -r 16000 -c 1 FILE),
// images to /tmp/femi-image-<n>.png.
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"femi/brain/link"
)

type device struct {
	conn  net.Conn
	wmu   sync.Mutex
	pings bool

	mu           sync.Mutex
	approvalID   string
	reminderID   string
	sayID        string
	sayFile      *os.File
	sayBytes     int
	image        []byte
	imageW       int
	imageH       int
	imageCaption string
	imageN       int
}

func ts() string { return time.Now().Format("15:04:05.000") }

func (d *device) send(typ byte, payload []byte) {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if err := link.WriteFrame(d.conn, typ, payload); err != nil {
		fmt.Printf("%s !! send %s: %v\n", ts(), link.TypeName(typ), err)
	}
}

func (d *device) sendJSON(typ byte, v any) {
	b, _ := json.Marshal(v)
	d.send(typ, b)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "brain host:port")
	pings := flag.Bool("pings", false, "print PING frames too")
	flag.Parse()

	conn, err := net.Dial("tcp", *addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect %s: %v\n", *addr, err)
		os.Exit(1)
	}
	d := &device{conn: conn, pings: *pings}
	fmt.Printf("%s connected to %s\n", ts(), *addr)
	d.sendJSON(link.TypeHello, map[string]string{"device": "femi", "fw": "fake-0.1"})

	go d.stdin()
	d.read() // until the brain hangs up
}

func (d *device) read() {
	for {
		f, err := link.ReadFrame(d.conn)
		if err != nil {
			fmt.Printf("%s link closed: %v\n", ts(), err)
			os.Exit(0)
		}
		d.handle(f)
	}
}

func (d *device) handle(f link.Frame) {
	switch f.Type {
	case link.TypePing:
		if d.pings {
			fmt.Printf("%s <- PING\n", ts())
		}
	case link.TypeState:
		var s struct{ Face, Line string }
		json.Unmarshal(f.Payload, &s)
		fmt.Printf("%s <- STATE %-8s %q\n", ts(), s.Face, s.Line)
	case link.TypeSayStart:
		var s struct {
			ID    string `json:"id"`
			Text  string `json:"text"`
			Bytes int    `json:"bytes"`
		}
		json.Unmarshal(f.Payload, &s)
		fmt.Printf("%s <- SAY %s %q (%d bytes, %.1f s audio)\n", ts(), s.ID, s.Text, s.Bytes, float64(s.Bytes)/32000)
		d.mu.Lock()
		d.sayID, d.sayBytes = s.ID, 0
		if s.Bytes > 0 {
			d.sayFile, _ = os.Create(fmt.Sprintf("/tmp/femi-say-%s.raw", s.ID))
		}
		d.mu.Unlock()
	case link.TypeSayChunk:
		d.mu.Lock()
		d.sayBytes += len(f.Payload)
		if d.sayFile != nil {
			d.sayFile.Write(f.Payload)
		}
		d.mu.Unlock()
	case link.TypeSayEnd:
		d.mu.Lock()
		id := d.sayID
		if d.sayFile != nil {
			fmt.Printf("%s <- SAY_END %s: saved %d bytes to %s\n", ts(), id, d.sayBytes, d.sayFile.Name())
			d.sayFile.Close()
			d.sayFile = nil
		} else {
			fmt.Printf("%s <- SAY_END %s\n", ts(), id)
		}
		d.mu.Unlock()
		d.sendJSON(link.TypeEvent, map[string]string{"type": "played", "id": id})
	case link.TypeCommand:
		fmt.Printf("%s <- COMMAND %s\n", ts(), f.Payload)
		var c struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Text    string `json:"text"`
			W       int    `json:"w"`
			H       int    `json:"h"`
			Caption string `json:"caption"`
			Bytes   int    `json:"bytes"`
		}
		json.Unmarshal(f.Payload, &c)
		d.mu.Lock()
		switch c.Type {
		case "approve":
			d.approvalID = c.ID
			fmt.Printf("   approve? [y/n] %s\n", c.Text)
		case "reminder":
			d.reminderID = c.ID
			fmt.Printf("   REMINDER %s: %s  (type /ack to tap)\n", c.ID, c.Text)
		case "image":
			d.image, d.imageW, d.imageH, d.imageCaption = make([]byte, 0, c.Bytes), c.W, c.H, c.Caption
			if c.Bytes == 0 {
				d.image = nil
			}
		}
		d.mu.Unlock()
	case link.TypeImageChunk:
		d.mu.Lock()
		if d.image != nil {
			d.image = append(d.image, f.Payload...)
			if len(d.image) >= d.imageW*d.imageH*2 {
				d.imageN++
				name := fmt.Sprintf("/tmp/femi-image-%d.png", d.imageN)
				savePNG(name, d.image, d.imageW, d.imageH)
				fmt.Printf("%s    IMAGE %dx%d, %d bytes, caption %q -> %s\n", ts(), d.imageW, d.imageH, len(d.image), d.imageCaption, name)
				d.image = nil
			}
		}
		d.mu.Unlock()
	default:
		fmt.Printf("%s <- %s %q\n", ts(), link.TypeName(f.Type), f.Payload)
	}
}

func savePNG(path string, rgb565 []byte, w, h int) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		v := binary.LittleEndian.Uint16(rgb565[i*2:])
		r, g, b := uint8(v>>11)<<3, uint8(v>>5&0x3f)<<2, uint8(v&0x1f)<<3
		img.Set(i%w, i/w, color.RGBA{r | r>>5, g | g>>6, b | b>>5, 255})
	}
	if f, err := os.Create(path); err == nil {
		png.Encode(f, img)
		f.Close()
	}
}

func (d *device) stdin() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		d.mu.Lock()
		approval := d.approvalID
		reminder := d.reminderID
		d.mu.Unlock()

		low := strings.ToLower(line)
		switch {
		case approval != "" && (low == "y" || low == "yes" || low == "n" || low == "no"):
			ok := low[0] == 'y'
			d.mu.Lock()
			d.approvalID = ""
			d.mu.Unlock()
			fmt.Printf("%s -> approval %s ok=%v\n", ts(), approval, ok)
			d.sendJSON(link.TypeEvent, map[string]any{"type": "approval", "id": approval, "ok": ok})
		case low == "/quit":
			os.Exit(0)
		case strings.HasPrefix(low, "/ack"):
			id := strings.TrimSpace(line[4:])
			if id == "" {
				id = reminder
			}
			fmt.Printf("%s -> ack %s\n", ts(), id)
			d.sendJSON(link.TypeEvent, map[string]string{"type": "ack", "id": id})
		case strings.HasPrefix(low, "/audio "):
			d.sendAudio(strings.TrimSpace(line[7:]))
		default:
			fmt.Printf("%s -> text %q\n", ts(), line)
			d.sendJSON(link.TypeEvent, map[string]string{"type": "text", "text": line})
		}
	}
	// stdin closed: keep listening until the brain hangs up or ^C.
}

func (d *device) sendAudio(path string) {
	pcm, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("!! %v\n", err)
		return
	}
	fmt.Printf("%s -> utterance %s (%.1f s)\n", ts(), path, float64(len(pcm))/32000)
	d.sendJSON(link.TypeAudioStart, map[string]any{"rate": 16000, "trigger": "tap"})
	for off := 0; off < len(pcm); off += 2048 {
		d.send(link.TypeAudioChunk, pcm[off:min(off+2048, len(pcm))])
	}
	d.sendJSON(link.TypeAudioEnd, map[string]string{"reason": "silence"})
}
