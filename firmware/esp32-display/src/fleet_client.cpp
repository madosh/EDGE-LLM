#include "fleet_client.h"

#include <ArduinoJson.h>
#include <WiFi.h>
#include <WiFiClientSecure.h>

#include "secrets.h"

namespace {

constexpr const char* kAgentVersion = "cami-display/0.1.0";
constexpr uint32_t kResponseTimeoutMs = 10000;
constexpr size_t kMaxResponseBytes = 8192;

// RAM reported at registration, in MiB, rounded up. The control plane treats
// 0 as "unknown" and would not hold the board to a model's memory
// requirement, so report at least 1.
uint64_t memTotalMb() {
  uint64_t bytes = static_cast<uint64_t>(ESP.getHeapSize()) + ESP.getPsramSize();
  uint64_t mb = (bytes + (1u << 20) - 1) >> 20;
  return mb == 0 ? 1 : mb;
}

// Reads one header line, without its CR LF. Returns "" at the deadline.
String readLine(WiFiClientSecure& tls, uint32_t deadline) {
  String line;
  while (millis() < deadline) {
    if (!tls.available()) {
      if (!tls.connected()) break;
      delay(2);
      continue;
    }
    char c = static_cast<char>(tls.read());
    if (c == '\n') break;
    if (c != '\r') line += c;
  }
  return line;
}

String trimmedMessage(const String& body) {
  String msg = body;
  msg.trim();
  if (msg.length() > 90) msg = msg.substring(0, 90) + "...";
  return msg;
}

}  // namespace

bool FleetClient::resolve(IPAddress& ip) {
  if (ip.fromString(CP_ADDRESS)) return true;
  if (WiFi.hostByName(CP_ADDRESS, ip) == 1) return true;
  lastError_ = String("cannot resolve ") + CP_ADDRESS;
  return false;
}

int FleetClient::request(const char* method, const String& path, const String& body, String& response) {
  response = "";
  IPAddress ip;
  if (!resolve(ip)) return kTransportError;

  WiFiClientSecure tls;
  tls.setHandshakeTimeout(15);
  // Connect to the address, but verify the certificate against CP_TLS_NAME:
  // the TLS stack in this core cannot match an IP against the certificate's
  // IP addresses, only DNS names.
  if (!tls.connect(ip, CP_PORT, CP_TLS_NAME, CA_CERT, DEVICE_CERT, DEVICE_KEY)) {
    char reason[100] = {0};
    tls.lastError(reason, sizeof(reason));
    lastError_ = String("TLS to ") + CP_ADDRESS + ":" + CP_PORT + " failed: " + reason;
    return kTransportError;
  }

  // HTTP/1.0: the server closes the connection after one response and never
  // uses chunked encoding, so the body is simply everything until close.
  String head = String(method) + " " + path + " HTTP/1.0\r\n" +
                "Host: " CP_TLS_NAME "\r\n" +
                "User-Agent: " + kAgentVersion + "\r\n" +
                "Accept: application/json\r\n";
  if (body.length() > 0) {
    head += "Content-Type: application/json\r\nContent-Length: " + String(body.length()) + "\r\n";
  }
  head += "\r\n";
  tls.print(head);
  if (body.length() > 0) tls.print(body);

  uint32_t deadline = millis() + kResponseTimeoutMs;
  String statusLine = readLine(tls, deadline);
  int firstSpace = statusLine.indexOf(' ');
  if (!statusLine.startsWith("HTTP/") || firstSpace < 0) {
    tls.stop();
    lastError_ = "no HTTP response from control plane";
    return kBadResponse;
  }
  int code = statusLine.substring(firstSpace + 1, firstSpace + 4).toInt();

  while (readLine(tls, deadline).length() > 0) {
  }
  while (millis() < deadline && (tls.connected() || tls.available())) {
    if (!tls.available()) {
      delay(2);
      continue;
    }
    char c = static_cast<char>(tls.read());
    if (response.length() < kMaxResponseBytes) response += c;
  }
  tls.stop();

  if (code >= 400) {
    lastError_ = String("HTTP ") + code + ": " + trimmedMessage(response);
  }
  return code;
}

