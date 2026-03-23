package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	_ "modernc.org/sqlite"
)

const sqliteDriverName = "sqlite"

var (
	ulidEntropy = ulid.Monotonic(rand.Reader, 0)
	ulidMu      sync.Mutex
)

// Task is the persisted task row used by the sidebar.
type Task struct {
	ID         string
	Name       string
	Worktree   string
	ArchivedAt string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Message is one persisted conversation entry associated with a task.
type Message struct {
	ID        string
	TaskID    string
	Role      string
	Content   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store wraps Orb's SQLite persistence layer.
type Store struct {
	db *sql.DB
}

// OpenDefault opens the default Orb SQLite database and ensures schema.
func OpenDefault(ctx context.Context) (*Store, error) {
	dataPath, err := defaultDBPath()
	if err != nil {
		return nil, fmt.Errorf("resolve default db path: %w", err)
	}
	return Open(ctx, dataPath)
}

// Open opens an Orb SQLite database file and ensures schema.
func Open(ctx context.Context, dbPath string) (*Store, error) {
	cleanPath := filepath.Clean(strings.TrimSpace(dbPath))
	if cleanPath == "" {
		return nil, errors.New("open store: empty database path")
	}

	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	db, err := sql.Open(sqliteDriverName, cleanPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite database: %w", err)
	}

	store := &Store{db: db}
	if err := store.ensureSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

// Close closes the underlying SQLite database connection.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close sqlite database: %w", err)
	}
	return nil
}

// ListActiveTasks returns unarchived tasks sorted by updated_at descending.
func (s *Store) ListActiveTasks(ctx context.Context) ([]Task, error) {
	const query = `
	SELECT id, name, worktree_path, COALESCE(archived_at, ''), created_at, updated_at
	FROM tasks
	WHERE archived_at IS NULL
	ORDER BY updated_at DESC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list active tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]Task, 0)
	for rows.Next() {
		var (
			task          Task
			createdAtText string
			updatedAtText string
		)
		if err := rows.Scan(&task.ID, &task.Name, &task.Worktree, &task.ArchivedAt, &createdAtText, &updatedAtText); err != nil {
			return nil, fmt.Errorf("scan task row: %w", err)
		}
		createdAt, err := parseTimestamp(createdAtText)
		if err != nil {
			return nil, fmt.Errorf("parse task created_at %q: %w", createdAtText, err)
		}
		updatedAt, err := parseTimestamp(updatedAtText)
		if err != nil {
			return nil, fmt.Errorf("parse task updated_at %q: %w", updatedAtText, err)
		}
		task.CreatedAt = createdAt
		task.UpdatedAt = updatedAt
		tasks = append(tasks, task)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task rows: %w", err)
	}

	return tasks, nil
}

// CreateTask inserts a new task and returns it.
func (s *Store) CreateTask(ctx context.Context, name string) (Task, error) {
	taskName := strings.TrimSpace(name)
	if taskName == "" {
		return Task{}, errors.New("create task: name cannot be empty")
	}

	now := time.Now().UTC()
	id, err := newULID(now)
	if err != nil {
		return Task{}, fmt.Errorf("create task id: %w", err)
	}

	const query = `
	INSERT INTO tasks (id, name, worktree_path, archived_at, created_at, updated_at)
	VALUES (?, ?, '', NULL, ?, ?)`

	timestamp := formatTimestamp(now)
	if _, err := s.db.ExecContext(ctx, query, id, taskName, timestamp, timestamp); err != nil {
		return Task{}, fmt.Errorf("insert task: %w", err)
	}

	return Task{
		ID:        id,
		Name:      taskName,
		Worktree:  "",
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// ArchiveTask soft-deletes a task by setting archived_at and updated_at.
func (s *Store) ArchiveTask(ctx context.Context, taskID string) error {
	cleanID := strings.TrimSpace(taskID)
	if cleanID == "" {
		return errors.New("archive task: empty task id")
	}

	now := formatTimestamp(time.Now().UTC())
	const query = `
	UPDATE tasks
	SET archived_at = ?, updated_at = ?
	WHERE id = ? AND archived_at IS NULL`

	result, err := s.db.ExecContext(ctx, query, now, now, cleanID)
	if err != nil {
		return fmt.Errorf("archive task %q: %w", cleanID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("archive task rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("archive task %q: not found or already archived", cleanID)
	}

	return nil
}

// SetTaskWorktreePath saves a task-specific worktree path and refreshes updated_at.
func (s *Store) SetTaskWorktreePath(ctx context.Context, taskID string, worktreePath string) error {
	cleanID := strings.TrimSpace(taskID)
	if cleanID == "" {
		return errors.New("set task worktree path: empty task id")
	}

	normalizedPath := filepath.Clean(strings.TrimSpace(worktreePath))
	if normalizedPath == "." {
		normalizedPath = ""
	}

	now := formatTimestamp(time.Now().UTC())
	const query = `
	UPDATE tasks
	SET worktree_path = ?, updated_at = ?
	WHERE id = ? AND archived_at IS NULL`

	result, err := s.db.ExecContext(ctx, query, normalizedPath, now, cleanID)
	if err != nil {
		return fmt.Errorf("set task worktree path for %q: %w", cleanID, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("set task worktree path rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("set task worktree path for %q: not found or archived", cleanID)
	}

	return nil
}

// ListMessagesByTask returns task messages ordered by created_at ascending.
func (s *Store) ListMessagesByTask(ctx context.Context, taskID string) ([]Message, error) {
	cleanID := strings.TrimSpace(taskID)
	if cleanID == "" {
		return []Message{}, nil
	}

	const query = `
	SELECT id, task_id, role, content, created_at, updated_at
	FROM messages
	WHERE task_id = ?
	ORDER BY created_at ASC`

	rows, err := s.db.QueryContext(ctx, query, cleanID)
	if err != nil {
		return nil, fmt.Errorf("list messages for task %q: %w", cleanID, err)
	}
	defer rows.Close()

	messages := make([]Message, 0)
	for rows.Next() {
		var (
			message       Message
			createdAtText string
			updatedAtText string
		)
		if err := rows.Scan(
			&message.ID,
			&message.TaskID,
			&message.Role,
			&message.Content,
			&createdAtText,
			&updatedAtText,
		); err != nil {
			return nil, fmt.Errorf("scan message row: %w", err)
		}

		createdAt, err := parseTimestamp(createdAtText)
		if err != nil {
			return nil, fmt.Errorf("parse message created_at %q: %w", createdAtText, err)
		}
		updatedAt, err := parseTimestamp(updatedAtText)
		if err != nil {
			return nil, fmt.Errorf("parse message updated_at %q: %w", updatedAtText, err)
		}

		message.CreatedAt = createdAt
		message.UpdatedAt = updatedAt
		messages = append(messages, message)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate message rows: %w", err)
	}

	return messages, nil
}

// CreateMessage inserts a new task message and updates task.updated_at.
func (s *Store) CreateMessage(ctx context.Context, taskID string, role string, content string) (Message, error) {
	cleanTaskID := strings.TrimSpace(taskID)
	cleanRole := strings.TrimSpace(role)
	cleanContent := strings.TrimSpace(content)

	if cleanTaskID == "" {
		return Message{}, errors.New("create message: empty task id")
	}
	if cleanRole == "" {
		return Message{}, errors.New("create message: empty role")
	}
	if cleanContent == "" {
		return Message{}, errors.New("create message: empty content")
	}

	now := time.Now().UTC()
	id, err := newULID(now)
	if err != nil {
		return Message{}, fmt.Errorf("create message id: %w", err)
	}
	timestamp := formatTimestamp(now)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, fmt.Errorf("begin message transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	const insertMessageQuery = `
	INSERT INTO messages (id, task_id, role, content, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?)`
	if _, err := tx.ExecContext(
		ctx,
		insertMessageQuery,
		id,
		cleanTaskID,
		cleanRole,
		cleanContent,
		timestamp,
		timestamp,
	); err != nil {
		return Message{}, fmt.Errorf("insert message: %w", err)
	}

	const touchTaskQuery = `
	UPDATE tasks
	SET updated_at = ?
	WHERE id = ? AND archived_at IS NULL`
	result, err := tx.ExecContext(ctx, touchTaskQuery, timestamp, cleanTaskID)
	if err != nil {
		return Message{}, fmt.Errorf("touch task updated_at for message: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return Message{}, fmt.Errorf("message task rows affected: %w", err)
	}
	if affected == 0 {
		return Message{}, fmt.Errorf("message task %q not found or archived", cleanTaskID)
	}

	if err := tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("commit message transaction: %w", err)
	}

	return Message{
		ID:        id,
		TaskID:    cleanTaskID,
		Role:      cleanRole,
		Content:   cleanContent,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (s *Store) ensureSchema(ctx context.Context) error {
	const schema = `
	CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		worktree_path TEXT NOT NULL DEFAULT '',
		archived_at TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_tasks_active_updated
	ON tasks (archived_at, updated_at DESC);

	CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		FOREIGN KEY(task_id) REFERENCES tasks(id)
	);

	CREATE INDEX IF NOT EXISTS idx_messages_task_created
	ON messages (task_id, created_at ASC);
	`

	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("ensure sqlite schema: %w", err)
	}
	return nil
}

func defaultDBPath() (string, error) {
	xdgDataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if xdgDataHome != "" {
		return filepath.Join(xdgDataHome, "orb", "orb.sqlite3"), nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}

	return filepath.Join(homeDir, ".local", "share", "orb", "orb.sqlite3"), nil
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

func parseTimestamp(value string) (time.Time, error) {
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return timestamp, nil
}

func formatTimestamp(timestamp time.Time) string {
	return timestamp.UTC().Format(time.RFC3339Nano)
}
