#include "touch.h"

#include <Arduino.h>
#include <Wire.h>

#include "board.h"

namespace {
constexpr uint8_t kRegTouchCount = 0x02;  // TD_STATUS: number of touch points
}

bool TapSensor::begin() {
  pinMode(PIN_TOUCH_RST, OUTPUT);
  digitalWrite(PIN_TOUCH_RST, LOW);
  delay(10);
  digitalWrite(PIN_TOUCH_RST, HIGH);
  delay(300);  // FT6336 needs ~300 ms after reset before it answers on I2C

  Wire.begin(PIN_TOUCH_SDA, PIN_TOUCH_SCL, 400000);
  Wire.beginTransmission(TOUCH_I2C_ADDR);
  present_ = Wire.endTransmission() == 0;
  return present_;
}

bool TapSensor::take(int& x, int& y) {
  if (!present_) return false;
  Wire.beginTransmission(TOUCH_I2C_ADDR);
  Wire.write(kRegTouchCount);
  if (Wire.endTransmission(false) != 0) return false;
  // TD_STATUS, then the first point's X and Y, each 12 bits across two bytes.
  if (Wire.requestFrom(static_cast<uint8_t>(TOUCH_I2C_ADDR), static_cast<uint8_t>(5)) != 5) return false;
  uint8_t raw[5];
  for (uint8_t& b : raw) b = static_cast<uint8_t>(Wire.read());

  bool down = (raw[0] & 0x0F) > 0;
  if (down) {
    lastX_ = ((raw[1] & 0x0F) << 8) | raw[2];
    lastY_ = ((raw[3] & 0x0F) << 8) | raw[4];
    havePoint_ = true;
  }
  bool released = down_ && !down && havePoint_;
  down_ = down;
  if (!released) return false;
  x = lastX_;
  y = lastY_;
  return true;
}
