package api

import (
	"fmt"
	"net/http"

	"github.com/ran-su/cronplus/internal/core"
)

// SSEHandler serves Server-Sent Events from the event broker.
func SSEHandler(broker *core.EventBroker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "SSE not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		ch := broker.Subscribe()
		defer broker.Unsubscribe(ch)

		// Send initial keepalive
		fmt.Fprintf(w, ": connected\n\n")
		flusher.Flush()

		for {
			select {
			case event := <-ch:
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Data)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}
