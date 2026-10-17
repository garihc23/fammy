// Package link is the brain side of the device⇄brain TCP protocol
// (see /home/leog/femi/PROTOCOL.md): length-prefixed frames, one device at a
// time, thread-safe writers and a keep-alive PING.
package link

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Frame types, device → brain.
const (
	TypeHello      byte = 0x01
	TypeAudioStart byte = 0x02
	TypeAudioChunk byte = 0x03
	TypeAudioEnd   byte = 0x04
	TypeEvent      byte = 0x05
)

// Frame types, brain → device.
const (
	TypeState      byte = 0x81
	TypeSayStart   byte = 0x82
	TypeSayChunk   byte = 0x83
	TypeSayEnd     byte = 0x84
	TypeCommand    byte = 0x85
	TypeImageChunk byte = 0x86
	TypePing       byte = 0x87
)

const (
	// HeaderSize is 1 byte type + 4 bytes little-endian payload length.
	HeaderSize = 5
	// MaxPayload is the largest payload either side may send.
	MaxPayload = 1 << 20
	// ChunkSize is the size of the SAY_CHUNK / IMAGE_CHUNK pieces we send.
	ChunkSize = 4096
)

// ErrTooLarge is returned for a frame whose payload exceeds MaxPayload.
var ErrTooLarge = errors.New("link: frame payload exceeds 1 MiB")

// Frame is one protocol message.
type Frame struct {
	Type    byte
	Payload []byte
}

// TypeName returns a readable name for a frame type, for logs.
func TypeName(t byte) string {
	switch t {
	case TypeHello:
		return "HELLO"
	case TypeAudioStart:
		return "AUDIO_START"
	case TypeAudioChunk:
		return "AUDIO_CHUNK"
	case TypeAudioEnd:
		return "AUDIO_END"
	case TypeEvent:
		return "EVENT"
	case TypeState:
		return "STATE"
	case TypeSayStart:
		return "SAY_START"
	case TypeSayChunk:
		return "SAY_CHUNK"
	case TypeSayEnd:
		return "SAY_END"
	case TypeCommand:
		return "COMMAND"
	case TypeImageChunk:
		return "IMAGE_CHUNK"
	case TypePing:
		return "PING"
	}
	return fmt.Sprintf("0x%02x", t)
}

// ReadFrame reads exactly one frame. Partial reads are handled; a clean EOF
// before the header returns io.EOF, an EOF inside a frame io.ErrUnexpectedEOF.
func ReadFrame(r io.Reader) (Frame, error) {
	var hdr [HeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	n := binary.LittleEndian.Uint32(hdr[1:])
	if n > MaxPayload {
		return Frame{}, fmt.Errorf("%w (type %s, %d bytes)", ErrTooLarge, TypeName(hdr[0]), n)
	}
	f := Frame{Type: hdr[0], Payload: make([]byte, n)}
	if _, err := io.ReadFull(r, f.Payload); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return Frame{}, err
	}
	return f, nil
}

// WriteFrame writes one frame with a single Write call.
func WriteFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > MaxPayload {
		return ErrTooLarge
	}
	buf := make([]byte, HeaderSize+len(payload))
	buf[0] = typ
	binary.LittleEndian.PutUint32(buf[1:HeaderSize], uint32(len(payload)))
	copy(buf[HeaderSize:], payload)
	_, err := w.Write(buf)
	return err
}
