#include <ESP_I2S.h>
#include <ESP_SR.h>
#include "speech.h"
#include "shared.h"
#include "voice.h"
#include "clips.h"
#include "net.h"

enum {
  CMD_HAPPY, CMD_SLEEP, CMD_WAKE, CMD_MUSIC, CMD_STOP, CMD_THINK, CMD_HACK, CMD_NORMAL,
  CMD_SET_TIMER, CMD_POMO, CMD_TCANCEL, CMD_TPAUSE, CMD_TRESUME, CMD_GAME, CMD_MORSE, CMD_HEY,
  CMD_NUM_FIRST   // number phrase k gets command id CMD_NUM_FIRST + k
};

// spoken numbers, in the same order as the number phrases in commands[]
static const int NUMBERS[] = {1, 2, 3, 5, 10, 15, 20, 30, 45, 60};

static const char *cmdNames[] = {
  "HAPPY", "SLEEP", "WAKE", "MUSIC", "STOP", "THINK", "HACK", "NORMAL",
  "SET TIMER", "POMODORO", "CANCEL", "PAUSE", "RESUME", "GAME", "MORSE", "HEY FAMYY"
};

// keep phrases short and distinct: long phrases and near-duplicates are missed or confused
static const sr_cmd_t commands[] = {
  {CMD_HAPPY,     "be happy"},
  {CMD_HAPPY,     "smile"},
  {CMD_SLEEP,     "go to sleep"},
  {CMD_WAKE,      "wake up"},
  {CMD_MUSIC,     "play music"},
  {CMD_STOP,      "stop"},
  {CMD_STOP,      "exit"},
  {CMD_STOP,      "quit"},
  {CMD_THINK,     "start thinking"},
  {CMD_HACK,      "hacking mode"},
  {CMD_HACK,      "hacker mode"},
  {CMD_NORMAL,    "normal mode"},
  {CMD_SET_TIMER, "set timer"},
  {CMD_SET_TIMER, "set a timer"},
  {CMD_SET_TIMER, "start timer"},
  {CMD_POMO,      "start pomodoro"},
  {CMD_POMO,      "pomodoro"},
  {CMD_TCANCEL,   "cancel timer"},
  {CMD_TCANCEL,   "cancel the timer"},
  {CMD_TCANCEL,   "stop timer"},
  {CMD_TPAUSE,    "pause timer"},
  {CMD_TPAUSE,    "pause the timer"},
  {CMD_TRESUME,   "resume timer"},
  {CMD_TRESUME,   "resume the timer"},
  {CMD_GAME,      "play game"},
  {CMD_GAME,      "play a game"},
  {CMD_MORSE,     "morse code"},
  {CMD_MORSE,     "learn morse"},
  {CMD_MORSE,     "teach me morse"},
  // the wake phrase, when HEY_FAMYY is on: the command recogniser listens for it all the time.
  // Spelled the way it sounds ("Fammy", like family): the recogniser works the sounds out from the spelling,
  // so each way people actually say the name gets its own spelling.
  {CMD_HEY,       "hey fammy"},
  {CMD_HEY,       "hi fammy"},
  {CMD_HEY,       "hey famy"},
  {CMD_HEY,       "hi famy"},
  {CMD_HEY,       "hey faimy"},
  {CMD_HEY,       "hi faimy"},
  {CMD_HEY,       "hey femy"},
  {CMD_HEY,       "hi femy"},
  {CMD_HEY,       "hey femmy"},
  {CMD_HEY,       "hi femmy"},
  // numbers: reusable answers to any "how much / how long" question
  {CMD_NUM_FIRST + 0, "one"},
  {CMD_NUM_FIRST + 1, "two"},
  {CMD_NUM_FIRST + 2, "three"},
  {CMD_NUM_FIRST + 3, "five"},
  {CMD_NUM_FIRST + 4, "ten"},
  {CMD_NUM_FIRST + 5, "fifteen"},
  {CMD_NUM_FIRST + 6, "twenty"},
  {CMD_NUM_FIRST + 7, "thirty"},
  {CMD_NUM_FIRST + 8, "forty five"},
  {CMD_NUM_FIRST + 9, "sixty"},
};

// mic input for the speech engine: the INMP441 sends 24-bit audio in 32-bit slots.
// Convert to the 16-bit the models want, with extra gain so normal speech isn't too quiet for them.
class MicWithGain : public I2SClass {
public:
  int shift = 14;   // 16 = plain conversion; each step lower doubles the volume (14 = 4x, +12 dB)
  size_t readBytes(char *buffer, size_t size) override {
    static int32_t raw[256];
    int16_t *out = (int16_t *)buffer;
    size_t want = size / 2, done = 0;
    while (done < want) {
      size_t chunk = min(want - done, (size_t)256);
      size_t got = I2SClass::readBytes((char *)raw, chunk * 4) / 4;
      if (got == 0) break;
      for (size_t i = 0; i < got; i++) out[done + i] = (int16_t)constrain(raw[i] >> shift, -32768, 32767);
      voiceFeed(out + done, got);   // also recorded for the brain while a request is being spoken
      done += got;
    }
    return done * 2;
  }
};

