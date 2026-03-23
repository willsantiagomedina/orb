package agent

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	ulidEntropy = ulid.Monotonic(rand.Reader, 0)
	ulidMu      sync.Mutex
)

type state struct {
	Snapshot
}

// Pool manages multi-agent lifecycle and thread-safe status updates.
type Pool struct {
	mu      sync.RWMutex
	agents  map[string]*state
	order   []string
	active  string
	created int
}

// NewPool creates an empty agent pool.
func NewPool() *Pool {
	return &Pool{
		agents: make(map[string]*state),
		order:  make([]string, 0, 4),
	}
}

// Create registers a new agent and returns its snapshot.
func (p *Pool) Create(input CreateInput) (Snapshot, error) {
	if p == nil {
		return Snapshot{}, errors.New("create agent: nil pool")
	}

	now := time.Now().UTC()
	id, err := newULID(now)
	if err != nil {
		return Snapshot{}, fmt.Errorf("create agent id: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.created++
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = fmt.Sprintf("Agent %d", p.created)
	}
	backendID := strings.TrimSpace(input.BackendID)
	if backendID == "" {
		backendID = "codex"
	}

	snapshot := Snapshot{
		ID:          id,
		Name:        name,
		BackendID:   backendID,
		SessionName: strings.TrimSpace(input.SessionName),
		TaskID:      strings.TrimSpace(input.TaskID),
		Worktree:    strings.TrimSpace(input.Worktree),
		Status:      StatusIdle,
		Active:      false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	p.agents[id] = &state{Snapshot: snapshot}
	p.order = append(p.order, id)
	return snapshot, nil
}

// List returns agents in creation order.
func (p *Pool) List() []Snapshot {
	if p == nil {
		return []Snapshot{}
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	list := make([]Snapshot, 0, len(p.order))
	for _, id := range p.order {
		agentState, ok := p.agents[id]
		if !ok {
			continue
		}
		list = append(list, agentState.Snapshot)
	}
	return list
}

// Get returns a single agent snapshot by ID.
func (p *Pool) Get(id string) (Snapshot, bool) {
	if p == nil {
		return Snapshot{}, false
	}

	cleanID := strings.TrimSpace(id)
	if cleanID == "" {
		return Snapshot{}, false
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	agentState, ok := p.agents[cleanID]
	if !ok {
		return Snapshot{}, false
	}
	return agentState.Snapshot, true
}

// SetActive sets one agent as active and marks others inactive.
func (p *Pool) SetActive(id string) bool {
	if p == nil {
		return false
	}

	cleanID := strings.TrimSpace(id)
	if cleanID == "" {
		return false
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	target, ok := p.agents[cleanID]
	if !ok {
		return false
	}

	now := time.Now().UTC()
	for _, agentState := range p.agents {
		if agentState.Active {
			agentState.Active = false
			agentState.UpdatedAt = now
		}
	}

	target.Active = true
	target.UpdatedAt = now
	p.active = cleanID
	return true
}

// Active returns the active agent snapshot, if any.
func (p *Pool) Active() (Snapshot, bool) {
	if p == nil {
		return Snapshot{}, false
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if strings.TrimSpace(p.active) == "" {
		return Snapshot{}, false
	}
	agentState, ok := p.agents[p.active]
	if !ok {
		return Snapshot{}, false
	}
	return agentState.Snapshot, true
}

// SetStatus updates an agent runtime status.
func (p *Pool) SetStatus(id string, status Status) bool {
	if p == nil {
		return false
	}
	if status != StatusIdle && status != StatusThinking && status != StatusTool {
		status = StatusIdle
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	agentState, ok := p.agents[strings.TrimSpace(id)]
	if !ok {
		return false
	}
	agentState.Status = status
	agentState.UpdatedAt = time.Now().UTC()
	return true
}

// SetBackend updates an agent backend assignment.
func (p *Pool) SetBackend(id string, backendID string) bool {
	return p.update(id, func(snapshot *Snapshot) {
		snapshot.BackendID = strings.TrimSpace(backendID)
	})
}

// SetSession updates an agent session name.
func (p *Pool) SetSession(id string, sessionName string) bool {
	return p.update(id, func(snapshot *Snapshot) {
		snapshot.SessionName = strings.TrimSpace(sessionName)
	})
}

// SetTask updates an agent task ID.
func (p *Pool) SetTask(id string, taskID string) bool {
	return p.update(id, func(snapshot *Snapshot) {
		snapshot.TaskID = strings.TrimSpace(taskID)
	})
}

// SetWorktree updates an agent worktree path.
func (p *Pool) SetWorktree(id string, worktree string) bool {
	return p.update(id, func(snapshot *Snapshot) {
		snapshot.Worktree = strings.TrimSpace(worktree)
	})
}

func (p *Pool) update(id string, fn func(snapshot *Snapshot)) bool {
	if p == nil {
		return false
	}
	cleanID := strings.TrimSpace(id)
	if cleanID == "" {
		return false
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	agentState, ok := p.agents[cleanID]
	if !ok {
		return false
	}
	fn(&agentState.Snapshot)
	agentState.UpdatedAt = time.Now().UTC()
	return true
}

func newULID(now time.Time) (string, error) {
	ulidMu.Lock()
	defer ulidMu.Unlock()

	id, err := ulid.New(ulid.Timestamp(now), ulidEntropy)
	if err != nil {
		return "", fmt.Errorf("generate ulid: %w", err)
	}
	return id.String(), nil
}
