// What the network task last learned, handed to the UI under a mutex.
#pragma once

#include <Arduino.h>

#include "fleet_client.h"

enum class Link : uint8_t {
  WifiConnecting,  // not on Wi-Fi yet
  Registering,     // on Wi-Fi, no device ID from the control plane yet
  Online,          // the last heartbeat or state call succeeded
  Unreachable,     // registered before, but the last call failed
};

struct Snapshot {
  Link link = Link::WifiConnecting;
  String error;                  // why the last call failed; "" after a success
  uint32_t lastHeartbeatMs = 0;  // millis() of the last accepted heartbeat, 0 = never
  int rssi = 0;           // Wi-Fi signal, dBm
  String ip;              // this board's address
  FleetState state;       // last state fetched; state.valid is false before the first
};
