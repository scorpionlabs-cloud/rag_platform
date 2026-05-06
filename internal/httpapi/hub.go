package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"rag-unified-realtime/internal/logging"
	"rag-unified-realtime/internal/model"
)

type eventClient struct {
	w  http.ResponseWriter
	fl http.Flusher
}

type Hub struct {
	mu         sync.Mutex
	clients    map[*eventClient]bool
	broadcast  chan model.WSMessage
	register   chan *eventClient
	unregister chan *eventClient
	log        *logging.Logger
}

func NewHub(log *logging.Logger) *Hub {
	h := &Hub{
		clients:    make(map[*eventClient]bool),
		broadcast:  make(chan model.WSMessage, 256),
		register:   make(chan *eventClient, 32),
		unregister: make(chan *eventClient, 32),
		log:        log,
	}
	go h.run()
	return h
}

func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
		case client := <-h.unregister:
			h.mu.Lock()
			delete(h.clients, client)
			h.mu.Unlock()
		case msg := <-h.broadcast:
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			payload := fmt.Sprintf("data: %s\n\n", data)
			h.mu.Lock()
			for client := range h.clients {
				if _, err := fmt.Fprint(client.w, payload); err != nil {
					delete(h.clients, client)
					continue
				}
				client.fl.Flush()
			}
			h.mu.Unlock()
		}
	}
}

func (h *Hub) Broadcast(msg model.WSMessage) {
	select {
	case h.broadcast <- msg:
	default:
		if h.log != nil {
			h.log.Warn("job event dropped", "job_id", msg.JobID, "status", msg.Status)
		}
	}
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	client := &eventClient{w: w, fl: flusher}
	h.register <- client
	defer func() { h.unregister <- client }()

	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	<-r.Context().Done()
}
