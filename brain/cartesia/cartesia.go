// Package cartesia is a small client for Cartesia's batch speech-to-text and
// non-streaming text-to-speech HTTP APIs, as used by the Femi brain.
//
// API details were checked against the official docs on 2026-10-17:
//
//	Batch STT (POST /stt):        https://docs.cartesia.ai/api-reference/stt/transcribe
//	TTS bytes (POST /tts/bytes):  https://docs.cartesia.ai/api-reference/tts/bytes
//	Auth headers:                 https://docs.cartesia.ai/get-started/authenticate-your-client-applications
//	STT endpoints vs models:      https://docs.cartesia.ai/use-the-api/stt/compare-endpoints
//	Audio input formats:          https://docs.cartesia.ai/build-with-cartesia/stt/audio-input
//	Voice choice:                 https://docs.cartesia.ai/build-with-cartesia/capability-guides/choosing-a-voice
//
// Cartesia-Version used: 2026-08-14 (the only value the current spec allows).
// Auth: "Authorization: Bearer <api key>" (the docs also accept "X-Api-Key").
package cartesia

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is Cartesia's production API host.
	DefaultBaseURL = "https://api.cartesia.ai"

	// APIVersion is sent in the required Cartesia-Version header.
	APIVersion = "2026-08-14"

	// DefaultSTTModel is the only model the batch /stt endpoint accepts
	// (ink-2 / ink-preview are WebSocket-only).
	DefaultSTTModel = "ink-whisper"

	// DefaultTTSModel is the current Sonic model (the spec's default).
	DefaultTTSModel = "sonic-3.6"

	// VoiceJacqueline is Cartesia's "Jacqueline" voice: a warm, reassuring,
	// clearly articulated young American female voice. Cartesia lists her
	// among its stable, realistic English voices recommended for production
	// voice agents, which suits a family assistant talking to kids and
	// elderly people.
	VoiceJacqueline = "9626c31c-bec5-4cca-baa8-f8ba9e84c8bc"

	// VoiceSkylar is Cartesia's "Skylar" voice: an approachable American
	// female voice made for customer care. Famyy's voice, picked by the team.
	VoiceSkylar = "db6b0ed5-d5d3-463d-ae85-518a07d3c2b4"

	// VoiceArushi is Cartesia's "Arushi" voice: a Hinglish female voice for
	// bilingual content. Speaking English (DefaultLanguage) she has an Indian
	// accent, which suits an Indian family. Famyy's voice, picked by the team.
	VoiceArushi = "95d51f79-c397-46f9-b49a-23763d3eaa2d"

	// VoiceDev is Cartesia's "Dev": a warm, conversational Hindi male voice. It
	// reads the other side of a phone call back (Fo's side is Jacqueline).
	VoiceDev = "910fb75e-1d20-4840-ac63-ac6b26a71bdc"

	// DefaultVoiceID is the voice used by New.
	DefaultVoiceID = VoiceArushi

	// DefaultEmotion colours every reply. "content" (warm, pleased) and "calm"
	// are both among Sonic-3's most reliable emotions; content suits cheerful
	// news and kids without sounding over-excited to the elderly. Emotion is
	// beta and English only.
	DefaultEmotion = "content"

	// DefaultLanguage is used for both STT and TTS.
	DefaultLanguage = "en"

	// DefaultSTTTimeout and DefaultTTSTimeout bound a single request.
	DefaultSTTTimeout = 30 * time.Second
	DefaultTTSTimeout = 30 * time.Second
)

const (
	maxErrorSnippet  = 512      // bytes of an error response body kept in APIError
	maxSTTResponse   = 1 << 20  // 1 MiB of transcript JSON is plenty
	maxAudioResponse = 32 << 20 // ~17 min of 16 kHz s16le mono
)

// ttsSampleRates are the output sample rates the TTS spec allows.
var ttsSampleRates = map[int]bool{8000: true, 16000: true, 22050: true, 24000: true, 44100: true, 48000: true}

