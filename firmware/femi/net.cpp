#include <WiFi.h>
#include "net.h"
#include "femi.h"
#include "voice.h"
#include "shared.h"
#include "config.h"
#include "fo.h"
#include "secrets.h"

// Small control frames go through a queue so any task can send; the link task is the only writer on the socket.
struct Ctrl {
  uint8_t type;
  char json[236];
};
static QueueHandle_t ctrlQ;
static WiFiClient cli;
static volatile bool up = false;

bool netUp() { return up; }

void netSend(uint8_t type, const char *json) {
  Ctrl c;
  c.type = type;
  strlcpy(c.json, json, sizeof c.json);
  xQueueSend(ctrlQ, &c, 0);
}

static void setUp(bool u) {
  if (up == u) return;
  up = u;
  femiLock();
  FemiView &v = femiView();
  v.linkUp = u;
  if (!u) {   // anything in flight is lost with the link
    v.busy = v.approve = false;
    v.foState = FO_OFF;
  }
  femiUnlock();
  Serial.println(u ? ">> brain connected" : ">> brain disconnected");
}

static bool writeFrame(uint8_t type, const void *p, uint32_t n) {
  uint8_t h[5] = {type, (uint8_t)n, (uint8_t)(n >> 8), (uint8_t)(n >> 16), (uint8_t)(n >> 24)};
  if (cli.write(h, 5) != 5) return false;
  return n == 0 || cli.write((const uint8_t *)p, n) == n;
}

static void sendMic() {
  static uint8_t buf[2048];
  size_t n = micRing().read(buf, sizeof buf);
  if (n) writeFrame(F_AUDIO_CHUNK, buf, n);
}

// ---------- incoming ----------

static uint8_t hdr[5];
static int hdrGot = 0;
static uint8_t curType;
static uint32_t remaining;
static char json[1024];
static uint32_t jsonGot;
static uint32_t imageAt, imageBytes;
static bool imageSnap;   // the image arriving is a camera snapshot (a polaroid), not a full-screen photo

static void resetParser() {
  hdrGot = 0;
  remaining = 0;
  jsonGot = 0;
}

static BrainFace parseFace(const char *s) {
  if (!strcmp(s, "thinking")) return BF_THINKING;
  if (!strcmp(s, "working")) return BF_WORKING;
  if (!strcmp(s, "happy")) return BF_HAPPY;
  if (!strcmp(s, "sad")) return BF_SAD;
  return BF_IDLE;
}