static MicWithGain mic;

// waiting for the wake phrase: the always-on command recogniser for "hey Famyy", or Espressif's "Hi ESP" model
static sr_mode_t idleMode() { return HEY_FAMYY ? SR_MODE_COMMAND : SR_MODE_WAKEWORD; }

void speechPause() { ESP_SR.setMode(SR_MODE_OFF); }   // stop listening while Femi records, thinks and talks
void speechRearm() { ESP_SR.setMode(idleMode()); }

// woken up: with the brain connected, record the request for it; offline, fall back to local commands
static void onWake() {
  Serial.println(">> wake");
  if (timerReq == REQ_NONE) timerReq = REQ_DISMISS;   // waking also silences a finished timer
  if (netUp() && !voiceRecording() && !playerBusy()) {
    speechPause();
    if (!voiceStart("wake")) speechRearm();
    return;
  }
  static uint32_t toldOffline = 0;
  if (!netUp() && (toldOffline == 0 || millis() - toldOffline > 60000)) {   // say so, at most once a minute
    toldOffline = millis();
    playerClip(CLIP_OFFLINE);
  }
  listening = true;
  wanted = LISTENING;
  ESP_SR.setMode(SR_MODE_COMMAND);
}

// runs in the speech task: only decide what to show and post requests, never draw here
static void onSrEvent(sr_event_t event, int command_id, int phrase_id) {
  switch (event) {
    case SR_EVENT_WAKEWORD:   // "Hi ESP" (only when HEY_FAMYY is off)
      onWake();
      break;
    case SR_EVENT_COMMAND:
      if (command_id == CMD_HEY) {
        onWake();
        break;
      }
      if (!listening) {   // heard a command without the wake phrase first: ignore it
        speechRearm();
        break;
      }
      if (command_id >= CMD_NUM_FIRST) {
        int n = NUMBERS[command_id - CMD_NUM_FIRST];
        Serial.printf(">> number: %d\n", n);
        if (asking == ASK_TIMER) {
          timerReq = n;
          asking = ASK_NONE;
          moodUntil = millis() + 1500; wanted = HAPPY;   // quick "got it"
        }
        // a number with no question pending is ignored
      } else {
        Serial.printf(">> command: %s\n", cmdNames[command_id]);
        asking = ASK_NONE;   // any other command drops an unanswered question
        switch (command_id) {
          case CMD_HAPPY:
          case CMD_MUSIC:     moodUntil = millis() + 3000; wanted = HAPPY; break;
          case CMD_THINK:     moodUntil = millis() + 5000; wanted = THINKING; break;
          case CMD_HACK:      hackerMode = true; moodUntil = millis() + 3000; wanted = HAPPY; break;
          case CMD_NORMAL:    hackerMode = false; wanted = IDLE; break;
          case CMD_SLEEP:     wanted = SLEEPING; break;
          case CMD_WAKE:      wanted = IDLE; break;
          case CMD_STOP:      wanted = IDLE; timerReq = REQ_DISMISS; appReq = APP_QUIT; break;
          case CMD_SET_TIMER: asking = ASK_TIMER; wanted = ASKING; break;
          case CMD_POMO:      timerReq = REQ_POMODORO; moodUntil = millis() + 1500; wanted = HAPPY; break;
          case CMD_TCANCEL:   timerReq = REQ_CANCEL; wanted = IDLE; break;
          case CMD_TPAUSE:    timerReq = REQ_PAUSE; break;
          case CMD_TRESUME:   timerReq = REQ_RESUME; break;
          case CMD_GAME:      appReq = APP_RUNNER; wanted = IDLE; break;
          case CMD_MORSE:     appReq = APP_MORSE; wanted = IDLE; break;
        }
      }
      ESP_SR.setMode(SR_MODE_COMMAND);   // follow-up window: keep listening
      break;
    case SR_EVENT_TIMEOUT:   // the command recogniser's window ran out: in HEY_FAMYY mode it simply starts again
      if (listening) Serial.println(">> timeout");
      listening = false;
      asking = ASK_NONE;
      if (wanted == LISTENING || wanted == ASKING) wanted = IDLE;
      speechRearm();
      break;
    default:
      break;
  }
}

void speechBegin() {
  mic.setPins(MIC_SCK, MIC_WS, -1, MIC_SD);
  mic.setTimeout(1000);
  mic.begin(I2S_MODE_STD, 16000, I2S_DATA_BIT_WIDTH_32BIT, I2S_SLOT_MODE_MONO, I2S_STD_SLOT_LEFT);
  ESP_SR.onEvent(onSrEvent);
  ESP_SR.begin(mic, commands, sizeof(commands) / sizeof(sr_cmd_t), SR_CHANNELS_MONO, idleMode(), "M");
}
