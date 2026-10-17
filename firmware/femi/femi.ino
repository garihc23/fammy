// Femi: a family agent's body. Claude thinks (the brain on the laptop), Fo acts in the world, Femi listens, shows and talks.
// Tools menu: Arduino Runs On = Core 0 (speech recognition owns core 1; on the same core the screen halves its frame rate)
//   config.h   pins, constants, shared enums
//   shared.h   the mailbox between the speech task and loop()
//   speech.*   wake phrase ("hey femi") and offline voice commands (ESP-SR)
//   voice.*    recording requests for the brain, playing its replies
//   net.*      Wi-Fi link to the brain (see PROTOCOL.md)
//   femi.*     what the brain has asked Femi to show
//   timer.*    countdown and pomodoro
//   face.*     the display: terminal creature, moods, cards, photos
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
#include "femi.h"
#include "voice.h"
#include "net.h"

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
  femiBegin();
  faceBegin();
  playerBegin();
  netBegin();
  speechBegin();
  randomSeed(esp_random());
}

// the caption a screenful at a time (whole words, ~20 characters), in step with the audio
static void captionPage(const FemiView &v, uint32_t now, char *out, size_t n) {
  const int PAGE = 20;
  const char *starts[24];
  int pages = 0;
  for (const char *p = v.caption; *p && pages < 24;) {
    while (*p == ' ') p++;
    if (!*p) break;
    starts[pages++] = p;
    int len = strlen(p), cut = min(len, PAGE);
    if (len > PAGE) {
      int sp = cut;
      while (sp > 0 && p[sp] != ' ') sp--;
      if (sp > 0) cut = sp;
    }
    p += cut;
  }
  if (pages == 0) {
    out[0] = 0;
    return;
  }
  int page = min(pages - 1, (int)((uint64_t)(now - v.speakSince) * pages / max(v.speakMs, (uint32_t)1)));
  const char *s = starts[page];
  int len = page + 1 < pages ? starts[page + 1] - s : strlen(s);
  snprintf(out, n, "%.*s", min(len, PAGE), s);
}

static void clearFlag(bool FemiView::*flag) {
  femiLock();
  femiView().*flag = false;
  femiUnlock();
}

