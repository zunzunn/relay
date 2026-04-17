package relay

import (
	"sync"
	"time"
)

// CommandID is a unique identifier for a queued command.
type CommandID string

// QueuedCommand represents a command submitted by a participant.
type QueuedCommand struct {
	ID        string    `json:"command_id"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Command   string    `json:"command"`
	Timestamp time.Time `json:"timestamp"`
	Status    string    `json:"status"` // pending | approved | rejected
}

// CmdQueue holds pending commands for a room.
type CmdQueue struct {
	mu       sync.RWMutex
	commands map[string]*QueuedCommand
	ordered  []string // command IDs in submission order
}

func NewCommandQueue() *CmdQueue {
	return &CmdQueue{
		commands: make(map[string]*QueuedCommand),
		ordered:  make([]string, 0),
	}
}

func (q *CmdQueue) Enqueue(cmd *QueuedCommand) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.commands[cmd.ID] = cmd
	q.ordered = append(q.ordered, cmd.ID)
	cmd.Status = "pending"
}

func (q *CmdQueue) Approve(id string) *QueuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()
	if cmd, ok := q.commands[id]; ok {
		cmd.Status = "approved"
		return cmd
	}
	return nil
}

func (q *CmdQueue) Reject(id string) *QueuedCommand {
	q.mu.Lock()
	defer q.mu.Unlock()
	if cmd, ok := q.commands[id]; ok {
		cmd.Status = "rejected"
		return cmd
	}
	return nil
}

func (q *CmdQueue) Get(id string) (*QueuedCommand, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	cmd, ok := q.commands[id]
	return cmd, ok
}

func (q *CmdQueue) Pending() []*QueuedCommand {
	q.mu.RLock()
	defer q.mu.RUnlock()
	var pending []*QueuedCommand
	for _, id := range q.ordered {
		if cmd, ok := q.commands[id]; ok && cmd.Status == "pending" {
			pending = append(pending, cmd)
		}
	}
	return pending
}

func (q *CmdQueue) All() []*QueuedCommand {
	q.mu.RLock()
	defer q.mu.RUnlock()
	var all []*QueuedCommand
	for _, id := range q.ordered {
		if cmd, ok := q.commands[id]; ok {
			all = append(all, cmd)
		}
	}
	return all
}