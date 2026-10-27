// Cami Fleet status display for the ESP32-S3 2.8" touch board.
//
// The board is a real fleet member: it registers with its own certificate,
// sends heartbeats, and shows what the control plane decided for it. It
// cannot run a model, so a deployment that reaches it is reported failed
// with that reason, and a model whose requirements it does not meet is
// skipped by the control plane before it ever gets here.
//
// Two tasks: the network task (core 0, next to the Wi-Fi stack) does every
// blocking TLS call and publishes a Snapshot; the Arduino loop (core 1) reads
// touch and draws, so taps stay responsive during a handshake.

#include <Arduino.h>
#include <WiFi.h>

#include "board.h"
#include "fleet_client.h"
#include "secrets.h"
#include "snapshot.h"
#include "touch.h"
#include "ui.h"

namespace {

constexpr uint32_t kHeartbeatEveryMs = 10000;  // the control plane marks a device offline after 30 s
constexpr uint32_t kStateEveryMs = 5000;
constexpr uint32_t kRetryAfterMs = 5000;
constexpr const char* kNoRuntime = "display-only device: an ESP32-S3 status screen has no model runtime";

SemaphoreHandle_t snapshotLock;
Snapshot shared;  // written by the network task, read by the UI, under snapshotLock

template <typename F>
void update(F edit) {
  xSemaphoreTake(snapshotLock, portMAX_DELAY);
  edit(shared);
  xSemaphoreGive(snapshotLock);
}

Snapshot read() {
  xSemaphoreTake(snapshotLock, portMAX_DELAY);
  Snapshot copy = shared;
  xSemaphoreGive(snapshotLock);
  return copy;
}

// Records a failed call. Returns true if the control plane no longer knows
// this device ID (its database was reset, say), so the board must register again.
bool failed(const FleetClient& client, int code) {
  String error = client.lastError();
  Serial.printf("[fleet] %s\n", error.c_str());
  update([&](Snapshot& s) {
    s.link = Link::Unreachable;
    s.error = error;
  });
  return code == 403 || code == 404;
}

void networkTask(void*) {
  FleetClient client;
  String deviceId;
  uint32_t lastHeartbeat = 0;
  uint32_t lastState = 0;
  bool heartbeatDue = true;
  bool stateDue = true;

  WiFi.mode(WIFI_STA);
  WiFi.setHostname(DEVICE_NAME);
  WiFi.disconnect(false, true);
  vTaskDelay(pdMS_TO_TICKS(100));
  const int found = WiFi.scanNetworks();
  Serial.printf("[wifi] scan found %d, joining \"%s\"\n", found, WIFI_SSID);
  const int shown = found > 12 ? 12 : found;
  for (int i = 0; i < shown; i++) {
    Serial.printf("[wifi] seen \"%s\" rssi %d ch %d auth %d\n", WiFi.SSID(i).c_str(), WiFi.RSSI(i),
                  WiFi.channel(i), static_cast<int>(WiFi.encryptionType(i)));
  }
  WiFi.scanDelete();
  WiFi.disconnect(true);
  WiFi.mode(WIFI_STA);
  vTaskDelay(pdMS_TO_TICKS(200));
  WiFi.onEvent([](WiFiEvent_t event, WiFiEventInfo_t info) {
    if (event == ARDUINO_EVENT_WIFI_STA_DISCONNECTED) {
      Serial.printf("[wifi] disconnect reason %u\n",
                    static_cast<unsigned>(info.wifi_sta_disconnected.reason));
    }
  });
  WiFi.setAutoReconnect(true);
  WiFi.setSleep(false);
  WiFi.begin(WIFI_SSID, WIFI_PASSWORD);

  uint32_t lastStatusLog = 0;
  for (;;) {
    if (WiFi.status() != WL_CONNECTED) {
      update([](Snapshot& s) {
        s.link = Link::WifiConnecting;
        s.ip = "";
        s.error = String("joining Wi-Fi \"") + WIFI_SSID + "\"";
      });
      const uint32_t now = millis();
      if (now - lastStatusLog >= 5000) {
        lastStatusLog = now;
        Serial.printf("[wifi] still joining, status %d\n", static_cast<int>(WiFi.status()));
      }
      vTaskDelay(pdMS_TO_TICKS(500));
      continue;
    }
    const String ip = WiFi.localIP().toString();
    const int rssi = WiFi.RSSI();
    update([&](Snapshot& s) {
      s.ip = ip;
      s.rssi = rssi;
    });

    if (deviceId.isEmpty()) {
      update([](Snapshot& s) { s.link = Link::Registering; });
      int code = client.registerDevice(deviceId);
      if (code != 200) {
        deviceId = "";
        failed(client, code);
        update([](Snapshot& s) { s.link = Link::Registering; });
        vTaskDelay(pdMS_TO_TICKS(kRetryAfterMs));
        continue;
      }
      Serial.printf("[fleet] registered as %s, device id %s\n", DEVICE_NAME, deviceId.c_str());
      heartbeatDue = stateDue = true;
    }

    uint32_t now = millis();
    if (heartbeatDue || now - lastHeartbeat >= kHeartbeatEveryMs) {
      int code = client.heartbeat(deviceId);
      if (code != 200) {
        if (failed(client, code)) deviceId = "";
        vTaskDelay(pdMS_TO_TICKS(kRetryAfterMs));
        continue;
      }
      lastHeartbeat = millis();
      heartbeatDue = false;
      update([&](Snapshot& s) {
        s.link = Link::Online;
        s.error = "";
        s.lastHeartbeatMs = lastHeartbeat;
      });
    }

    now = millis();
    if (stateDue || now - lastState >= kStateEveryMs) {
      FleetState state;
      int code = client.fetchState(deviceId, state);
      if (code != 200) {
        if (failed(client, code)) deviceId = "";
        vTaskDelay(pdMS_TO_TICKS(kRetryAfterMs));
        continue;
      }
      // A deployment that targets this board would wait for it forever and
      // hold its rollout open, so answer it the way an agent whose runtime
      // cannot load the model does: failed, with the reason.
      for (int i = 0; i < state.assignmentCount; i++) {
        Assignment& a = state.assignments[i];
        if (a.status != "pending") continue;
        if (client.ack(deviceId, a.deploymentId, "failed", kNoRuntime) == 204) {
          Serial.printf("[fleet] deployment %s (%s): reported failed, no runtime\n", a.deploymentId.c_str(),
                        a.modelId.c_str());
          a.status = "failed";
          a.errorMsg = kNoRuntime;
        }
      }
      lastState = millis();
      stateDue = false;
      update([&](Snapshot& s) {
        s.link = Link::Online;
        s.error = "";
        s.state = state;
      });
    }

    vTaskDelay(pdMS_TO_TICKS(200));
  }
}

// ── Status LED ───────────────────────────────────────────────────────────────

void showLink(Link link) {
  static int shown = -1;
  if (static_cast<int>(link) == shown) return;
  shown = static_cast<int>(link);
  switch (link) {
    case Link::WifiConnecting:
    case Link::Registering:
      neopixelWrite(PIN_RGB_LED, 0, 0, 24);  // blue: starting
      break;
    case Link::Online:
      neopixelWrite(PIN_RGB_LED, 0, 20, 0);  // green: heartbeats accepted
      break;
    case Link::Unreachable:
      neopixelWrite(PIN_RGB_LED, 24, 0, 0);  // red: control plane not answering
      break;
  }
}

// ── UI ───────────────────────────────────────────────────────────────────────

Ui ui;
TapSensor taps;
View view = View::Device;

bool bootButtonPressed() {
  static bool wasDown = false;
  static uint32_t changedAt = 0;
  bool down = digitalRead(PIN_BOOT_BUTTON) == LOW;
  if (down == wasDown || millis() - changedAt < 40) return false;
  wasDown = down;
  changedAt = millis();
  return !down;  // on release
}

}  // namespace

void setup() {
  Serial.begin(115200);
  pinMode(PIN_AUDIO_EN, OUTPUT);
  digitalWrite(PIN_AUDIO_EN, HIGH);  // speaker amplifier off: this firmware makes no sound
  pinMode(PIN_BOOT_BUTTON, INPUT_PULLUP);
  neopixelWrite(PIN_RGB_LED, 0, 0, 24);

  ui.begin(DEVICE_NAME, String(CP_ADDRESS) + ":" + CP_PORT);
  ui.splash("starting...");
  bool touch = taps.begin();
  Serial.printf("[display] %s, touch %s\n", DEVICE_NAME, touch ? "found" : "not found (BOOT button switches views)");

  snapshotLock = xSemaphoreCreateMutex();
  xTaskCreatePinnedToCore(networkTask, "fleet-net", 16384, nullptr, 1, nullptr, 0);
}

void loop() {
  if (taps.tapped() || bootButtonPressed()) {
    view = view == View::Device ? View::Fleet : View::Device;
  }
  Snapshot snap = read();
  showLink(snap.link);
  ui.render(snap, view);
  delay(50);
}
