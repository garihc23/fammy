# Famyy

Famyy (say it "Fammy", like family) is an AI family member that lives on the desk: a little ESP32-S3 robot with a face, a mic, a speaker, a touch pad and a camera on the laptop. Kids learn and play with it, parents hand it errands, grandparents get medicine reminders and company.

Built for the GSD hackathon by Karan and Chirag.

## How it works

```
 device (ESP32-S3)           brain (Go, on the laptop)                    the world
 wake word "hey Famyy"  ──>  Cartesia ink-whisper: speech to text
 screen, speaker, touch <──  Claude (Claude Code, Haiku 5.5): thinks      Fo (Wajo, MCP):
                             Famyy's own tools (MCP): reminders, timers,  calls, texts, email,
                             photos, camera, lists, approvals             shopping, research
                             Cartesia sonic: Famyy's voice
```

- **Device** (`firmware/femi`): wake word on the chip, records the request, shows Famyy and Fo on the screen, plays the reply. Tap to approve, stop or talk.
- **Brain** (`brain/`): one TCP link to the device ([PROTOCOL.md](PROTOCOL.md)). It keeps a Claude Code session warm between turns, so a reply starts in about 1.5 s. Long Fo errands carry on in the background, and Famyy reports when they finish.

## Safety

- Anything that sends, calls, books or buys needs a tap (or a spoken "yes") on Famyy's approval card first.
- Calls and texts only between 8 am and 10 pm unless it's urgent or confirmed.
- Camera photos stay in memory only and are never saved; conversations aren't written to disk either.
- Famyy's Claude has no shell or file access: only web search, Fo and Famyy's own tools.

## Run it

Brain (Linux, Go 1.24+, [Claude Code](https://claude.com/claude-code) installed and signed in):

```bash
cp brain/.env.example brain/.env    # then add your keys; brain/.env is git-ignored
./start-brain.sh
```

Device: copy `firmware/femi/secrets.example.h` to `secrets.h` (git-ignored) with your Wi-Fi and the laptop's address, then:

```bash
firmware/build.sh femi upload
```

Before restarting the brain or flashing the device while it's in use, `tools/brain-idle.sh 60` waits until nothing is in flight.

## Layout

| Path | What |
|---|---|
| `brain/` | the brain: `agent` (Claude Code runner), `femi` (turns, tools, speech queue), `cartesia`, `camera`, `link`, `mcp`, `remind` |
| `brain/prompt.md` | who Famyy is and how it behaves |
| `firmware/femi/` | the ESP32-S3 firmware |
| `firmware/preview/` | renders the device screen on the laptop |
| `tools/` | `brain-idle.sh`, `esp-speaker.sh` |
| `family/` | the family's profile, photos and reminders (personal, git-ignored) |
