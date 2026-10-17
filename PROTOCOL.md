# Femi link protocol

One TCP connection from the device (client) to the brain (server, port **8787**).
The device reconnects every 2 s if the link drops. No TLS: it never leaves the home Wi-Fi.

## Framing

Every message is one frame:

| bytes | field |
|---|---|
| 1 | type |
| 4 | payload length N, unsigned little-endian (max 1 MiB) |
| N | payload: UTF-8 JSON for control frames, raw bytes for audio/image frames |

Audio is always **16 kHz, signed 16-bit little-endian, mono** (`pcm_s16le`) in both directions.

## Device → brain

| type | name | payload |
|---|---|---|
| `0x01` | HELLO | `{"device":"femi","fw":"0.1"}` sent once after connecting |
| `0x02` | AUDIO_START | `{"rate":16000,"trigger":"wake"\|"tap"}` the user started speaking |
| `0x03` | AUDIO_CHUNK | raw pcm, streamed live while the user talks |
| `0x04` | AUDIO_END | `{"reason":"silence"\|"max"\|"cancel"}` end of the utterance; `cancel` = throw it away |
| `0x05` | EVENT | JSON, see below |

Device events (`0x05`):
- `{"type":"approval","id":"a1","ok":true}` answer to an APPROVE command (tap = true, hold = false, timeout = no event)
- `{"type":"ack","id":"r1"}` the user acknowledged a reminder (tapped)
- `{"type":"text","text":"what's on my calendar?"}` typed request, for testing without speaking
- `{"type":"played","id":"s1"}` the device finished playing a SAY

## Brain → device

| type | name | payload |
|---|---|---|
| `0x81` | STATE | `{"face":"thinking"\|"working"\|"idle"\|"happy"\|"sad","line":"Asking Fo…"}` what Femi should look like now; `line` goes in the terminal line (keep ≤ 24 chars) |
| `0x82` | SAY_START | `{"id":"s1","text":"…","bytes":N}` Femi is about to speak; `text` is shown as a caption, N = total pcm bytes that follow |
| `0x83` | SAY_CHUNK | raw pcm |
| `0x84` | SAY_END | `{"id":"s1"}` |
| `0x85` | COMMAND | JSON, see below |
| `0x86` | IMAGE_CHUNK | raw RGB565 pixels (little-endian uint16), row-major, for the IMAGE command before it |
| `0x87` | PING | `{}` keep-alive every 10 s; the device ignores it |

Brain commands (`0x85`):
- `{"type":"approve","id":"a1","text":"Email Rianna: I'm in for Saturday"}` ask the person to approve: **tap = yes, hold 2 s = no**; device answers with an approval event, or nothing after 60 s
- `{"type":"reminder","id":"r1","text":"Time for your 9 am medicine","urgent":true}` show a reminder until tapped (device answers with ack)
- `{"type":"image","w":320,"h":240,"caption":"Riya at the beach, 2019","bytes":N}` followed by IMAGE_CHUNK frames totalling N bytes; device shows it full screen until tapped or 20 s
- `{"type":"timer","minutes":5}` / `{"type":"timer","action":"cancel"|"pause"|"resume"|"pomodoro"}` drive the on-device timer
- `{"type":"app","name":"game"|"morse"}` start an on-device app
- `{"type":"theme","name":"normal"|"hacker"}`

## Flow of one request

```
device                                   brain
AUDIO_START ─────────────────────────────►
AUDIO_CHUNK… ────────────────────────────►   (buffered)
AUDIO_END ───────────────────────────────►   speech → text (Cartesia)
          ◄──────────────── STATE thinking   Claude turn starts (with Fo + Femi tools)
          ◄──────── STATE working "Calling…"  Fo progress lines, as they arrive
          ◄──────────── COMMAND approve a1   (only for things that cost money or message someone)
EVENT approval a1 ok ────────────────────►
          ◄── SAY_START / SAY_CHUNK… / SAY_END   reply text → speech (Cartesia)
EVENT played s1 ─────────────────────────►
```
While Femi is speaking the device ignores the wake word, so it doesn't hear itself.