static void onCommand(const char *j) {
  char type[16] = "", s[32] = "";
  jsonStr(j, "type", type, sizeof type);
  uint32_t now = millis();
  if (!strcmp(type, "approve")) {
    femiLock();
    FemiView &v = femiView();
    v.approve = true;
    v.approveSince = now;
    jsonStr(j, "id", v.approveId, sizeof v.approveId);
    jsonStr(j, "text", v.approveText, sizeof v.approveText);
    femiUnlock();
    playerSfx(SFX_QUESTION);
  } else if (!strcmp(type, "reminder")) {
    femiLock();
    FemiView &v = femiView();
    v.remind = true;
    jsonStr(j, "id", v.remindId, sizeof v.remindId);
    jsonStr(j, "text", v.remindText, sizeof v.remindText);
    femiUnlock();
    playerSfx(SFX_BELL);
  } else if (!strcmp(type, "reminder_done")) {   // acknowledged by voice in a normal request: close its card
    char id[16] = "";
    jsonStr(j, "id", id, sizeof id);
    femiLock();
    FemiView &v = femiView();
    if (v.remind && (!id[0] || !strcmp(id, v.remindId))) v.remind = false;   // the loop then stops its bell and repeats
    femiUnlock();
  } else if (!strcmp(type, "image")) {   // {"type":"image","kind":"snap","w":112,"h":84,"bytes":...,"caption":...}
    char kind[12] = "";
    jsonStr(j, "kind", kind, sizeof kind);
    imageSnap = !strcmp(kind, "snap") && jsonInt(j, "w", 0) == SNAP_W && jsonInt(j, "h", 0) == SNAP_H;
    imageBytes = min((uint32_t)jsonInt(j, "bytes", 0), (uint32_t)(imageSnap ? SNAP_W * SNAP_H * 2 : PHOTO_W * PHOTO_H * 2));
    imageAt = 0;
    femiLock();
    FemiView &v = femiView();
    if (imageSnap) {   // the new one shows once all its pixels have arrived
      v.snap = false;
      jsonStr(j, "caption", v.snapCaption, sizeof v.snapCaption);
    } else {
      v.photo = false;
      jsonStr(j, "caption", v.photoCaption, sizeof v.photoCaption);
    }
    femiUnlock();
  } else if (!strcmp(type, "timer")) {
    long minutes = jsonInt(j, "minutes", 0);
    jsonStr(j, "action", s, sizeof s);
    if (minutes > 0) timerReq = minutes;
    else if (!strcmp(s, "cancel")) timerReq = REQ_CANCEL;
    else if (!strcmp(s, "pause")) timerReq = REQ_PAUSE;
    else if (!strcmp(s, "resume")) timerReq = REQ_RESUME;
    else if (!strcmp(s, "pomodoro")) timerReq = REQ_POMODORO;
  } else if (!strcmp(type, "app")) {
    jsonStr(j, "name", s, sizeof s);
    if (!strcmp(s, "game")) appReq = APP_RUNNER;
    if (!strcmp(s, "morse")) appReq = APP_MORSE;
  } else if (!strcmp(type, "list")) {   // {"type":"list","title":"Recent calls","items":"one|two|three"}
    femiLock();
    FemiView &v = femiView();
    jsonStr(j, "title", v.listTitle, sizeof v.listTitle);
    if (!jsonStr(j, "items", v.listItems, sizeof v.listItems)) v.listItems[0] = 0;
    v.list = true;
    v.listSince = now;
    femiUnlock();
    playerSfx(SFX_FLIP);
  } else if (!strcmp(type, "heard")) {   // the full transcript, for the thought cloud
    femiLock();
    FemiView &v = femiView();
    jsonStr(j, "text", v.heard, sizeof v.heard);
    v.heardSince = now;
    femiUnlock();
    playerSfx(SFX_POP);
  } else if (!strcmp(type, "fo")) {   // Fo's visit: {"type":"fo","state":"start","task":"call"}, then "done" with "ok"
    jsonStr(j, "state", s, sizeof s);
    femiLock();
    FemiView &v = femiView();
    if (!strcmp(s, "start") && v.foState != FO_WORKING) {   // a follow-up to Fo while it's here keeps the same visit
      char task[16] = "";
      jsonStr(j, "task", task, sizeof task);
      v.foState = FO_WORKING;
      v.foTask = foTaskFromName(task);
      v.foCalledAt = now;
    } else if (!strcmp(s, "done") && v.foState == FO_WORKING) {
      v.foState = jsonBool(j, "ok", false) ? FO_DONE : FO_FAILED;
      v.foSince = now;
    }
    femiUnlock();
  } else if (!strcmp(type, "theme")) {
    jsonStr(j, "name", s, sizeof s);
    hackerMode = !strcmp(s, "hacker");
  }
}

static void onControl(uint8_t type, const char *j) {
  uint32_t now = millis();
  switch (type) {
    case F_STATE: {
      char face[16] = "";
      jsonStr(j, "face", face, sizeof face);
      BrainFace f = parseFace(face);
      femiLock();
      FemiView &v = femiView();
      v.face = f;
      v.faceSince = now;
      if (!jsonStr(j, "line", v.line, sizeof v.line)) v.line[0] = 0;
      femiUnlock();
      if (f == BF_HAPPY) {
        moodUntil = now + 2500;
        wanted = HAPPY;
      }
      break;
    }
    case F_SAY_START: {
      char id[16] = "";
      jsonStr(j, "id", id, sizeof id);
      uint32_t bytes = jsonInt(j, "bytes", 0);
      femiLock();
      FemiView &v = femiView();
      jsonStr(j, "text", v.caption, sizeof v.caption);
      v.speaking = v.caption[0] || bytes;   // an empty SAY means "say nothing": it only ends the turn
      v.busy = true;
      v.speakSince = now;
      v.speakMs = bytes ? bytes / 32 : max(2500u, (unsigned)strlen(v.caption) * 70);   // 32 bytes per ms at 16 kHz
      femiUnlock();
      int listen = jsonBool(j, "listen", false) ? 1 : strstr(j, "\"listen\"") ? 0 : -1;   // -1: an older brain didn't say
      playerSayStart(id, bytes, listen);
      break;
    }
    case F_SAY_END:
      playerSayEnd();
      break;
    case F_COMMAND:
      onCommand(j);
      break;
    default:   // PING and anything unknown
      break;
  }
}

