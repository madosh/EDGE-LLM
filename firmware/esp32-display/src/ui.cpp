#include "ui.h"

#define LGFX_USE_V1
#include <LovyanGFX.hpp>

#include "board.h"

namespace {

// ── Panel driver ─────────────────────────────────────────────────────────────

class Lcd : public lgfx::LGFX_Device {
 public:
  Lcd() {
    {
      auto cfg = bus_.config();
      cfg.spi_host = SPI3_HOST;
      cfg.spi_mode = 0;
      cfg.freq_write = 20000000;  // the clock Xiaozhi's driver for this panel uses
      cfg.freq_read = 16000000;
      cfg.spi_3wire = false;
      cfg.use_lock = true;
      cfg.dma_channel = SPI_DMA_CH_AUTO;
      cfg.pin_sclk = PIN_LCD_SCLK;
      cfg.pin_mosi = PIN_LCD_MOSI;
      cfg.pin_miso = PIN_LCD_MISO;
      cfg.pin_dc = PIN_LCD_DC;
      bus_.config(cfg);
      panel_.setBus(&bus_);
    }
    {
      auto cfg = panel_.config();
      cfg.pin_cs = PIN_LCD_CS;
      cfg.pin_rst = -1;  // shared with EN: the panel resets with the chip
      cfg.pin_busy = -1;
      cfg.panel_width = LCD_WIDTH;
      cfg.panel_height = LCD_HEIGHT;
      cfg.offset_x = 0;
      cfg.offset_y = 0;
      cfg.offset_rotation = 0;
      cfg.readable = true;
      cfg.invert = true;      // this IPS panel shows inverted colors otherwise
      cfg.rgb_order = false;  // flip if red and blue come out swapped
      cfg.dlen_16bit = false;
      cfg.bus_shared = false;
      panel_.config(cfg);
    }
    {
      auto cfg = light_.config();
      cfg.pin_bl = PIN_LCD_BL;
      cfg.invert = false;
      cfg.freq = 44100;
      cfg.pwm_channel = 7;
      light_.config(cfg);
      panel_.setLight(&light_);
    }
    setPanel(&panel_);
  }

