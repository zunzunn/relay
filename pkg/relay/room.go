package relay

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
)

// Room represents a shared terminal session.
type Room struct {
	Code     string
	Password []byte // bcrypt hash, empty if no password
	HostID   string
	Mu       sync.RWMutex
	Users    map[string]*UserInfo // userID -> info
	Seq      uint64
}

func (r *Room) AddUser(id string, info *UserInfo) {
	r.Mu.Lock()
	defer r.Mu.Unlock()
	r.Users[id] = info
	if info.IsHost {
		r.HostID = id
	}
}

func (r *Room) RemoveUser(id string) *UserInfo {
	r.Mu.Lock()
	defer r.Mu.Unlock()
	u, ok := r.Users[id]
	if ok {
		delete(r.Users, id)
	}
	return u
}

func (r *Room) GetUser(id string) (*UserInfo, bool) {
	r.Mu.RLock()
	defer r.Mu.RUnlock()
	u, ok := r.Users[id]
	return u, ok
}

func (r *Room) GetHostID() string {
	r.Mu.RLock()
	defer r.Mu.RUnlock()
	return r.HostID
}

func (r *Room) NextSeq() uint64 {
	r.Mu.Lock()
	defer r.Mu.Unlock()
	r.Seq++
	return r.Seq
}

func (r *Room) HasPassword() bool {
	r.Mu.RLock()
	defer r.Mu.RUnlock()
	return len(r.Password) > 0
}

func (r *Room) UserCount() int {
	r.Mu.RLock()
	defer r.Mu.RUnlock()
	return len(r.Users)
}

// RoomRegistry manages all active rooms.
type RoomRegistry struct {
	mu    sync.RWMutex
	rooms map[string]*Room
}

func NewRoomRegistry() *RoomRegistry {
	return &RoomRegistry{rooms: make(map[string]*Room)}
}

// GenerateRoomCode creates a short, URL-safe room code.
func GenerateRoomCode() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic("rand.Read failed: " + err.Error())
	}
	return base64.URLEncoding.EncodeToString(b)[:6]
}

func (r *RoomRegistry) Create(code string, passwordHash []byte) *Room {
	r.mu.Lock()
	defer r.mu.Unlock()
	room := &Room{
		Code:     code,
		Password: passwordHash,
		Users:    make(map[string]*UserInfo),
	}
	r.rooms[code] = room
	return room
}

func (r *RoomRegistry) Get(code string) (*Room, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	room, ok := r.rooms[code]
	return room, ok
}

func (r *RoomRegistry) Delete(code string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rooms, code)
}

func (r *RoomRegistry) Exists(code string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.rooms[code]
	return ok
}

// ToPayload converts a UserInfo to its JSON-serializable payload form.
func (u *UserInfo) ToPayload() *UserInfoPayload {
	return &UserInfoPayload{
		ID:       u.ID,
		Username: u.Username,
		IsHost:   u.IsHost,
		Color:    u.Color,
	}
}

// UserInfoPayload is the JSON-serializable form of UserInfo.
type UserInfoPayload struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	IsHost   bool   `json:"is_host"`
	Color    string `json:"color"`
}
