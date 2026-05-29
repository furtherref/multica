package agent

import (
	"log/slog"
	"sync"
	"testing"
)

// TestCodexEmitsReconnectingOnWillRetry verifies the source of the reconnect
// UX signal: a retrying ("willRetry") error notification must surface a
// transient MessageStatus{reconnecting} (which the daemon broadcasts in place
// as a "Reconnecting…" hint), while a terminal error must NOT — it fails the
// turn instead.
func TestCodexEmitsReconnectingOnWillRetry(t *testing.T) {
	newClient := func() (*codexClient, *[]Message, *sync.Mutex) {
		var mu sync.Mutex
		var got []Message
		c := &codexClient{
			cfg:                  Config{Logger: slog.Default()},
			stdin:                &fakeStdin{},
			pending:              make(map[int]*pendingRPC),
			notificationProtocol: "unknown",
			threadID:             "T1",
			onMessage:            func(m Message) { mu.Lock(); got = append(got, m); mu.Unlock() },
			onSemanticActivity:   func(string) {},
			onTurnDone:           func(bool) {},
		}
		return c, &got, &mu
	}

	hasReconnecting := func(msgs []Message) bool {
		for _, m := range msgs {
			if m.Type == MessageStatus && m.Status == "reconnecting" {
				return true
			}
		}
		return false
	}

	t.Run("retrying error surfaces reconnecting", func(t *testing.T) {
		c, got, mu := newClient()
		c.handleLine(`{"jsonrpc":"2.0","method":"error","params":{"threadId":"T1","willRetry":true,"error":{"message":"connection reset"}}}`)
		mu.Lock()
		defer mu.Unlock()
		if !hasReconnecting(*got) {
			t.Fatalf("expected a MessageStatus{reconnecting} on willRetry, got %+v", *got)
		}
		if c.getTurnError() != "" {
			t.Errorf("retrying error must not set turnError, got %q", c.getTurnError())
		}
	})

	t.Run("terminal error does not surface reconnecting and fails the turn", func(t *testing.T) {
		c, got, mu := newClient()
		c.handleLine(`{"jsonrpc":"2.0","method":"error","params":{"threadId":"T1","willRetry":false,"error":{"message":"fatal"}}}`)
		mu.Lock()
		defer mu.Unlock()
		if hasReconnecting(*got) {
			t.Errorf("terminal error must not surface reconnecting, got %+v", *got)
		}
		if c.getTurnError() != "fatal" {
			t.Errorf("terminal error should set turnError to %q, got %q", "fatal", c.getTurnError())
		}
	})
}