 private:
  lgfx::Panel_ILI9341 panel_;
  lgfx::Bus_SPI bus_;
  lgfx::Light_PWM light_;
};

Lcd lcd;

// ── Look ─────────────────────────────────────────────────────────────────────
// 0xRRGGBB; LovyanGFX reads a uint32_t color as RGB888. Same palette as the
// web dashboard: sky blue for the control plane, orange for edge devices.

constexpr uint32_t kBg = 0x0B1020;
constexpr uint32_t kPanel = 0x151C2F;
constexpr uint32_t kText = 0xE5E7EB;
constexpr uint32_t kMuted = 0x94A3B8;
constexpr uint32_t kCloud = 0x38BDF8;
constexpr uint32_t kEdge = 0xF97316;
constexpr uint32_t kGreen = 0x22C55E;
constexpr uint32_t kAmber = 0xF59E0B;
constexpr uint32_t kRed = 0xEF4444;
constexpr uint32_t kGray = 0x64748B;

constexpr int kMargin = 10;
constexpr int kContentWidth = LCD_WIDTH - 2 * kMargin;
constexpr int kHeaderHeight = 32;
constexpr int kFooterTop = LCD_HEIGHT - 24;

const lgfx::IFont* const kFontTitle = &lgfx::fonts::FreeSansBold12pt7b;
const lgfx::IFont* const kFontBold = &lgfx::fonts::FreeSansBold9pt7b;
const lgfx::IFont* const kFontBody = &lgfx::fonts::Font2;
const lgfx::IFont* const kFontNumber = &lgfx::fonts::Font4;

uint32_t statusColor(const String& status) {
  if (status == "running") return kGreen;
  if (status == "failed") return kRed;
  if (status == "skipped" || status == "rolled_back") return kGray;
  return kAmber;  // pending, downloading, verifying
}

String statusWord(const String& status) {
  if (status == "rolled_back") return "ROLLED BACK";
  String word = status;
  word.toUpperCase();
  return word;
}

// Text cut to fit width, with "..." when it was cut.
String fit(const String& text, int width) {
  if (lcd.textWidth(text.c_str()) <= width) return text;
  String cut = text;
  while (cut.length() > 0 && lcd.textWidth((cut + "...").c_str()) > width) {
    cut.remove(cut.length() - 1);
  }
  return cut + "...";
}

// Greedy word wrap at the current font. Returns the y below the last line.
int drawWrapped(const String& text, int x, int y, int width, int maxLines) {
  const int lineHeight = lcd.fontHeight() + 2;
  String line;
  int lines = 0;
  unsigned int i = 0;
  while (i <= text.length() && lines < maxLines) {
    int space = text.indexOf(' ', i);
    if (space < 0) space = text.length();
    String word = text.substring(i, space);
    String candidate = line.length() > 0 ? line + " " + word : word;
    if (line.length() == 0 || lcd.textWidth(candidate.c_str()) <= width) {
      line = candidate;
      i = space + 1;
      continue;
    }
    lcd.drawString(line.c_str(), x, y);
    y += lineHeight;
    lines++;
    line = "";
  }
  if (line.length() > 0 && lines < maxLines) {
    lcd.drawString(fit(line, width).c_str(), x, y);
    y += lineHeight;
  }
  return y;
}

void caption(const char* text, int y) {
  lcd.setFont(kFontBody);
  lcd.setTextColor(kMuted);
  lcd.setTextDatum(lgfx::textdatum_t::top_left);
  lcd.drawString(text, kMargin, y);
}

// A rounded box with a big number and a label under it.
void statBox(int x, int y, int w, int h, int value, const char* label, uint32_t color) {
  lcd.fillRoundRect(x, y, w, h, 6, kPanel);
  lcd.setTextDatum(lgfx::textdatum_t::top_center);
  lcd.setFont(kFontNumber);
  lcd.setTextColor(color);
  lcd.drawString(String(value).c_str(), x + w / 2, y + 6);
  lcd.setFont(kFontBody);
  lcd.setTextColor(kMuted);
  lcd.drawString(label, x + w / 2, y + h - 20);
}

String ago(uint32_t sinceMs) {
  uint32_t s = (millis() - sinceMs) / 1000;
  if (s < 90) return String(s) + " s ago";
  return String(s / 60) + " min ago";
}

}  // namespace

// ── Ui ───────────────────────────────────────────────────────────────────────

void Ui::begin(const String& deviceName, const String& controlPlane) {
  deviceName_ = deviceName;
  controlPlane_ = controlPlane;
  lcd.init();
  lcd.setRotation(0);  // portrait, USB-C at the bottom
  lcd.setBrightness(200);
  lcd.fillScreen(kBg);
}

void Ui::splash(const char* line) {
  lcd.fillScreen(kBg);
  lcd.setTextDatum(lgfx::textdatum_t::middle_center);
  lcd.setFont(kFontTitle);
  lcd.setTextColor(kCloud);
  lcd.drawString("CAMI FLEET", LCD_WIDTH / 2, 130);
  lcd.setFont(kFontBody);
  lcd.setTextColor(kEdge);
  lcd.drawString("edge status display", LCD_WIDTH / 2, 158);
  lcd.setTextColor(kMuted);
  lcd.drawString(line, LCD_WIDTH / 2, 200);
  drawn_ = "";
  footerDrawn_ = "";
}

