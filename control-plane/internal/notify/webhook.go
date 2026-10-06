package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/rs/zerolog/log"
)

type WebhookNotifier struct {
	url    string
	host   string
	client *http.Client
}

type WebhookPayload struct {
	Event     string    `json:"event"`
	DeviceID  string    `json:"device_id,omitempty"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	Severity  string    `json:"severity"`
}

func NewWebhookNotifier(rawURL string) *WebhookNotifier {
	if rawURL == "" {
		return nil
	}
	host := "(invalid URL)"
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		host = u.Host
	}
	return &WebhookNotifier{
		url:  rawURL,
		host: host,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Host returns only the webhook's host. Slack and Discord webhook URLs carry
// their secret in the path, so the full URL must never be logged.
func (w *WebhookNotifier) Host() string {
	if w == nil {
		return ""
	}
	return w.host
}

func (w *WebhookNotifier) Send(payload WebhookPayload) {
	if w == nil {
		return
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Warn().Err(err).Msg("webhook: marshal failed")
		return
	}

	resp, err := w.client.Post(w.url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Warn().Err(err).Str("host", w.host).Msg("webhook: send failed")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Warn().Int("status", resp.StatusCode).Str("host", w.host).Msg("webhook: non-success response")
	}
}

func (w *WebhookNotifier) NotifyDeviceOffline(deviceID, deviceName string) {
	w.Send(WebhookPayload{
		Event:     "device.offline",
		DeviceID:  deviceID,
		Message:   fmt.Sprintf("Device %s (%s) went offline", deviceName, shortID(deviceID)),
		Timestamp: time.Now().UTC(),
		Severity:  "warning",
	})
}

func (w *WebhookNotifier) NotifyDeploymentFailed(deviceID, deploymentID, errorMsg string) {
	w.Send(WebhookPayload{
		Event:     "deployment.failed",
		DeviceID:  deviceID,
		Message:   fmt.Sprintf("Deployment %s failed on device %s: %s", shortID(deploymentID), shortID(deviceID), errorMsg),
		Timestamp: time.Now().UTC(),
		Severity:  "error",
	})
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
