package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

type WebhookNotifier struct {
	url    string
	client *http.Client
}

type WebhookPayload struct {
	Event     string    `json:"event"`
	DeviceID  string    `json:"device_id,omitempty"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	Severity  string    `json:"severity"`
}

func NewWebhookNotifier(url string) *WebhookNotifier {
	if url == "" {
		return nil
	}
	return &WebhookNotifier{
		url: url,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
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
		log.Warn().Err(err).Str("url", w.url).Msg("webhook: send failed")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Warn().Int("status", resp.StatusCode).Str("url", w.url).Msg("webhook: non-success response")
	}
}

func (w *WebhookNotifier) NotifyDeviceOffline(deviceID, deviceName string) {
	w.Send(WebhookPayload{
		Event:     "device.offline",
		DeviceID:  deviceID,
		Message:   fmt.Sprintf("Device %s (%s) went offline", deviceName, deviceID[:8]),
		Timestamp: time.Now().UTC(),
		Severity:  "warning",
	})
}

func (w *WebhookNotifier) NotifyDeploymentFailed(deviceID, deploymentID, errorMsg string) {
	w.Send(WebhookPayload{
		Event:     "deployment.failed",
		DeviceID:  deviceID,
		Message:   fmt.Sprintf("Deployment %s failed on device %s: %s", deploymentID[:8], deviceID[:8], errorMsg),
		Timestamp: time.Now().UTC(),
		Severity:  "error",
	})
}
