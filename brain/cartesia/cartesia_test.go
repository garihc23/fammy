package cartesia

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testKey = "sk_car_test_secret_123"

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(testKey)
	c.BaseURL = srv.URL
	c.HTTPClient = srv.Client()
	return c
}

func checkCommonHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", r.Method)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want Bearer <key>", got)
	}
	if got := r.Header.Get("Cartesia-Version"); got != "2026-08-14" {
		t.Errorf("Cartesia-Version = %q, want 2026-08-14", got)
	}
}

func TestNewDefaults(t *testing.T) {
	c := New("k")
	if c.BaseURL != "https://api.cartesia.ai" || c.Version != "2026-08-14" ||
		c.STTModel != "ink-whisper" || c.TTSModel != "sonic-3.6" ||
		c.VoiceID != DefaultVoiceID || c.Language != "en" ||
		c.STTTimeout != 30*time.Second || c.TTSTimeout != 30*time.Second || c.HTTPClient == nil {
		t.Fatalf("unexpected defaults: %v", c)
	}
}

func TestClientStringRedactsKey(t *testing.T) {
	c := New(testKey)
	for _, s := range []string{fmt.Sprint(c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c)} {
		if strings.Contains(s, testKey) {
			t.Fatalf("formatted client leaks key: %s", s)
		}
	}
}

func TestWavBytes(t *testing.T) {
	pcm := []byte{0x01, 0x02, 0x03, 0x04, 0xff, 0x7f}
	w := wavBytes(pcm, 16000)
	if len(w) != 44+len(pcm) {
		t.Fatalf("len = %d, want %d", len(w), 44+len(pcm))
	}
	le := binary.LittleEndian
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"RIFF", string(w[0:4]), "RIFF"},
		{"riff size", le.Uint32(w[4:8]), uint32(36 + len(pcm))},
		{"WAVE", string(w[8:12]), "WAVE"},
		{"fmt id", string(w[12:16]), "fmt "},
		{"fmt size", le.Uint32(w[16:20]), uint32(16)},
		{"format", le.Uint16(w[20:22]), uint16(1)},
		{"channels", le.Uint16(w[22:24]), uint16(1)},
		{"sample rate", le.Uint32(w[24:28]), uint32(16000)},
		{"byte rate", le.Uint32(w[28:32]), uint32(32000)},
		{"block align", le.Uint16(w[32:34]), uint16(2)},
		{"bits", le.Uint16(w[34:36]), uint16(16)},
		{"data id", string(w[36:40]), "data"},
		{"data size", le.Uint32(w[40:44]), uint32(len(pcm))},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if !bytes.Equal(w[44:], pcm) {
		t.Errorf("payload = %x, want %x", w[44:], pcm)
	}
	// Input must not be aliased/modified.
	if pcm[0] != 0x01 {
		t.Errorf("input modified")
	}

	if e := wavBytes(nil, 8000); len(e) != 44 || le.Uint32(e[40:44]) != 0 || le.Uint32(e[28:32]) != 16000 {
		t.Errorf("empty wav header wrong: %x", e)
	}
}