void Ui::render(const Snapshot& snap, View view) {
  const FleetState& st = snap.state;
  String sig = String(static_cast<int>(view)) + "|" + static_cast<int>(snap.link) + "|" + snap.error + "|" +
               snap.ip + "|" + st.valid + "|" + st.deviceName + "|" + st.deviceStatus + "|" + st.currentModel +
               "|" + st.labels + "|" + st.devicesOnline + "|" + st.devicesOffline + "|" +
               st.deploymentsActive + "|" + st.deploymentsCompleted + "|" + st.deploymentsFailed + "|" +
               st.deploymentsRolledBack + "|" + st.assignmentCount;
  for (int i = 0; i < st.assignmentCount; i++) {
    sig += "|" + st.assignments[i].deploymentId + st.assignments[i].status + st.assignments[i].errorMsg;
  }

  if (sig != drawn_) {
    lcd.startWrite();
    lcd.fillScreen(kBg);
    drawHeader(view);
    drawLinkPill(snap);
    if (snap.link != Link::Online || !st.valid) {
      drawProblem(snap);
    } else if (view == View::Device) {
      drawDeviceView(snap);
    } else {
      drawFleetView(snap);
    }
    lcd.endWrite();
    drawn_ = sig;
    footerDrawn_ = "";
  }
  drawFooter(snap);
}

void Ui::drawHeader(View view) {
  lcd.fillRect(0, 0, LCD_WIDTH, kHeaderHeight, kPanel);
  lcd.setFont(kFontBold);
  lcd.setTextDatum(lgfx::textdatum_t::middle_left);
  lcd.setTextColor(kCloud);
  lcd.drawString("CAMI FLEET", kMargin, kHeaderHeight / 2);

  // Two dots: which of the two views is showing. Tap to switch.
  int x = LCD_WIDTH - kMargin - 22;
  lcd.fillCircle(x, kHeaderHeight / 2, 4, view == View::Device ? kEdge : kGray);
  lcd.fillCircle(x + 16, kHeaderHeight / 2, 4, view == View::Fleet ? kEdge : kGray);
}

void Ui::drawLinkPill(const Snapshot& snap) {
  const char* text = "CONNECTING WI-FI";
  uint32_t color = kAmber;
  switch (snap.link) {
    case Link::WifiConnecting:
      break;
    case Link::Registering:
      text = "REGISTERING";
      break;
    case Link::Online:
      // The board reached the control plane just now, so this is what the
      // control plane recorded for it, not a guess.
      if (snap.state.valid && snap.state.deviceStatus == "offline") {
        text = "OFFLINE";
        color = kRed;
      } else {
        text = "ONLINE";
        color = kGreen;
      }
      break;
    case Link::Unreachable:
      text = "UNREACHABLE";
      color = kRed;
      break;
  }
  const int y = kHeaderHeight + 10;
  lcd.fillRoundRect(kMargin, y, kContentWidth, 34, 8, color);
  lcd.setFont(kFontTitle);
  lcd.setTextDatum(lgfx::textdatum_t::middle_center);
  lcd.setTextColor(kBg);
  lcd.drawString(text, LCD_WIDTH / 2, y + 17);
}

void Ui::drawProblem(const Snapshot& snap) {
  int y = 96;
  lcd.setTextDatum(lgfx::textdatum_t::top_left);
  lcd.setFont(kFontBold);
  lcd.setTextColor(kText);
  lcd.drawString(fit(deviceName_, kContentWidth).c_str(), kMargin, y);
  y += 28;

  caption("Wi-Fi", y);
  lcd.setTextColor(kText);
  lcd.drawString(fit(snap.ip.length() > 0 ? snap.ip : String("not connected"), 150).c_str(), 80, y);
  y += 20;
  caption("Control plane", y);
  lcd.setTextColor(kText);
  lcd.drawString(fit(controlPlane_, 130).c_str(), 100, y);
  y += 30;

  if (snap.error.length() > 0) {
    caption("Last error", y);
    y += 20;
    lcd.setTextColor(kRed);
    drawWrapped(snap.error, kMargin, y, kContentWidth, 6);
  }
}

