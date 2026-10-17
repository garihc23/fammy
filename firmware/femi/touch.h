#pragma once
#include <Arduino.h>

// Capacitive touch pad: any bare metal on TOUCH_PIN works as a button.
// It is sampled in its own task every few ms, so press lengths are accurate
// no matter how slowly the screen redraws.
struct TouchInput {
  bool tapped;               // a press started since the last poll
  bool down;                 // finger on the pad now
  uint32_t heldMs;           // how long the current press has lasted
  uint8_t releases;          // presses that ended since the last poll, oldest first
  uint16_t releasedMs[4];    // their lengths
};

void touchBegin();                    // learn the untouched level and start sampling; keep fingers off while it starts
TouchInput touchPoll(uint32_t now);   // call once per loop
