package httpserver

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// handleWS holds one long-lived connection per browser tab, fed by the
// hub's broadcasts. There's nothing browsers send us on this channel —
// all writes go through the regular /api/notes handlers — so the only
// job here is to relay hub messages out and notice when the client goes
// away.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Printf("ws accept: %v", err)
		return
	}
	defer conn.CloseNow()

	// websocket.Accept succeeding means it already wrote a 101 response
	// directly to the hijacked connection — bypassing statusRecorder's
	// WriteHeader entirely, since Accept talks to the raw net.Conn, not
	// to w. Record the real status by hand so /ws doesn't log as its
	// pre-seeded 200 default no matter how the connection ends.
	if sr, ok := w.(*statusRecorder); ok {
		sr.status = http.StatusSwitchingProtocols
	}

	client := s.hub.Register()
	defer s.hub.Unregister(client)

	// CloseRead discards anything the browser sends (nothing, normally)
	// and cancels the returned context once the connection closes.
	ctx := conn.CloseRead(r.Context())

	// Hand this connection its ID before anything else, so the browser
	// can tag its own future requests with it — see event.Origin.
	hello, err := json.Marshal(event{Type: "hello", ClientID: client.ID()})
	if err == nil {
		writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = conn.Write(writeCtx, websocket.MessageText, hello)
		cancel()
	}
	if err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-client.Send():
			if !ok {
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Write(writeCtx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
