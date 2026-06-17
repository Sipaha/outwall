package daemon

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Sipaha/outwall/internal/events"
)

// sseHandler streams domain events from the bus to a connected client as Server-Sent Events.
// It subscribes for the lifetime of the request, writes an "event:"/"data:" frame per event,
// emits a comment heartbeat every 25s to keep the connection alive, and returns when the
// client disconnects. Requires an http.Flusher (503 if the transport cannot stream).
func sseHandler(bus *events.Bus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		ch, cancel := bus.Subscribe()
		defer cancel()
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ping.C:
				_, _ = w.Write([]byte(": ping\n\n"))
				flusher.Flush()
			case e, ok := <-ch:
				if !ok {
					return
				}
				data, _ := json.Marshal(e.Data)
				_, _ = w.Write([]byte("event: " + e.Type + "\ndata: "))
				_, _ = w.Write(data)
				_, _ = w.Write([]byte("\n\n"))
				flusher.Flush()
			}
		}
	}
}