void loop() {
  uint32_t now = millis();
  TouchInput touch = touchPoll(now);   // read every loop, so an old tap can't leak into something new
  FemiView v;
  femiLock();
  v = femiView();
  femiUnlock();

  // take the pending timer request, if any, and let the timer advance
  int req = timerReq;
  if (req != REQ_NONE) timerReq = REQ_NONE;
  timerUpdate(now, req);
  settingsUpdate();
  if (timerPhase() == T_DONE && touch.tapped) timerReq = REQ_DISMISS;   // a tap silences a finished timer

  // ---------- what the brain asked for comes first ----------

  // a photo, until tapped or 20 s
  static uint32_t photoOnScreen = 0;   // arrival time of the photo being shown
  if (v.photo) {
    if (touch.tapped || now - v.photoSince > 20000) {
      clearFlag(&FemiView::photo);
      photoOnScreen = 0;
      faceRepaint();
      touch.tapped = false;
    } else {
      facePhotoFrame(now, femiPhoto(), v.photoCaption, photoOnScreen != v.photoSince);
      photoOnScreen = v.photoSince;
      delay(20);
      return;
    }
  } else if (photoOnScreen) {
    photoOnScreen = 0;
    faceRepaint();
  }

  // a reminder, until tapped
  if (v.remind) {
    if (touch.tapped) {
      char j[64];
      snprintf(j, sizeof j, "{\"type\":\"ack\",\"id\":\"%s\"}", v.remindId);
      netSend(F_EVENT, j);
      clearFlag(&FemiView::remind);
      touch.tapped = false;
    } else {
      faceCardFrame(now, true, "Reminder", v.remindText, "tap when done", 0);
      delay(10);
      return;
    }
  }

  // an approval: tap = yes, hold 2 s = no, 60 s untouched = no answer
  if (v.approve) {
    int answer = -1;
    for (int i = 0; i < touch.releases; i++) {
      if (touch.releasedMs[i] < EXIT_SHOW_MS) answer = 1;
    }
    float hold = touch.down && touch.heldMs > 300 ? (touch.heldMs - 300) / float(EXIT_HOLD_MS - 300) : 0;
    if (hold >= 1) answer = 0;
    if (answer >= 0 || now - v.approveSince > 60000) {
      if (answer >= 0) {
        char j[80];
        snprintf(j, sizeof j, "{\"type\":\"approval\",\"id\":\"%s\",\"ok\":%s}", v.approveId, answer ? "true" : "false");
        netSend(F_EVENT, j);
        playerChime(answer == 1);
      }
      clearFlag(&FemiView::approve);
      touch.tapped = false;
    } else {
      faceCardFrame(now, false, "OK to do this?", v.approveText, "tap=yes  hold=no", hold);
      delay(10);
      return;
    }
  }

  // ---------- apps (game, morse teacher) ----------
  static const App *app = nullptr;
  int a = appReq;
  if (a != APP_NONE) appReq = APP_NONE;
  if (a == APP_RUNNER) app = &RUNNER_APP;
  if (a == APP_MORSE)  app = &MORSE_APP;
  if (a > APP_NONE) app->start(now);
  if (a == APP_QUIT || timerPhase() == T_DONE || v.speaking) app = nullptr;   // a finished timer or Femi talking ends any app
  if (app && !app->update(now, touch)) app = nullptr;

  // holding the pad for 2 s leaves any app; the bar appears after the first 0.8 s
  float exitHold = 0;
  if (app && touch.down && touch.heldMs > EXIT_SHOW_MS) {
    exitHold = (touch.heldMs - EXIT_SHOW_MS) / float(EXIT_HOLD_MS - EXIT_SHOW_MS);
    if (exitHold >= 1) app = nullptr;
  }

  // ---------- conversation ----------
  bool busy = v.busy || voiceRecording() || playerBusy();
  static bool wasBusy = false;
  static uint32_t busySince = 0;
  if (busy && !wasBusy) busySince = now;
  // the brain went quiet for 2 minutes in the middle of a request: give up on it
  if (v.busy && !v.speaking && !voiceRecording() && now - max(busySince, v.faceSince) > 120000) {
    clearFlag(&FemiView::busy);
    busy = false;
  }
  if (wasBusy && !busy) speechRearm();   // done answering: listen for "hey femi" again
  wasBusy = busy;

  // tap to talk, when nothing else is going on
  if (!app && touch.tapped && !busy && v.linkUp && timerPhase() != T_DONE) {
    speechPause();
    if (!voiceStart("tap")) speechRearm();
  }

  // doze off after a long quiet spell; "hey femi" wakes it as usual
  static uint32_t lastActive = 0;
  if (app || busy || listening || timerPhase() != T_OFF || wanted != IDLE) lastActive = now;
  else if (now - lastActive > DOZE_MIN * 60000UL) wanted = SLEEPING;

  if (app) {
    faceAppFrame(now, *app, exitHold);
    delay(5);
    return;
  }

  // ---------- the face ----------
  Face f = wanted;
  if ((f == HAPPY || f == THINKING) && now > moodUntil) {   // timed moods fall back to listening or idle
    wanted = listening ? LISTENING : IDLE;
    f = wanted;
  }
  char termText[48];
  const char *tt = nullptr;
  if (timerPhase() == T_DONE) {
    f = ALARM;   // a finished timer beats everything
  } else if (voiceRecording()) {
    f = LISTENING;
  } else if (v.speaking) {
    f = TALKING;
    captionPage(v, now, termText, sizeof termText);
    tt = termText;
  } else if (v.busy) {
    f = v.face == BF_WORKING ? WORKING : THINKING;
    if (v.line[0]) {
      strlcpy(termText, v.line, sizeof termText);
      tt = termText;
    }
  } else if (f == IDLE && timerPhase() == T_FOCUS && !timerPaused()) {
    f = FOCUS;
  }
  faceFrame(now, f, tt);
  delay(10);   // give the speech tasks room to breathe
}