int FleetClient::registerDevice(String& deviceId) {
  JsonDocument doc;
  doc["device_name"] = DEVICE_NAME;
  doc["agent_version"] = kAgentVersion;
  doc["arch"] = "xtensa";
  doc["os"] = "esp-idf";
  doc["mem_total_mb"] = memTotalMb();
  // No accelerator: a model that needs a CPU, GPU or NPU skips this board.
  doc["accelerators"].to<JsonArray>();

  JsonObject labels = doc["labels"].to<JsonObject>();
  String pairs = DEVICE_LABELS;
  int start = 0;
  while (start < static_cast<int>(pairs.length())) {
    int comma = pairs.indexOf(',', start);
    if (comma < 0) comma = pairs.length();
    String pair = pairs.substring(start, comma);
    int eq = pair.indexOf('=');
    if (eq > 0) {
      String key = pair.substring(0, eq);
      String value = pair.substring(eq + 1);
      key.trim();
      value.trim();
      labels[key] = value;
    }
    start = comma + 1;
  }

  String body, response;
  serializeJson(doc, body);
  int code = request("POST", "/device/v1/register", body, response);
  if (code != 200) return code;

  JsonDocument reply;
  if (deserializeJson(reply, response) || !reply["device_id"].is<const char*>()) {
    lastError_ = "register: unexpected response";
    return kBadResponse;
  }
  deviceId = reply["device_id"].as<const char*>();
  return code;
}

int FleetClient::heartbeat(const String& deviceId) {
  String response;
  return request("POST", "/device/v1/heartbeat", "{\"device_id\":\"" + deviceId + "\"}", response);
}

int FleetClient::ack(const String& deviceId, const String& deploymentId, const char* status, const char* errorMsg) {
  JsonDocument doc;
  doc["deployment_id"] = deploymentId;
  doc["device_id"] = deviceId;
  doc["status"] = status;
  doc["error_msg"] = errorMsg;
  String body, response;
  serializeJson(doc, body);
  return request("POST", "/device/v1/ack", body, response);
}

int FleetClient::fetchState(const String& deviceId, FleetState& state) {
  String response;
  int code = request("GET", "/device/v1/state?device_id=" + deviceId, "", response);
  if (code != 200) return code;

  JsonDocument doc;
  DeserializationError err = deserializeJson(doc, response);
  if (err) {
    lastError_ = String("state: bad JSON (") + err.c_str() + ")";
    return kBadResponse;
  }

  FleetState next;
  next.serverTsUnix = doc["server_ts_unix"] | 0;

  JsonObject device = doc["device"];
  next.deviceName = device["name"] | "";
  next.deviceStatus = device["status"] | "";
  next.currentModel = device["current_model_id"] | "";
  for (JsonPair kv : device["labels"].as<JsonObject>()) {
    if (next.labels.length() > 0) next.labels += "  ";
    next.labels += kv.key().c_str();
    next.labels += "=";
    next.labels += kv.value().as<const char*>();
  }

  for (JsonObject a : doc["assignments"].as<JsonArray>()) {
    if (next.assignmentCount == kMaxAssignments) break;
    Assignment& out = next.assignments[next.assignmentCount++];
    out.deploymentId = a["deployment_id"] | "";
    out.modelId = a["model_id"] | "";
    out.status = a["status"] | "";
    out.errorMsg = a["error_msg"] | "";
  }

  JsonObject devices = doc["fleet"]["devices"];
  next.devicesOnline = devices["online"] | 0;
  next.devicesOffline = devices["offline"] | 0;
  JsonObject deployments = doc["fleet"]["deployments"];
  next.deploymentsActive = (deployments["pending"] | 0) + (deployments["in_progress"] | 0);
  next.deploymentsCompleted = deployments["completed"] | 0;
  next.deploymentsFailed = (deployments["failed"] | 0) + (deployments["partial_failure"] | 0);
  next.deploymentsRolledBack = deployments["rolled_back"] | 0;

  next.valid = true;
  state = next;
  return code;
}
