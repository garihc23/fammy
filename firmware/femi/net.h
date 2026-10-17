#pragma once
#include <Arduino.h>

// The link to the brain: Wi-Fi + one TCP connection, framed as described in PROTOCOL.md.
enum : uint8_t {
  F_HELLO = 0x01, F_AUDIO_START = 0x02, F_AUDIO_CHUNK = 0x03, F_AUDIO_END = 0x04, F_EVENT = 0x05,
  F_STATE = 0x81, F_SAY_START = 0x82, F_SAY_CHUNK = 0x83, F_SAY_END = 0x84, F_COMMAND = 0x85,
  F_IMAGE_CHUNK = 0x86, F_PING = 0x87,
};

void netBegin();                                // starts Wi-Fi and the link task (core 0)
bool netUp();                                   // connected to the brain right now
void netSend(uint8_t type, const char *json);   // queue a small control frame; safe from any task
