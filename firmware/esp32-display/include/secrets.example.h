// Template for include/secrets.h, which is git-ignored because it holds the
// Wi-Fi password and this device's private key. Generate the real file with
//
//   python tools/make_secrets.py --certs certs --ssid ... --password ... --address ...
//
// or copy this file to secrets.h and paste the PEM files in by hand.
#pragma once

#define WIFI_SSID "your-wifi"
#define WIFI_PASSWORD "your-wifi-password"

// Where the control plane's device HTTPS API listens: the LAN IP (or a
// hostname this network resolves) of the machine running docker compose.
#define CP_ADDRESS "192.168.1.20"
#define CP_PORT 8443
// A DNS name in the server certificate. The board connects to CP_ADDRESS but
// checks the certificate against this name; gen-certs.sh always includes it.
#define CP_TLS_NAME "control-plane"

// Must equal the common name of certs/device.crt.
#define DEVICE_NAME "esp32-display-1"
// Same format as the agent's DEVICE_LABELS.
#define DEVICE_LABELS "location=desk,type=esp32-display"

// certs/ca.crt
static const char CA_CERT[] = R"PEM(-----BEGIN CERTIFICATE-----
...
-----END CERTIFICATE-----
)PEM";

// certs/device.crt
static const char DEVICE_CERT[] = R"PEM(-----BEGIN CERTIFICATE-----
...
-----END CERTIFICATE-----
)PEM";

// certs/device.key
static const char DEVICE_KEY[] = R"PEM(-----BEGIN PRIVATE KEY-----
...
-----END PRIVATE KEY-----
)PEM";
