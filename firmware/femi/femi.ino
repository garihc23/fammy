// DeskBuddy firmware (Arduino).
// Tools menu: Arduino Runs On = Core 0 (speech recognition owns core 1; on the same core the screen halves its frame rate)
//   config.h   pins, constants, shared enums
//   shared.h   the mailbox between the speech task and loop()
//   speech.*   wake word + voice commands (ESP-SR)
//   timer.*    countdown and pomodoro
//   face.*     the display: terminal creature, moods, animations
//   settings.* values kept across power-offs
//   touch.*    the capacitive touch pad
//   app.h      full-screen apps: runner.cpp (game), morse.cpp (morse teacher)
#include "config.h"
#include "shared.h"
#include "speech.h"
#include "timer.h"
#include "face.h"
#include "settings.h"
#include "touch.h"
#include "app.h"

volatile Face wanted = IDLE;
volatile bool listening = false;
volatile bool hackerMode = false;
volatile uint32_t moodUntil = 0;
volatile int timerReq = REQ_NONE;
volatile Ask asking = ASK_NONE;
volatile int appReq = APP_NONE;

void setup() {
  Serial.begin(115200);
  settingsBegin();   // first, so the face starts with the saved theme
  touchBegin();      // measures the untouched pad: keep fingers off while it starts
  faceBegin();
  speechBegin();
  randomSeed(esp_random());
}

void loop() {
  uint32_t now = millis();
  TouchInput touch = touchPoll(now);   // read every loop, so an old tap can't leak into a new app

  // take the pending timer request, if any, and let the timer advance
  int req = timerReq;
  if (req != REQ_NONE) timerReq = REQ_NONE;
  timerUpdate(now, req);
  settingsUpdate();

  // apps (game, morse teacher): started and stopped by voice, used with the touch pad
  static const App *app = nullptr;
  int a = appReq;
  if (a != APP_NONE) appReq = APP_NONE;
  if (a == APP_RUNNER) app = &RUNNER_APP;
  if (a == APP_MORSE)  app = &MORSE_APP;
  if (a > APP_NONE) app->start(now);
  if (a == APP_QUIT || timerPhase() == T_DONE) app = nullptr;   // a finished timer interrupts any app
  if (app && !app->update(now, touch)) app = nullptr;

  // holding the pad for 2 s leaves any app; the bar appears after the first 0.8 s
  float exitHold = 0;
  if (app && touch.down && touch.heldMs > EXIT_SHOW_MS) {
    exitHold = (touch.heldMs - EXIT_SHOW_MS) / float(EXIT_HOLD_MS - EXIT_SHOW_MS);
    if (exitHold >= 1) app = nullptr;
  }

  // doze off after a long quiet spell; "Hi ESP" wakes it as usual
  static uint32_t lastActive = 0;
  if (app || listening || timerPhase() != T_OFF || wanted != IDLE) lastActive = now;
  else if (now - lastActive > DOZE_MIN * 60000UL) wanted = SLEEPING;

  if (app) {
    faceAppFrame(now, *app, exitHold);
    delay(5);
    return;
  }

  // timed moods fall back to listening or idle
  Face f = wanted;
  if ((f == HAPPY || f == THINKING) && now > moodUntil) {
    wanted = listening ? LISTENING : IDLE;
    f = wanted;
  }
  if (timerPhase() == T_DONE) f = ALARM;   // a finished timer beats every mood
  else if (f == IDLE && timerPhase() == T_FOCUS && !timerPaused()) f = FOCUS;

  faceFrame(now, f);
  delay(10);   // give the speech tasks room to breathe
}
