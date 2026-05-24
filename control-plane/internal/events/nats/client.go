package nats

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

type Client struct {
	nc *nats.Conn
}

func New(url string) (*Client, error) {
	var nc *nats.Conn
	var err error
	for i := 0; i < 15; i++ {
		nc, err = nats.Connect(url,
			nats.RetryOnFailedConnect(true),
			nats.MaxReconnects(-1),
			nats.ReconnectWait(2*time.Second),
		)
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	return &Client{nc: nc}, nil
}

func (c *Client) Publish(subject string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.nc.Publish(subject, data)
}

// Subscribe registers a handler for a subject and returns a subscription that
// can be Unsubscribe()'d.
func (c *Client) Subscribe(subject string, handler func([]byte)) (*nats.Subscription, error) {
	return c.nc.Subscribe(subject, func(msg *nats.Msg) {
		handler(msg.Data)
	})
}

func (c *Client) Close() {
	c.nc.Drain()
}

// Well-known subject helpers.
func SubjectDeviceOnline(deviceID string) string {
	return fmt.Sprintf("device.%s.online", deviceID)
}
func SubjectDeviceOffline(deviceID string) string {
	return fmt.Sprintf("device.%s.offline", deviceID)
}
func SubjectDeployment(deviceID string) string {
	return fmt.Sprintf("deployment.device.%s", deviceID)
}
