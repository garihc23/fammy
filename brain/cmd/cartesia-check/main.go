// Command cartesia-check is a manual end-to-end check of the cartesia package.
//
//	go run ./cmd/cartesia-check say "Hello, I am Femi"   # writes /tmp/femi-say.raw
//	go run ./cmd/cartesia-check hear /tmp/femi-say.raw   # prints the transcript
//
// Audio files are raw pcm_s16le mono at 16 kHz (what the device sends/plays).
// The API key comes from CARTESIA_API_KEY, or from a .env file (./.env, then
// /home/leog/femi/brain/.env). The key is never printed.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"femi/brain/cartesia"
)

const (
	sampleRate = 16000
	sayOutPath = "/tmp/femi-say.raw"
	keyName    = "CARTESIA_API_KEY"
)

var envFiles = []string{".env", "/home/leog/femi/brain/.env"}

func main() {
	if len(os.Args) < 3 || (os.Args[1] != "say" && os.Args[1] != "hear") {
		fmt.Fprintln(os.Stderr, `usage: cartesia-check say "text to speak"
       cartesia-check hear file.raw   (raw pcm_s16le mono 16 kHz)`)
		os.Exit(2)
	}

	key := loadKey()
	if key == "" {
		fmt.Fprintf(os.Stderr, "no Cartesia API key: set %s or add %s=... to %s\n",
			keyName, keyName, strings.Join(envFiles, " or "))
		os.Exit(1)
	}
	c := cartesia.New(key)
	ctx := context.Background()

	switch os.Args[1] {
	case "say":
		text := strings.Join(os.Args[2:], " ")
		start := time.Now()
		pcm, err := c.Speak(ctx, text, sampleRate)
		if err != nil {
			fail(err)
		}
		elapsed := time.Since(start)
		if err := os.WriteFile(sayOutPath, pcm, 0o644); err != nil {
			fail(err)
		}
		secs := float64(len(pcm)) / 2 / sampleRate
		fmt.Printf("wrote %s: %.2f s of audio (%d bytes, pcm_s16le mono %d Hz); request took %v\n",
			sayOutPath, secs, len(pcm), sampleRate, elapsed.Round(time.Millisecond))
		fmt.Printf("play it with: aplay -f S16_LE -r %d -c 1 %s\n", sampleRate, sayOutPath)

	case "hear":
		pcm, err := os.ReadFile(os.Args[2])
		if err != nil {
			fail(err)
		}
		start := time.Now()
		text, err := c.Transcribe(ctx, pcm, sampleRate)
		if err != nil {
			fail(err)
		}
		fmt.Fprintf(os.Stderr, "transcribed %.2f s of audio in %v\n",
			float64(len(pcm))/2/sampleRate, time.Since(start).Round(time.Millisecond))
		fmt.Println(text)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// loadKey returns the API key from the environment or the first .env file
// that defines it.
func loadKey() string {
	if k := strings.TrimSpace(os.Getenv(keyName)); k != "" {
		return k
	}
	for _, path := range envFiles {
		if k := readEnvFile(path)[keyName]; k != "" {
			return k
		}
	}
	return ""
}

// readEnvFile parses simple KEY=VALUE lines, ignoring blanks, # comments, an
// optional "export " prefix and surrounding quotes. Missing files yield nil.
func readEnvFile(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		vals[strings.TrimSpace(k)] = v
	}
	return vals
}
