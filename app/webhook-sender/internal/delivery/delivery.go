package delivery

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	HeaderEventID          = "X-Webhook-Event-Id"
	HeaderTimestamp        = "X-Webhook-Timestamp"
	HeaderSignatureVersion = "X-Webhook-Signature-Version"
	HeaderSignature        = "X-Webhook-Signature"
)

type Client struct {
	endpoint   string
	secret     string
	now        func() time.Time
	httpClient *http.Client
}

func NewClient(endpoint, secret string, now func() time.Time) *Client {
	return &Client{
		endpoint:   endpoint,
		secret:     secret,
		now:        now,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *Client) Deliver(ctx context.Context, eventID string, body []byte) error {
	timestamp := c.now().Unix()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(HeaderEventID, eventID)
	request.Header.Set(HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	request.Header.Set(HeaderSignatureVersion, "v1")
	request.Header.Set(HeaderSignature, sign(c.secret, timestamp, eventID, body))
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("webhook returned %s: %s", response.Status, message)
	}
	return nil
}

func sign(secret string, timestamp int64, eventID string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s.", timestamp, eventID)
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
