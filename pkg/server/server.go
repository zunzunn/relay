package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/relay-dev/relay/pkg/relay"
)

// Logger provides structured logging with [INFO], [WARN], [ERROR] prefixes.
type Logger struct{}

func (Logger) Info(format string, args ...interface{}) {
	log.Printf("[INFO]  "+format, args...)
}

func (Logger) Warn(format string, args ...interface{}) {
	log.Printf("[WARN]  "+format, args...)
}

func (Logger) Error(format string, args ...interface{}) {
	log.Printf("[ERROR] "+format, args...)
}

var logger = Logger{}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type Server struct {
	rooms   *relay.RoomRegistry
	clients map[string]*relay.WSClient
	mu      sync.RWMutex
}

func New() *Server {
	return &Server{
		rooms:   relay.NewRoomRegistry(),
		clients: make(map[string]*relay.WSClient),
	}
}

func generateClientID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

func deterministicColor(userID string) string {
	colors := []string{
		"#FF6B6B", "#4ECDC4", "#45B7D1", "#96CEB4",
		"#FFEAA7", "#DDA0DD", "#98D8C8", "#F7DC6F",
		"#BB8FCE", "#85C1E9",
	}
	var sum int
	for i, c := range userID {
		sum += int(c) * (i + 1)
	}
	return colors[sum%len(colors)]
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/ws" {
		s.handleWebSocket(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/room/") && r.Method == http.MethodGet {
		code := strings.TrimPrefix(r.URL.Path, "/api/room/")
		s.handleRoomInfo(w, code)
		return
	}
	if r.URL.Path == "/health" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (s *Server) handleRoomInfo(w http.ResponseWriter, code string) {
	room, ok := s.rooms.Get(code)
	if !ok {
		http.Error(w, `{"exists":false}`, http.StatusNotFound)
		return
	}
	info := map[string]interface{}{
		"exists":       true,
		"has_password": room.HasPassword(),
		"member_count": room.UserCount(),
	}
	data, _ := json.Marshal(info)
	w.Write(data)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Error("websocket upgrade: %v", err)
		return
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		if !websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
			logger.Warn("read initial message: %v", err)
		}
		conn.Close()
		return
	}

	var msg relay.Message
	if err := json.Unmarshal(data, &msg); err != nil {
		conn.WriteJSON(relay.Message{Type: relay.MsgError, Payload: relay.ErrMessage{Code: "invalid_json", Message: "Failed to parse join message"}})
		conn.Close()
		return
	}

	if msg.Type != relay.MsgJoinRoom {
		conn.WriteJSON(relay.Message{Type: relay.MsgError, Payload: relay.ErrMessage{Code: "unexpected_message", Message: "Expected join_room message first"}})
		conn.Close()
		return
	}

	payload, ok := msg.Payload.(map[string]interface{})
	if !ok {
		conn.WriteJSON(relay.Message{Type: relay.MsgError, Payload: relay.ErrMessage{Code: "invalid_payload", Message: "Invalid join payload"}})
		conn.Close()
		return
	}

	roomCode, _ := payload["room_code"].(string)
	password, _ := payload["password"].(string)
	username, _ := payload["username"].(string)
	isHost, _ := payload["is_host"].(bool)

	if roomCode == "" || username == "" {
		conn.WriteJSON(relay.Message{Type: relay.MsgError, Payload: relay.ErrMessage{Code: "missing_fields", Message: "room_code and username required"}})
		conn.Close()
		return
	}

	room, exists := s.rooms.Get(roomCode)
	if !exists {
		if isHost {
			room = s.rooms.Create(roomCode, nil)
		} else {
			conn.WriteJSON(relay.Message{Type: relay.MsgError, Payload: relay.ErrMessage{Code: "room_not_found", Message: "Room does not exist"}})
			conn.Close()
			return
		}
	}

	if room.HasPassword() && !isHost {
		storedPw := string(room.Password)
		if password != storedPw {
			conn.WriteJSON(relay.Message{Type: relay.MsgError, Payload: relay.ErrMessage{Code: "auth_failed", Message: "Invalid password"}})
			conn.Close()
			return
		}
	}

	clientID := generateClientID()
	client := relay.NewWSClient(clientID, conn)
	client.User = &relay.UserInfo{
		ID:       clientID,
		Username: username,
		IsHost:   isHost,
		Color:    deterministicColor(clientID),
	}

	s.mu.Lock()
	s.clients[clientID] = client
	s.mu.Unlock()

	room.AddUser(clientID, client.User)

	var users []relay.UserInfo
	for _, u := range room.Users {
		users = append(users, *u)
	}

	joined := relay.RoomJoined{
		RoomCode:     roomCode,
		HostID:       room.GetHostID(),
		Users:        users,
		Passwordured: room.HasPassword(),
	}
	client.SendJSON(relay.NewMessage(relay.MsgRoomJoined, joined))
	s.broadcastToRoom(roomCode, relay.NewMessage(relay.MsgUserJoined, relay.UserJoined{User: *client.User}), clientID)

	s.handleClient(client, room)

	room.RemoveUser(clientID)
	s.mu.Lock()
	delete(s.clients, clientID)
	s.mu.Unlock()
	s.broadcastToRoom(roomCode, relay.NewMessage(relay.MsgUserLeft, relay.UserLeft{UserID: clientID, Username: username}), "")
	if room.UserCount() == 0 {
		s.rooms.Delete(roomCode)
	}
	client.Close()
}

