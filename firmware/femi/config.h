#pragma once
#include <Arduino.h>

// ---------- pins (see the wiring plan) ----------
const int PIN_SCK = 12, PIN_MOSI = 11, PIN_MISO = 13;   // SPI bus, shared by display and SD card
const int TFT_CS = 10, TFT_DC = 9, TFT_RST = 8, TFT_BLK = 14;
const int SD_CS = 21;
const int MIC_SCK = 4, MIC_WS = 5, MIC_SD = 6;           // I2S bus 0: INMP441 mic
const int TOUCH_PIN = 2;                                 // capacitive touch pad
const int AMP_BCLK = 15, AMP_LRC = 16, AMP_DIN = 7;      // I2S bus 1: MAX98357 amp

// ---------- Femi ----------
const bool HEY_FEMI = true;          // wake phrase "hey femi" (always-on command recogniser); false = Espressif's "Hi ESP"
const float SPEAKER_VOLUME = 0.6f;   // 0..1 of full scale: the amp is loud

// display speed: 10 MHz is safe on long jumper wires; try 20-40 MHz once the wires are short
const uint32_t TFT_SPI_HZ = 40000000;
const bool PROFILE = false;   // print where each app frame's time goes, every 2 s, to the Serial Monitor

// ---------- timer ----------
const uint32_t FOCUS_MIN = 25, BREAK_MIN = 5;   // set both to 1 to test a whole pomodoro quickly

// ---------- behaviour ----------
const uint32_t DOZE_MIN = 10;   // fall asleep after this long with nothing happening
const uint32_t EXIT_SHOW_MS = 800, EXIT_HOLD_MS = 2000;   // hold the touch pad this long to leave an app

// what the face shows
enum Face { IDLE, LISTENING, HAPPY, THINKING, SLEEPING, FOCUS, ALARM, ASKING, TALKING, WORKING };

// which question a spoken number answers
enum Ask { ASK_NONE, ASK_TIMER };

// app requests posted by the speech task
enum { APP_QUIT = -1, APP_NONE = 0, APP_RUNNER = 1, APP_MORSE = 2 };

// timer requests posted by the speech task: positive = minutes, negative = actions
enum { REQ_NONE = 0, REQ_CANCEL = -1, REQ_POMODORO = -2, REQ_PAUSE = -3, REQ_RESUME = -4, REQ_DISMISS = -5 };
