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

bool TapSensor::tapped() {
  if (!present_) return false;
  Wire.beginTransmission(TOUCH_I2C_ADDR);
  Wire.write(kRegTouchCount);
  if (Wire.endTransmission(false) != 0) return false;
  if (Wire.requestFrom(static_cast<uint8_t>(TOUCH_I2C_ADDR), static_cast<uint8_t>(1)) != 1) return false;

  bool down = (Wire.read() & 0x0F) > 0;
  bool released = down_ && !down;
  down_ = down;
  return released;
}