func (s *Server) handleClient(client *relay.WSClient, room *relay.Room) {
	queue := relay.NewCommandQueue()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		client.WritePump()
	}()

	go func() {
		defer wg.Done()
		err := client.ReadPump(func(msg *relay.Message) error {
			switch msg.Type {
			case relay.MsgTerminalData:
				s.broadcastToRoom(room.Code, msg, client.ID)
			case relay.MsgCursorMove:
				s.broadcastToRoom(room.Code, msg, client.ID)
			case relay.MsgResize:
				s.broadcastToRoom(room.Code, msg, "")
			case relay.MsgChatMessage:
				s.broadcastToRoom(room.Code, msg, "")
			case relay.MsgMarker, relay.MsgMarkerRemove:
				s.broadcastToRoom(room.Code, msg, "")
			case relay.MsgCommandQueue:
				payload, ok := msg.Payload.(map[string]interface{})
				if !ok {
					return nil
				}
				cmd := &relay.QueuedCommand{
					ID:        relay.GenerateID(),
					UserID:    client.ID,
					Username:  client.User.Username,
					Command:   getString(payload, "command"),
					Timestamp: time.Now(),
				}
				queue.Enqueue(cmd)

				hostID := room.GetHostID()
				s.mu.RLock()
				host, ok := s.clients[hostID]
				s.mu.RUnlock()
				if ok {
					payload["command_id"] = cmd.ID
					payload["user_id"] = client.ID
					payload["username"] = client.User.Username
					host.SendJSON(relay.NewMessage(relay.MsgCommandQueue, payload))
				}
				client.SendJSON(relay.NewMessage(relay.MsgCommandQueue, payload))

			case relay.MsgCommandApprove:
				payload, ok := msg.Payload.(map[string]interface{})
				if !ok {
					return nil
				}
				cmdID := getString(payload, "command_id")
				cmd, _ := queue.Get(cmdID)
				if cmd != nil {
					s.mu.RLock()
					target, ok := s.clients[cmd.UserID]
					s.mu.RUnlock()
					if ok {
						target.SendJSON(relay.NewMessage(relay.MsgCommandApprove, payload))
					}
					queue.Approve(cmdID)
				}
				s.broadcastToRoom(room.Code, relay.NewMessage(relay.MsgCommandApprove, payload), "")

			case relay.MsgCommandReject:
				payload, ok := msg.Payload.(map[string]interface{})
				if !ok {
					return nil
				}
				cmdID := getString(payload, "command_id")
				cmd, _ := queue.Get(cmdID)
				if cmd != nil {
					s.mu.RLock()
					target, ok := s.clients[cmd.UserID]
					s.mu.RUnlock()
					if ok {
						target.SendJSON(relay.NewMessage(relay.MsgCommandReject, payload))
					}
					queue.Reject(cmdID)
				}
				s.broadcastToRoom(room.Code, relay.NewMessage(relay.MsgCommandReject, payload), "")

			case relay.MsgPing:
				client.SendJSON(relay.NewMessage(relay.MsgPong, nil))
			}
			return nil
		})
		if err != nil {
			logger.Warn("client %s read error: %v", client.ID, err)
		}
	}()

	wg.Wait()
}

func (s *Server) broadcastToRoom(roomCode string, msg *relay.Message, exceptClientID string) {
	room, ok := s.rooms.Get(roomCode)
	if !ok {
		return
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	room.Mu.RLock()
	defer room.Mu.RUnlock()
	for id := range room.Users {
		if id == exceptClientID {
			continue
		}
		s.mu.RLock()
		client, ok := s.clients[id]
		s.mu.RUnlock()
		if ok {
			select {
			case client.Send <- data:
			default:
			}
		}
	}
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func Run(port int) {
	srv := New()
	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.ServeHTTP)

	addr := fmt.Sprintf(":%d", port)
	logger.Info("listening on %s", addr)

	srvHTTP := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	err := srvHTTP.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		logger.Error("server stopped: %v", err)
	}
}
