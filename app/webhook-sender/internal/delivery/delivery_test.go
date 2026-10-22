package delivery

import (
	"io"
	"net/http"
	"testing"
	"time"
)

func TestClientDeliversSignedEvent(t *testing.T) {
	body := []byte(`{"id":"evt-1","event":"profile.updated","version":1}`)
	var received *http.Request
	var receivedBody []byte
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		received = request
		receivedBody, _ = io.ReadAll(request.Body)
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: http.NoBody}, nil
	})

	client := NewClient("http://receiver.test/webhooks/platform", "local-webhook-secret", func() time.Time { return time.Unix(1_800_000_000, 0) })
	client.httpClient.Transport = transport
	if err := client.Deliver(t.Context(), "evt-1", body); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if string(receivedBody) != string(body) {
		t.Fatalf("body = %s, want %s", receivedBody, body)
	}
	if got := received.Header.Get(HeaderEventID); got != "evt-1" {
		t.Fatalf("event id = %q", got)
	}
	if got := received.Header.Get(HeaderSignature); got != "203bbae0894b8e881f971c625de4c969f50f98662268db488545c456ebb5bc51" {
		t.Fatalf("signature = %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }
