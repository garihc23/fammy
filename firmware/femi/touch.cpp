#include "touch.h"
#include "config.h"

// touchRead() rises when a finger is on the pad (about 32k -> 40k+ on this board).
static const float PRESS_DELTA = 5000, RELEASE_DELTA = 2500;   // release lower than press: no flicker
static const int PRESS_SAMPLES = 3;                            // above threshold this many samples in a row = a real press
static const uint32_t SAMPLE_MS = 5;

struct Event { bool press; uint32_t length; };
static QueueHandle_t events;
static volatile bool downNow = false;
static volatile uint32_t pressedAtNow = 0;
static float baseline = 0;

// runs on its own every SAMPLE_MS and posts press / release events
static void sampleTask(void *) {
  bool down = false;
  int above = 0;
  uint32_t pressedAt = 0;
  for (;;) {
    float v = touchRead(TOUCH_PIN);
    uint32_t now = millis();
    if (!down) {
      above = v > baseline + PRESS_DELTA ? above + 1 : 0;
      if (above >= PRESS_SAMPLES) {   // debounced: a single noisy sample can't fake a press
        down = true;
        pressedAt = now - (PRESS_SAMPLES - 1) * SAMPLE_MS;
        Event e = {true, 0};
        xQueueSend(events, &e, 0);
      } else if (above == 0) {
        baseline += (v - baseline) / 64;   // follow slow drift (another charger, humid weather) while untouched
      }
    } else if (v < baseline + RELEASE_DELTA) {
      down = false;
      Event e = {false, now - pressedAt};
      xQueueSend(events, &e, 0);
    }
    pressedAtNow = pressedAt;
    downNow = down;
    vTaskDelay(pdMS_TO_TICKS(SAMPLE_MS));
  }
}

void touchBegin() {
  uint32_t sum = 0;
  for (int i = 0; i < 16; i++) {
    sum += touchRead(TOUCH_PIN);
    delay(5);
  }
  baseline = sum / 16.0f;
  events = xQueueCreate(16, sizeof(Event));
  // core 0, below the speech feed task so audio always wins
  xTaskCreatePinnedToCore(sampleTask, "touch", 2048, nullptr, 4, nullptr, 0);
}

TouchInput touchPoll(uint32_t now) {
  TouchInput in = {};
  Event e;
  while (xQueueReceive(events, &e, 0) == pdTRUE) {
    if (e.press) in.tapped = true;
    else if (in.releases < 4) in.releasedMs[in.releases++] = min(e.length, (uint32_t)65535);
  }
  in.down = downNow;
  in.heldMs = in.down ? now - pressedAtNow : 0;
  return in;
}