// Client talks to the Cartesia API. Create it with New, then adjust the
// exported fields before use if the defaults do not fit. It is safe for
// concurrent use as long as the fields are not modified while requests run.
type Client struct {
	apiKey string

	BaseURL    string        // default DefaultBaseURL
	Version    string        // Cartesia-Version header, default APIVersion
	STTModel   string        // default DefaultSTTModel
	TTSModel   string        // default DefaultTTSModel
	VoiceID    string        // default DefaultVoiceID
	Language   string        // ISO-639-1 code, default DefaultLanguage
	Emotion    string        // TTS emotion, default DefaultEmotion; "" lets the model infer it from the text
	STTTimeout time.Duration // per Transcribe call; 0 means no extra timeout
	TTSTimeout time.Duration // per Speak call; 0 means no extra timeout
	HTTPClient *http.Client  // default: a dedicated client with no global timeout
}

// New returns a Client with sensible defaults.
func New(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		BaseURL:    DefaultBaseURL,
		Version:    APIVersion,
		STTModel:   DefaultSTTModel,
		TTSModel:   DefaultTTSModel,
		VoiceID:    DefaultVoiceID,
		Language:   DefaultLanguage,
		Emotion:    DefaultEmotion,
		STTTimeout: DefaultSTTTimeout,
		TTSTimeout: DefaultTTSTimeout,
		HTTPClient: &http.Client{},
	}
}

// String describes the client without revealing the API key.
func (c *Client) String() string {
	return fmt.Sprintf("cartesia.Client{BaseURL:%q Version:%q STTModel:%q TTSModel:%q VoiceID:%q apiKey:[redacted]}",
		c.BaseURL, c.Version, c.STTModel, c.TTSModel, c.VoiceID)
}

// GoString keeps %#v from printing the API key.
func (c *Client) GoString() string { return c.String() }

