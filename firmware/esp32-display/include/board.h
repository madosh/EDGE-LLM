// Pin map of the ESP32-S3 2.8" display board (LCDWIKI ES3C28P / diymore
// "Xiaozhi" display), from the vendor's pin table and Xiaozhi's
// freenove-esp32s3-display-2.8-lcd board config.
#pragma once

// ILI9341V panel, 4-wire SPI. Its reset is tied to the ESP32's EN pin.
#define PIN_LCD_SCLK 12
#define PIN_LCD_MOSI 11
#define PIN_LCD_MISO 13
#define PIN_LCD_CS 10
#define PIN_LCD_DC 46
#define PIN_LCD_BL 45  // backlight, active high

// FT6336G capacitive touch, I2C (shared with the ES8311 audio codec).
#define PIN_TOUCH_SDA 16
#define PIN_TOUCH_SCL 15
#define PIN_TOUCH_RST 18
#define PIN_TOUCH_INT 17
#define TOUCH_I2C_ADDR 0x38

#define PIN_RGB_LED 42     // one WS2812-style addressable LED
#define PIN_BOOT_BUTTON 0  // low while pressed
#define PIN_AUDIO_EN 1     // speaker amplifier, low = on

#define LCD_WIDTH 240
#define LCD_HEIGHT 320
