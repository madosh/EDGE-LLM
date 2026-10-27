// Client for the control plane's device HTTPS API (/device/v1/*).
//
// Every request is its own mutual-TLS connection: the board presents its
// device certificate, checks the server's against the fleet CA, sends one
// HTTP/1.0 request and reads the response until the server closes.
#pragma once

#include <Arduino.h>

constexpr int kMaxAssignments = 5;

// One deployment as this device sees it.
struct Assignment {
  String deploymentId;
  String modelId;
  String status;    // pending | downloading | verifying | running | failed | skipped | rolled_back
  String errorMsg;  // the skip or failure reason, if any
};

// What GET /device/v1/state returned.
struct FleetState {
  bool valid = false;
  int64_t serverTsUnix = 0;

  String deviceName;
  String deviceStatus;  // online | offline, as the control plane sees it
  String currentModel;  // "" when the device has never run a model
  String labels;        // "key=value  key=value"

  Assignment assignments[kMaxAssignments];  // newest first
  int assignmentCount = 0;

  int devicesOnline = 0;
  int devicesOffline = 0;
  int deploymentsActive = 0;  // pending + in_progress
  int deploymentsCompleted = 0;
  int deploymentsFailed = 0;  // failed + partial_failure
  int deploymentsRolledBack = 0;
};

class FleetClient {
 public:
  // HTTP status of a call, or one of these when there was no HTTP response.
  static constexpr int kTransportError = -1;
  static constexpr int kBadResponse = -2;

  // Registers under DEVICE_NAME; fills deviceId on success. Returns the status.
  int registerDevice(String& deviceId);
  int heartbeat(const String& deviceId);
  int fetchState(const String& deviceId, FleetState& state);
  // Reports this device's status on one deployment.
  int ack(const String& deviceId, const String& deploymentId, const char* status, const char* errorMsg);

  // Why the last call failed, in words fit for the screen.
  const String& lastError() const { return lastError_; }

 private:
  int request(const char* method, const String& path, const String& body, String& response);
  bool resolve(IPAddress& ip);

  String lastError_;
};