static void frameDone() {
  if (curType != F_SAY_CHUNK && curType != F_IMAGE_CHUNK) {
    json[min(jsonGot, (uint32_t)sizeof json - 1)] = 0;
    onControl(curType, json);
  }
  hdrGot = 0;
  jsonGot = 0;
}

// read whatever has arrived; returns true if it did anything
static bool pumpIncoming() {
  static uint8_t tmp[1024];
  bool did = false;
  while (cli.available() > 0) {
    if (hdrGot < 5) {
      int r = cli.read(hdr + hdrGot, 5 - hdrGot);
      if (r <= 0) break;
      hdrGot += r;
      did = true;
      if (hdrGot < 5) continue;
      curType = hdr[0];
      remaining = hdr[1] | (hdr[2] << 8) | (hdr[3] << 16) | ((uint32_t)hdr[4] << 24);
      if (remaining == 0) frameDone();
      continue;
    }
    size_t want = min((size_t)remaining, sizeof tmp);
    if (curType == F_SAY_CHUNK && !playerDiscarding()) {
      want = min(want, speakRing().space());
      if (want == 0) break;   // the speaker is behind: leave the rest in TCP, which slows the brain down
    }
    int r = cli.read(tmp, want);
    if (r <= 0) break;
    did = true;
    remaining -= r;
    if (curType == F_SAY_CHUNK) {
      if (!playerDiscarding()) speakRing().write(tmp, r);   // after a stop, the rest of that reply is dropped
    } else if (curType == F_IMAGE_CHUNK) {
      uint32_t room = imageBytes > imageAt ? imageBytes - imageAt : 0;
      uint32_t n = min((uint32_t)r, room);
      memcpy((uint8_t *)(imageSnap ? femiSnap() : femiPhoto()) + imageAt, tmp, n);
      imageAt += n;
      if (imageBytes && imageAt >= imageBytes) {
        imageBytes = 0;
        femiLock();
        if (imageSnap) {
          femiView().snap = true;
          femiView().snapSince = millis();
        } else {
          femiView().photo = true;
          femiView().photoSince = millis();
        }
        femiUnlock();
        playerSfx(SFX_SHUTTER);
      }
    } else {
      uint32_t n = min((uint32_t)r, (uint32_t)sizeof json - 1 - jsonGot);
      memcpy(json + jsonGot, tmp, n);
      jsonGot += n;
    }
    if (remaining == 0) frameDone();
  }
  return did;
}

// ---------- the link task ----------

static void netTask(void *) {
  WiFi.mode(WIFI_STA);
  WiFi.setSleep(false);   // power saving adds lag to every reply
  WiFi.begin(WIFI_SSID, WIFI_PASSWORD);
  bool announcedWifi = false;
  for (;;) {
    if (WiFi.status() != WL_CONNECTED) {
      setUp(false);
      announcedWifi = false;
      vTaskDelay(pdMS_TO_TICKS(500));
      continue;
    }
    if (!announcedWifi) {
      configTzTime("IST-5:30", "pool.ntp.org", "time.google.com");   // the clock: India time, from the internet
      Serial.printf(">> wifi up, my address %s, brain at %s:%d\n", WiFi.localIP().toString().c_str(), BRAIN_HOST, BRAIN_PORT);
      announcedWifi = true;
    }
    if (!cli.connected()) {
      setUp(false);
      cli.stop();
      resetParser();
      if (!cli.connect(BRAIN_HOST, BRAIN_PORT, 2000)) {
        vTaskDelay(pdMS_TO_TICKS(2000));
        continue;
      }
      cli.setNoDelay(true);
      const char *hello = "{\"device\":\"famyy\",\"fw\":\"0.1\"}";
      writeFrame(F_HELLO, hello, strlen(hello));
      setUp(true);
    }

    bool did = false;
    Ctrl c;
    while (xQueueReceive(ctrlQ, &c, 0) == pdTRUE) {
      if (c.type == F_AUDIO_END) {
        while (micRing().available()) sendMic();   // every recorded byte goes before its end marker
      }
      writeFrame(c.type, c.json, strlen(c.json));
      did = true;
    }
    if (micRing().available() >= 2048 || (!voiceRecording() && micRing().available())) {
      sendMic();
      did = true;
    }
    if (pumpIncoming()) did = true;
    if (!did) vTaskDelay(pdMS_TO_TICKS(2));
  }
}

void netBegin() {
  ctrlQ = xQueueCreate(24, sizeof(Ctrl));
  xTaskCreatePinnedToCore(netTask, "net", 6144, nullptr, 3, nullptr, 0);
}
