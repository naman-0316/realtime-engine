// Command loadtest-client is a scripted WebSocket client used for manual
// end-to-end verification: it authenticates, finds a match, and plays
// tic-tac-toe using a fixed opening sequence, printing every server
// envelope it receives. Run two instances against a local `server` (see
// README) to watch a full game play out over real WebSocket connections —
// this project ships no browser UI, so this is the primary way to see the
// engine work outside of the automated test suite.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	baseURL := flag.String("addr", "http://localhost:8080", "server base HTTP address")
	playerID := flag.String("player", "", "player ID to request (empty = server-generated)")
	gameType := flag.String("game", "tictactoe", "game type to matchmake into")
	autoplay := flag.Bool("autoplay", true, "automatically send a scripted opening sequence when it's this player's turn")
	flag.Parse()

	token, sessionID, resolvedPlayerID := issueSession(*baseURL, *playerID)
	log.Printf("issued session %s for player %s", sessionID, resolvedPlayerID)

	conn := dial(*baseURL, token)
	defer conn.Close()

	send(conn, map[string]any{"type": "FIND_MATCH", "payload": map[string]string{"gameType": *gameType}})
	log.Printf("searching for a %s match...", *gameType)

	var mySeq uint64
	myTurn := false
	nextCellIdx := 0 // filled sequentially row-major from the shared moveCount, so two independent clients never pick the same cell

	for {
		var env map[string]any
		if err := conn.ReadJSON(&env); err != nil {
			log.Fatalf("connection closed: %v", err)
		}
		fmt.Printf(">> %s\n", mustJSON(env))

		switch env["type"] {
		case "ROOM_STARTED", "STATE_SNAPSHOT":
			// These carry a full "snapshot" (moveCount + turn); STATE_DIFF
			// (below) carries the same "turn" info but nested under "diff"
			// instead, and no moveCount at all — it's a per-move delta.
			snap, _ := env["snapshot"].(map[string]any)
			if turn, ok := snap["turn"].(string); ok {
				myTurn = turn == resolvedPlayerID
			}
			if mc, ok := snap["moveCount"].(float64); ok {
				nextCellIdx = int(mc)
			}
		case "STATE_DIFF":
			diff, _ := env["diff"].(map[string]any)
			if turn, ok := diff["turn"].(string); ok {
				myTurn = turn == resolvedPlayerID
			} else {
				myTurn = false // no "turn" means the game ended on this move
			}
			if _, hasCell := diff["cell"]; hasCell {
				nextCellIdx++
			}
			if terminal, _ := env["terminal"].(bool); terminal {
				log.Printf("game over, winner=%v", env["winner"])
				return
			}
		case "ERROR":
			log.Printf("server error: %v", env["error"])
		case "REDIRECT":
			log.Printf("server requested redirect to %v (not followed by this client)", env["redirectAddr"])
		}

		if *autoplay && myTurn && nextCellIdx < 9 {
			mySeq++
			x, y := nextCellIdx%3, nextCellIdx/3
			send(conn, map[string]any{
				"type": "ACTION_MOVE", "seq": mySeq, "ts": time.Now().UnixMilli(),
				"payload": map[string]int{"x": x, "y": y},
			})
			myTurn = false
		}
	}
}

func issueSession(baseURL, playerID string) (token, sessionID, resolvedPlayerID string) {
	body := "{}"
	if playerID != "" {
		b, _ := json.Marshal(map[string]string{"playerId": playerID})
		body = string(b)
	}
	resp, err := http.Post(baseURL+"/session", "application/json", strings.NewReader(body))
	if err != nil {
		log.Fatalf("POST /session: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		PlayerID  string `json:"playerId"`
		Token     string `json:"token"`
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		log.Fatalf("decode /session response: %v", err)
	}
	return out.Token, out.SessionID, out.PlayerID
}

func dial(baseURL, token string) *websocket.Conn {
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws"
	header := http.Header{"Authorization": []string{"Bearer " + token}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		log.Fatalf("dial %s: %v", wsURL, err)
	}
	return conn
}

func send(conn *websocket.Conn, msg map[string]any) {
	if err := conn.WriteJSON(msg); err != nil {
		log.Fatalf("write %v: %v", msg["type"], err)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
