// The two screens: this device, and the fleet at a glance.
#pragma once

#include "snapshot.h"

enum class View : uint8_t { Device, Fleet };

class Ui {
 public:
  // deviceName and controlPlane ("address:port") are shown while the board
  // cannot reach the control plane, before it knows anything else.
  void begin(const String& deviceName, const String& controlPlane);
  // One centered line under the logo, for boot messages.
  void splash(const char* line);
  // Redraws only when something on screen changed; call it often.
  void render(const Snapshot& snap, View view);

 private:
  void drawHeader(View view);
  void drawLinkPill(const Snapshot& snap);
  void drawDeviceView(const Snapshot& snap);
  void drawFleetView(const Snapshot& snap);
  void drawProblem(const Snapshot& snap);
  void drawFooter(const Snapshot& snap);

  String deviceName_;
  String controlPlane_;
  String drawn_;        // signature of what is on screen, minus the footer
  String footerDrawn_;  // footer text on screen
};
