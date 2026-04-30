package openclaw

import (
	"errors"
	"testing"
	"time"
)

func TestParseChannelLoginEventQRCode(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	line := `{"event":"qrcode","qrcode":"data:image/png;base64,xxx","expires_at":"` + expires + `"}`
	event, err := ParseChannelLoginEvent(line)
	if err != nil {
		t.Fatalf("ParseChannelLoginEvent() error = %v", err)
	}
	if event.Type != "qrcode" || event.QRCode == "" || event.ExpiresAt.IsZero() {
		t.Fatalf("event = %+v", event)
	}
}

func TestParseChannelLoginEventBound(t *testing.T) {
	line := `{"event":"bound","bound_identity":"alice","channel_name":"alice@wechat"}`
	event, err := ParseChannelLoginEvent(line)
	if err != nil {
		t.Fatalf("ParseChannelLoginEvent() error = %v", err)
	}
	if event.Bound != "alice" || event.Channel != "alice@wechat" {
		t.Fatalf("event = %+v", event)
	}
}

func TestParseChannelLoginEventRejectsPlainText(t *testing.T) {
	cases := []string{"", "Welcome to OpenClaw", "{}"}
	for _, line := range cases {
		_, err := ParseChannelLoginEvent(line)
		if !errors.Is(err, ErrUnparsableOutput) {
			t.Fatalf("ParseChannelLoginEvent(%q) error = %v, want ErrUnparsableOutput", line, err)
		}
	}
}

func TestParseChannelLoginEventDetectsExpiredQRCode(t *testing.T) {
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	line := `{"event":"qrcode","qrcode":"data:image/png;base64,xxx","expires_at":"` + past + `"}`
	_, err := ParseChannelLoginEvent(line)
	if !errors.Is(err, ErrEventExpired) {
		t.Fatalf("error = %v, want ErrEventExpired", err)
	}
}

func TestParseChannelLoginEventInvalidJSONReturnsError(t *testing.T) {
	_, err := ParseChannelLoginEvent(`{event: invalid}`)
	if !errors.Is(err, ErrUnparsableOutput) {
		t.Fatalf("error = %v, want ErrUnparsableOutput", err)
	}
}
