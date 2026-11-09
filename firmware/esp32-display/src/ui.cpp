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
constexpr int kListTop = 40;
constexpr int kRowH = 42;

bool tokenListed(const String& list, const String& item) {
  if (item.length() == 0) return false;
  int start = 0;
  while (start < static_cast<int>(list.length())) {
    int orAt = list.indexOf(" or ", start);
    int end = orAt < 0 ? static_cast<int>(list.length()) : orAt;
    String token = list.substring(start, end);
    token.trim();
    if (token.equalsIgnoreCase(item)) return true;
    if (orAt < 0) break;
    start = orAt + 4;
  }
  return false;
}

uint32_t requirementColor(bool required, bool met) {
  if (!required) return kMuted;
  return met ? kGreen : kRed;
}

const lgfx::IFont* const kFontTitle = &lgfx::fonts::FreeSansBold12pt7b;
const lgfx::IFont* const kFontBold = &lgfx::fonts::FreeSansBold9pt7b;
const lgfx::IFont* const kFontBody = &lgfx::fonts::Font2;

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

void Ui::render(const Snapshot& snap, View view, int agentIndex) {
  const FleetState& st = snap.state;
  String sig = String(static_cast<int>(view)) + "|" + agentIndex + "|" + static_cast<int>(snap.link) + "|" +
               snap.error + "|" + snap.ip + "|" + st.valid + "|" + st.devicesOnline + "|" + st.devicesOffline +
               "|" + st.agentCount;
  for (int i = 0; i < st.agentCount; i++) {
    const Agent& a = st.agents[i];
    sig += "|" + a.name + a.status + a.arch + String(a.memMb) + a.accelerators + a.currentModel +
           String(a.asked ? 1 : 0) + a.modelId + a.askStatus + a.reason + String(a.needMem) + a.needAccel + a.needArch;
  }

  if (sig != drawn_) {
    lcd.startWrite();
    lcd.fillScreen(kBg);
    if (snap.link != Link::Online || !st.valid) {
      drawHeader(View::Agents, st);
      drawLinkPill(snap);
      drawProblem(snap);
    } else if (view == View::Agent) {
      drawHeader(View::Agent, st);
      drawAgent(snap, agentIndex);
    } else {
      drawHeader(View::Agents, st);
      drawAgents(snap);
    }
    lcd.endWrite();
    drawn_ = sig;
    footerDrawn_ = "";
  }
  drawFooter(snap);
}

TapAction Ui::actionAt(int x, int y, View view, int agentCount) const {
  TapAction action;
  if (x < 0 || x >= LCD_WIDTH || y < 0 || y >= LCD_HEIGHT) return action;
  if (view == View::Agent) {
    action.back = true;
    return action;
  }
  if (y < kListTop) return action;
  int row = (y - kListTop) / kRowH;
  if (row >= 0 && row < agentCount && y < kListTop + agentCount * kRowH) action.agent = row;
  return action;
}

void Ui::drawHeader(View view, const FleetState& state) {
  lcd.fillRect(0, 0, LCD_WIDTH, kHeaderHeight, kPanel);
  lcd.setFont(kFontBold);
  lcd.setTextDatum(lgfx::textdatum_t::middle_left);
  lcd.setTextColor(kCloud);
  if (view == View::Agent) {
    lcd.drawString("< AGENTS", kMargin, kHeaderHeight / 2);
    return;
  }
  lcd.drawString("AGENTS", kMargin, kHeaderHeight / 2);
  lcd.setTextDatum(lgfx::textdatum_t::middle_right);
  lcd.setTextColor(kGreen);
  lcd.drawString((String(state.devicesOnline) + " up").c_str(), LCD_WIDTH - kMargin, kHeaderHeight / 2);
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

void Ui::drawAgents(const Snapshot& snap) {
  const FleetState& st = snap.state;
  if (st.agentCount == 0) {
    lcd.setFont(kFontBody);
    lcd.setTextColor(kMuted);
    lcd.setTextDatum(lgfx::textdatum_t::top_left);
    drawWrapped("No agents have registered yet.", kMargin, kListTop + 8, kContentWidth, 3);
    return;
  }
  const int shown = st.agentCount;
  for (int i = 0; i < shown; i++) {
    const Agent& a = st.agents[i];
    const int y = kListTop + i * kRowH;
    lcd.fillRoundRect(kMargin, y, kContentWidth, kRowH - 4, 6, kPanel);
    const bool up = a.status == "online";
    lcd.fillCircle(kMargin + 14, y + (kRowH - 4) / 2, 5, up ? kGreen : kRed);

    lcd.setTextDatum(lgfx::textdatum_t::top_left);
    lcd.setFont(kFontBold);
    lcd.setTextColor(kText);
    lcd.drawString(fit(a.name, kContentWidth - 36).c_str(), kMargin + 28, y + 4);

    String sub = a.asked ? a.modelId : String("nothing asked yet");
    if (a.asked) sub += "  " + statusWord(a.askStatus);
    lcd.setFont(kFontBody);
    lcd.setTextColor(a.asked ? statusColor(a.askStatus) : kMuted);
    lcd.drawString(fit(sub, kContentWidth - 36).c_str(), kMargin + 28, y + 22);
  }
}

void Ui::drawAgent(const Snapshot& snap, int agentIndex) {
  int y = kHeaderHeight + 10;
  lcd.setTextDatum(lgfx::textdatum_t::top_left);
  if (agentIndex < 0 || agentIndex >= snap.state.agentCount) {
    lcd.setFont(kFontBody);
    lcd.setTextColor(kMuted);
    drawWrapped("That agent is no longer in the fleet. Tap to go back.", kMargin, y, kContentWidth, 4);
    return;
  }
  const Agent& a = snap.state.agents[agentIndex];

  lcd.setFont(kFontTitle);
  lcd.setTextColor(kText);
  lcd.drawString(fit(a.name, kContentWidth - 70).c_str(), kMargin, y);
  lcd.setFont(kFontBold);
  lcd.setTextColor(a.status == "online" ? kGreen : kRed);
  lcd.setTextDatum(lgfx::textdatum_t::top_right);
  lcd.drawString(a.status == "online" ? "UP" : "DOWN", LCD_WIDTH - kMargin, y + 4);
  lcd.setTextDatum(lgfx::textdatum_t::top_left);
  y += 28;

  lcd.setFont(kFontBody);
  lcd.setTextColor(kMuted);
  String has = a.arch + "   " + a.memMb + " MB   " + a.accelerators;
  lcd.drawString(fit(has, kContentWidth).c_str(), kMargin, y);
  y += 18;
  if (a.currentModel.length() > 0) {
    lcd.setTextColor(kEdge);
    lcd.drawString(fit(String("running ") + a.currentModel, kContentWidth).c_str(), kMargin, y);
    y += 18;
  }
  if (a.live) {
    // Simulated numbers are labelled and greyed so they never pass for real ones.
    const bool measured = a.source == "probe";
    String perf = String(a.tps, 1) + " tok/s   " + a.ttftMs + " ms TTFT   " +
                  (measured ? "measured" : a.source.length() > 0 ? a.source : String("unknown"));
    lcd.setTextColor(measured ? kGreen : kGray);
    lcd.drawString(fit(perf, kContentWidth).c_str(), kMargin, y);
    y += 18;
  }
  y += 6;
  lcd.drawFastHLine(kMargin, y, kContentWidth, kPanel);
  y += 8;

  caption("WHAT IT WAS ASKED", y);
  y += 18;
  if (!a.asked) {
    lcd.setFont(kFontBody);
    lcd.setTextColor(kMuted);
    drawWrapped("No deployment has targeted this agent yet.", kMargin, y, kContentWidth, 3);
    return;
  }

  lcd.setFont(kFontBold);
  lcd.setTextColor(kText);
  lcd.drawString(fit(a.modelId, kContentWidth - 110).c_str(), kMargin, y);
  lcd.setTextColor(statusColor(a.askStatus));
  lcd.setTextDatum(lgfx::textdatum_t::top_right);
  lcd.drawString(fit(statusWord(a.askStatus), 100).c_str(), LCD_WIDTH - kMargin, y);
  lcd.setTextDatum(lgfx::textdatum_t::top_left);
  y += 22;

  caption("NEEDED", y);
  y += 18;
  auto needLine = [&](const char* label, const String& value, bool required, bool met) {
    lcd.setFont(kFontBody);
    lcd.setTextColor(kMuted);
    lcd.drawString(label, kMargin, y);
    lcd.setTextColor(requirementColor(required, met));
    lcd.drawString(fit(value, 140).c_str(), 96, y);
    y += 18;
  };
  const bool memRequired = a.needMem > 0;
  const bool memMet = !memRequired || (a.memMb > 0 && a.memMb >= a.needMem);
  needLine("RAM", memRequired ? String(a.needMem) + " MB" : String("any"), memRequired, memMet);

  const bool accelRequired = a.needAccel.length() > 0;
  String haveAccel = a.accelerators;
  haveAccel.toLowerCase();
  String wantAccel = a.needAccel;
  wantAccel.toLowerCase();
  const bool accelMet = !accelRequired || haveAccel.indexOf(wantAccel) >= 0;
  needLine("Accel", accelRequired ? a.needAccel : String("any"), accelRequired, accelMet);

  const bool archRequired = a.needArch.length() > 0;
  const bool archMet = !archRequired || tokenListed(a.needArch, a.arch);
  needLine("Arch", archRequired ? a.needArch : String("any"), archRequired, archMet);

  if (a.reason.length() > 0 && y < kFooterTop - 20) {
    y += 4;
    lcd.setFont(kFontBody);
    lcd.setTextColor(kText);
    drawWrapped(a.reason, kMargin, y, kContentWidth, 3);
  }
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
