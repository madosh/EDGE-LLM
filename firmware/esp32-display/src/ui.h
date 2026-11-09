// The live agent list, and the screen a tap opens for one agent.
#pragma once

#include "snapshot.h"

enum class View : uint8_t { Agents, Agent };

// Where a tap landed. agent is an index into FleetState::agents, or -1.
struct TapAction {
  bool back = false;
  int agent = -1;
};

class Ui {
 public:
  // deviceName and controlPlane ("address:port") are shown while the board
  // cannot reach the control plane, before it knows anything else.
  void begin(const String& deviceName, const String& controlPlane);
  // One centered line under the logo, for boot messages.
  void splash(const char* line);
  // Redraws only when something on screen changed; call it often.
  // agentIndex selects the open agent; -1 means that agent left the list.
  void render(const Snapshot& snap, View view, int agentIndex);
  // Maps a panel coordinate to a row on the agent list, or to "back" on the
  // detail screen. Coordinates outside a row are ignored.
  TapAction actionAt(int x, int y, View view, int agentCount) const;

 private:
  void drawHeader(View view, const FleetState& state);
  void drawLinkPill(const Snapshot& snap);
  void drawAgents(const Snapshot& snap);
  void drawAgent(const Snapshot& snap, int agentIndex);
  void drawProblem(const Snapshot& snap);
  void drawFooter(const Snapshot& snap);

  String deviceName_;
  String controlPlane_;
  String drawn_;        // signature of what is on screen, minus the footer
  String footerDrawn_;  // footer text on screen
};