// APIError is returned when Cartesia answers with a non-2xx status.
type APIError struct {
	Op         string // "stt" or "tts"
	StatusCode int
	Body       string // first bytes of the response body, API key scrubbed
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("cartesia %s: HTTP %d %s", e.Op, e.StatusCode, http.StatusText(e.StatusCode))
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

// sttResponse mirrors the documented TranscriptionResponse.
type sttResponse struct {
	Type      string  `json:"type"`
	RequestID string  `json:"request_id"`
	Text      string  `json:"text"`
	Language  string  `json:"language"`
	Duration  float64 `json:"duration"`
}

// Transcribe sends raw signed 16-bit little-endian mono PCM (no header) to the
// batch STT endpoint and returns the transcript. The audio is wrapped in a WAV
// container so the server can decode it without extra query parameters.
func (c *Client) Transcribe(ctx context.Context, pcm []byte, sampleRate int) (string, error) {
	if len(pcm) == 0 {
		return "", errors.New("cartesia stt: empty audio")
	}
	if len(pcm)%2 != 0 {
		return "", fmt.Errorf("cartesia stt: pcm length %d is not a whole number of 16-bit samples", len(pcm))
	}
	if sampleRate <= 0 {
		return "", fmt.Errorf("cartesia stt: invalid sample rate %d", sampleRate)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="audio.wav"`)
	h.Set("Content-Type", "audio/wav")
	part, err := mw.CreatePart(h)
	if err != nil {
		return "", fmt.Errorf("cartesia stt: build form: %w", err)
	}
	if _, err := part.Write(wavBytes(pcm, sampleRate)); err != nil {
		return "", fmt.Errorf("cartesia stt: build form: %w", err)
	}
	if err := mw.WriteField("model", c.STTModel); err != nil {
		return "", fmt.Errorf("cartesia stt: build form: %w", err)
	}
	if c.Language != "" {
		if err := mw.WriteField("language", c.Language); err != nil {
			return "", fmt.Errorf("cartesia stt: build form: %w", err)
		}
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("cartesia stt: build form: %w", err)
	}

	ctx, cancel := withTimeout(ctx, c.STTTimeout)
	defer cancel()

	resp, err := c.do(ctx, "/stt", mw.FormDataContentType(), &body)
	if err != nil {
		return "", fmt.Errorf("cartesia stt: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", c.apiError("stt", resp)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSTTResponse))
	if err != nil {
		return "", fmt.Errorf("cartesia stt: read response: %w", err)
	}
	var out sttResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("cartesia stt: decode response: %w: %s", err, c.snippet(raw))
	}
	return strings.TrimSpace(out.Text), nil
}

// ttsRequest mirrors the documented TTSRequest (fields we use).
type ttsRequest struct {
	ModelID      string          `json:"model_id"`
	Transcript   string          `json:"transcript"`
	Voice        ttsVoice        `json:"voice"`
	OutputFormat ttsOutputFormat `json:"output_format"`
	Language     string          `json:"language,omitempty"`
	Generation   *ttsGeneration  `json:"generation_config,omitempty"`
}

type ttsGeneration struct {
	Emotion string `json:"emotion,omitempty"`
}

type ttsVoice struct {
	ID string `json:"id"`
}

type ttsOutputFormat struct {
	Container  string `json:"container"`
	Encoding   string `json:"encoding"`
	SampleRate int    `json:"sample_rate"`
}

// Speak synthesizes text and returns raw pcm_s16le mono audio at sampleRate,
// with no container header, ready to be fed to an I2S amplifier.
func (c *Client) Speak(ctx context.Context, text string, sampleRate int) ([]byte, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("cartesia tts: empty text")
	}
	if !ttsSampleRates[sampleRate] {
		return nil, fmt.Errorf("cartesia tts: unsupported sample rate %d (want 8000, 16000, 22050, 24000, 44100 or 48000)", sampleRate)
	}

	req := ttsRequest{
		ModelID:    c.TTSModel,
		Transcript: text,
		Voice:      ttsVoice{ID: c.VoiceID},
		OutputFormat: ttsOutputFormat{
			Container:  "raw",
			Encoding:   "pcm_s16le",
			SampleRate: sampleRate,
		},
		Language: c.Language,
	}
	if c.Emotion != "" {
		req.Generation = &ttsGeneration{Emotion: c.Emotion}
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("cartesia tts: encode request: %w", err)
	}

	ctx, cancel := withTimeout(ctx, c.TTSTimeout)
	defer cancel()

	resp, err := c.do(ctx, "/tts/bytes", "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("cartesia tts: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, c.apiError("tts", resp)
	}

	audio, err := io.ReadAll(io.LimitReader(resp.Body, maxAudioResponse+1))
	if err != nil {
		return nil, fmt.Errorf("cartesia tts: read audio: %w", err)
	}
	if len(audio) > maxAudioResponse {
		return nil, fmt.Errorf("cartesia tts: audio response exceeds %d bytes", maxAudioResponse)
	}
	if len(audio) == 0 {
		return nil, errors.New("cartesia tts: empty audio response")
	}
	return audio, nil
}

func (c *Client) do(ctx context.Context, path, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Cartesia-Version", c.Version)
	req.Header.Set("Content-Type", contentType)
	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	return hc.Do(req)
}

func (c *Client) apiError(op string, resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorSnippet+1))
	return &APIError{Op: op, StatusCode: resp.StatusCode, Body: c.snippet(raw)}
}

// snippet trims a response body for inclusion in an error and scrubs the key
// in case the server ever echoes it back.
func (c *Client) snippet(raw []byte) string {
	s := string(raw)
	if len(s) > maxErrorSnippet {
		s = s[:maxErrorSnippet] + "..."
	}
	if c.apiKey != "" {
		s = strings.ReplaceAll(s, c.apiKey, "[redacted]")
	}
	return strings.TrimSpace(s)
}

func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

// wavBytes wraps raw signed 16-bit little-endian mono PCM in a canonical
// 44-byte RIFF/WAVE header.
func wavBytes(pcm []byte, rate int) []byte {
	const (
		channels      = 1
		bitsPerSample = 16
		blockAlign    = channels * bitsPerSample / 8
	)
	out := make([]byte, 44, 44+len(pcm))
	le := binary.LittleEndian
	copy(out[0:4], "RIFF")
	le.PutUint32(out[4:8], uint32(36+len(pcm)))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	le.PutUint32(out[16:20], 16) // fmt chunk size
	le.PutUint16(out[20:22], 1)  // PCM
	le.PutUint16(out[22:24], channels)
	le.PutUint32(out[24:28], uint32(rate))
	le.PutUint32(out[28:32], uint32(rate*blockAlign)) // byte rate
	le.PutUint16(out[32:34], blockAlign)
	le.PutUint16(out[34:36], bitsPerSample)
	copy(out[36:40], "data")
	le.PutUint32(out[40:44], uint32(len(pcm)))
	return append(out, pcm...)
}

// SpeakAs is Speak in another voice, e.g. each side of a phone call read back.
func (c *Client) SpeakAs(ctx context.Context, voiceID, text string, sampleRate int) ([]byte, error) {
	cc := *c
	cc.VoiceID = voiceID
	return cc.Speak(ctx, text, sampleRate)
}