void Ui::drawDeviceView(const Snapshot& snap) {
  const FleetState& st = snap.state;
  int y = 96;
  lcd.setTextDatum(lgfx::textdatum_t::top_left);
  lcd.setFont(kFontTitle);
  lcd.setTextColor(kText);
  lcd.drawString(fit(st.deviceName, kContentWidth).c_str(), kMargin, y);
  y += 30;

  lcd.setFont(kFontBody);
  lcd.setTextColor(kEdge);
  y = drawWrapped(st.labels.length() > 0 ? st.labels : String("no labels"), kMargin, y, kContentWidth, 2);

  y = max(y + 6, 166);
  lcd.drawFastHLine(kMargin, y, kContentWidth, kPanel);
  y += 8;
  caption("NEWEST DEPLOYMENT FOR THIS DEVICE", y);
  y += 20;

  if (st.assignmentCount == 0) {
    lcd.setFont(kFontBody);
    lcd.setTextColor(kMuted);
    drawWrapped("No deployment targets this device yet. Deploy with a selector that matches its labels.",
                kMargin, y, kContentWidth, 4);
    return;
  }

  const Assignment& newest = st.assignments[0];
  lcd.setFont(kFontTitle);
  lcd.setTextColor(kText);
  lcd.drawString(fit(newest.modelId, kContentWidth).c_str(), kMargin, y);
  y += 30;

  lcd.setFont(kFontBold);
  lcd.setTextColor(statusColor(newest.status));
  lcd.drawString(statusWord(newest.status).c_str(), kMargin, y);
  y += 24;

  if (newest.errorMsg.length() > 0) {
    lcd.setFont(kFontBody);
    lcd.setTextColor(kText);
    y = drawWrapped(newest.errorMsg, kMargin, y, kContentWidth, 3);
  }
  if (st.assignmentCount > 1 && y < kFooterTop - 18) {
    lcd.setFont(kFontBody);
    lcd.setTextColor(kMuted);
    lcd.drawString((String("+") + (st.assignmentCount - 1) + " earlier deployment(s)").c_str(), kMargin,
                   kFooterTop - 18);
  }
}

void Ui::drawFleetView(const Snapshot& snap) {
  const FleetState& st = snap.state;
  const int gap = 8;

  int y = 92;
  caption("DEVICES", y);
  y += 20;
  const int w3 = (kContentWidth - 2 * gap) / 3;
  statBox(kMargin, y, w3, 62, st.devicesOnline, "online", kGreen);
  statBox(kMargin + w3 + gap, y, w3, 62, st.devicesOffline, "offline", kRed);
  statBox(kMargin + 2 * (w3 + gap), y, w3, 62, st.devicesOnline + st.devicesOffline, "total", kText);
  y += 62 + 14;

  caption("DEPLOYMENTS", y);
  y += 20;
  const int w2 = (kContentWidth - gap) / 2;
  statBox(kMargin, y, w2, 56, st.deploymentsActive, "in progress", kAmber);
  statBox(kMargin + w2 + gap, y, w2, 56, st.deploymentsCompleted, "completed", kGreen);
  y += 56 + gap;
  statBox(kMargin, y, w2, 56, st.deploymentsFailed, "failed", kRed);
  statBox(kMargin + w2 + gap, y, w2, 56, st.deploymentsRolledBack, "rolled back", kGray);
}

void Ui::drawFooter(const Snapshot& snap) {
  String left;
  switch (snap.link) {
    case Link::Online:
      left = snap.lastHeartbeatMs > 0 ? "heartbeat " + ago(snap.lastHeartbeatMs) : String("online");
      break;
    case Link::Unreachable:
      left = snap.lastHeartbeatMs > 0 ? "last heartbeat " + ago(snap.lastHeartbeatMs) : String("retrying");
      break;
    default:
      left = "starting";
      break;
  }
  String right = snap.ip.length() > 0 ? String(snap.rssi) + " dBm" : String("no Wi-Fi");
  String text = left + "|" + right;
  if (text == footerDrawn_) return;

  lcd.startWrite();
  lcd.fillRect(0, kFooterTop, LCD_WIDTH, LCD_HEIGHT - kFooterTop, kPanel);
  lcd.setFont(kFontBody);
  lcd.setTextColor(kMuted);
  lcd.setTextDatum(lgfx::textdatum_t::middle_left);
  lcd.drawString(left.c_str(), kMargin, kFooterTop + 12);
  lcd.setTextDatum(lgfx::textdatum_t::middle_right);
  lcd.drawString(right.c_str(), LCD_WIDTH - kMargin, kFooterTop + 12);
  lcd.endWrite();
  footerDrawn_ = text;
}
