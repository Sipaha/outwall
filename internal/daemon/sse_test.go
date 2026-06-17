package daemon

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Sipaha/outwall/internal/events"
)

func TestSSEStreamsEvents(t *testing.T) {
	bus := events.NewBus()
	srv := httptest.NewServer(sseHandler(bus))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Give the handler a moment to subscribe, then publish.
	time.Sleep(50 * time.Millisecond)
	bus.Publish("agent.registered", map[string]string{"id": "a1"})

	sc := bufio.NewScanner(resp.Body)
	var sawType, sawData bool
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: agent.registered") {
			sawType = true
		}
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "a1") {
			sawData = true
			break
		}
	}
	require.True(t, sawType)
	require.True(t, sawData)
}
