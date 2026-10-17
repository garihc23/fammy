package femi

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// keepRecordings is how many of the newest recordings saveRecording keeps.
const keepRecordings = 20

// saveRecording writes one request as a 16 kHz mono WAV in dir, named by time
// and why it ended, so the device's silence detector can be tuned against
// real rooms. Older files beyond keepRecordings are removed.
func saveRecording(dir string, pcm []byte, reason string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+len(pcm)))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:], 1) // mono
	binary.LittleEndian.PutUint32(h[24:], SampleRate)
	binary.LittleEndian.PutUint32(h[28:], SampleRate*2)
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(len(pcm)))
	name := filepath.Join(dir, fmt.Sprintf("%s-%s.wav", time.Now().Format("20060102-150405"), reason))
	if err := os.WriteFile(name, append(h, pcm...), 0o644); err != nil {
		return err
	}
	old, _ := filepath.Glob(filepath.Join(dir, "*.wav"))
	sort.Strings(old)
	for len(old) > keepRecordings {
		os.Remove(old[0])
		old = old[1:]
	}
	return nil
}