func TestTranscribeRequestShape(t *testing.T) {
	pcm := make([]byte, 3200) // 100 ms of 16 kHz s16le
	for i := range pcm {
		pcm[i] = byte(i)
	}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		checkCommonHeaders(t, r)
		if r.URL.Path != "/stt" {
			t.Errorf("path = %s, want /stt", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected query %q (WAV upload needs no encoding/sample_rate)", r.URL.RawQuery)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data; boundary=") {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.FormValue("model"); got != "ink-whisper" {
			t.Errorf("model = %q", got)
		}
		if got := r.FormValue("language"); got != "en" {
			t.Errorf("language = %q", got)
		}
		f, fh, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("file field: %v", err)
		}
		defer f.Close()
		if !strings.HasSuffix(fh.Filename, ".wav") {
			t.Errorf("filename = %q, want *.wav", fh.Filename)
		}
		if ct := fh.Header.Get("Content-Type"); ct != "audio/wav" {
			t.Errorf("file Content-Type = %q", ct)
		}
		got, _ := io.ReadAll(f)
		if !bytes.Equal(got, wavBytes(pcm, 16000)) {
			t.Errorf("uploaded file is not the expected WAV (len %d)", len(got))
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"type":"transcript","request_id":"req_1","text":"  Hello, I am Femi. ","language":"en","duration":0.1}`)
	})

	text, err := c.Transcribe(context.Background(), pcm, 16000)
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "Hello, I am Femi." {
		t.Errorf("text = %q", text)
	}
}

func TestTranscribeCustomModelAndLanguage(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.FormValue("model"); got != "ink-whisper-x" {
			t.Errorf("model = %q", got)
		}
		if got := r.FormValue("language"); got != "hi" {
			t.Errorf("language = %q", got)
		}
		io.WriteString(w, `{"type":"transcript","text":"namaste"}`)
	})
	c.STTModel = "ink-whisper-x"
	c.Language = "hi"
	if text, err := c.Transcribe(context.Background(), []byte{0, 0}, 16000); err != nil || text != "namaste" {
		t.Fatalf("got %q, %v", text, err)
	}
}

func TestTranscribeHTTPError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		// A misbehaving server echoing the key must not leak it into errors.
		io.WriteString(w, `{"error":"invalid api key `+testKey+`"}`)
	})
	_, err := c.Transcribe(context.Background(), []byte{0, 0}, 16000)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 401 || apiErr.Op != "stt" {
		t.Fatalf("err = %#v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "401") || !strings.Contains(msg, "invalid api key") {
		t.Errorf("error lacks status/body: %s", msg)
	}
	if strings.Contains(msg, testKey) {
		t.Errorf("error leaks key: %s", msg)
	}
}

func TestTranscribeBadJSON(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `not json`)
	})
	if _, err := c.Transcribe(context.Background(), []byte{0, 0}, 16000); err == nil || !strings.Contains(err.Error(), "not json") {
		t.Fatalf("err = %v", err)
	}
}

func TestTranscribeInputValidation(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be called")
	})
	cases := []struct {
		pcm  []byte
		rate int
	}{{nil, 16000}, {[]byte{1, 2, 3}, 16000}, {[]byte{1, 2}, 0}}
	for _, tc := range cases {
		if _, err := c.Transcribe(context.Background(), tc.pcm, tc.rate); err == nil {
			t.Errorf("Transcribe(%v, %d): expected error", tc.pcm, tc.rate)
		}
	}
}

func TestSpeakRequestShape(t *testing.T) {
	wantAudio := []byte{0x10, 0x00, 0x20, 0x00, 0xf0, 0xff}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		checkCommonHeaders(t, r)
		if r.URL.Path != "/tts/bytes" {
			t.Errorf("path = %s, want /tts/bytes", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		want := map[string]any{
			"model_id":   "sonic-3.6",
			"transcript": "Hello, I am Femi",
			"voice":      map[string]any{"id": DefaultVoiceID},
			"output_format": map[string]any{
				"container":   "raw",
				"encoding":    "pcm_s16le",
				"sample_rate": float64(16000),
			},
			"language":          "en",
			"generation_config": map[string]any{"emotion": "content"},
		}
		gotJSON, _ := json.Marshal(body)
		wantJSON, _ := json.Marshal(want)
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Errorf("body =\n %s\nwant\n %s", gotJSON, wantJSON)
		}
		w.Header().Set("Content-Type", "audio/pcm")
		w.Write(wantAudio)
	})

	audio, err := c.Speak(context.Background(), "Hello, I am Femi", 16000)
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if !bytes.Equal(audio, wantAudio) {
		t.Errorf("audio = %x, want %x", audio, wantAudio)
	}
}

func TestSpeakCustomVoiceAndModel(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body ttsRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Voice.ID != "voice-123" || body.ModelID != "sonic-3" || body.OutputFormat.SampleRate != 24000 {
			t.Errorf("body = %+v", body)
		}
		w.Write([]byte{0, 0})
	})
	c.VoiceID = "voice-123"
	c.TTSModel = "sonic-3"
	if _, err := c.Speak(context.Background(), "hi", 24000); err != nil {
		t.Fatal(err)
	}
}

func TestSpeakHTTPErrorTruncatesBody(t *testing.T) {
	long := `{"error":"bad voice"}` + strings.Repeat("x", 4000)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, long)
	})
	_, err := c.Speak(context.Background(), "hi", 16000)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 || apiErr.Op != "tts" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "bad voice") {
		t.Errorf("error = %s", err)
	}
	if len(apiErr.Body) > maxErrorSnippet+3 {
		t.Errorf("body snippet not truncated: %d bytes", len(apiErr.Body))
	}
}

func TestSpeakInputValidation(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be called")
	})
	if _, err := c.Speak(context.Background(), "   ", 16000); err == nil {
		t.Error("expected error for empty text")
	}
	if _, err := c.Speak(context.Background(), "hi", 11025); err == nil {
		t.Error("expected error for unsupported sample rate")
	}
}

func TestSpeakEmptyAudio(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	if _, err := c.Speak(context.Background(), "hi", 16000); err == nil {
		t.Error("expected error for empty audio")
	}
}

func TestSpeakTimeout(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Drain the body so the server notices the client hanging up.
		io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	c.TTSTimeout = 50 * time.Millisecond
	start := time.Now()
	_, err := c.Speak(context.Background(), "hi", 16000)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("timeout not applied promptly: %v", time.Since(start))
	}
}

func TestBaseURLTrailingSlash(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tts/bytes" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte{0, 0})
	})
	c.BaseURL += "/"
	if _, err := c.Speak(context.Background(), "hi", 16000); err != nil {
		t.Fatal(err)
	}
}
