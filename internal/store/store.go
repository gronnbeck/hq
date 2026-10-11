// Package store persists hq's data in SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id            INTEGER PRIMARY KEY,
	email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL,
	created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	id         INTEGER PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	token_hash TEXT NOT NULL UNIQUE,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS access_tokens (
	id         INTEGER PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	name       TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	created_at INTEGER NOT NULL,
	UNIQUE (user_id, name)
);

CREATE TABLE IF NOT EXISTS agent_tokens (
	id              INTEGER PRIMARY KEY,
	access_token_id INTEGER NOT NULL REFERENCES access_tokens(id) ON DELETE CASCADE,
	agent           TEXT NOT NULL,
	token_hash      TEXT NOT NULL UNIQUE,
	created_at      INTEGER NOT NULL,
	expires_at      INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
	id         INTEGER PRIMARY KEY,
	user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	agent      TEXT NOT NULL,
	body       TEXT NOT NULL,
	created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS messages_user_id ON messages (user_id, id);
`

type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens (creating if needed) the SQLite database at path.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer at a time; a single connection avoids lock errors.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

type User struct {
	ID           int64
	Email        string
	PasswordHash string
}

type AccessToken struct {
	ID        int64
	UserID    int64
	Name      string
	CreatedAt time.Time
}

type Agent struct {
	UserID int64
	Name   string
}

type Message struct {
	ID        int64
	Agent     string
	Body      string
	CreatedAt time.Time
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func (s *Store) CreateUser(ctx context.Context, email, passwordHash string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (email, password_hash, created_at) VALUES (?, ?, ?)`,
		email, passwordHash, s.now().Unix())
	if isUniqueViolation(err) {
		return 0, ErrConflict
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash FROM users WHERE email = ?`, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// CreateSession logs a user in and returns when the session expires.
func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash string, ttl time.Duration) (time.Time, error) {
	now := s.now()
	expires := now.Add(ttl)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (user_id, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		userID, tokenHash, now.Unix(), expires.Unix())
	return expires.UTC(), err
}

// UserIDBySession returns the user that owns an unexpired session.
func (s *Store) UserIDBySession(ctx context.Context, tokenHash string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, s.now().Unix()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) CreateAccessToken(ctx context.Context, userID int64, name, tokenHash string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO access_tokens (user_id, name, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		userID, name, tokenHash, s.now().Unix())
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

func (s *Store) ListAccessTokens(ctx context.Context, userID int64) ([]AccessToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, name, created_at FROM access_tokens WHERE user_id = ? ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := []AccessToken{}
	for rows.Next() {
		var t AccessToken
		var created int64
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &created); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(created, 0).UTC()
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// DeleteAccessToken revokes an access token and, by cascade, every agent
// token issued from it.
func (s *Store) DeleteAccessToken(ctx context.Context, userID int64, name string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM access_tokens WHERE user_id = ? AND name = ?`, userID, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AccessTokenByHash(ctx context.Context, tokenHash string) (AccessToken, error) {
	var t AccessToken
	var created int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, name, created_at FROM access_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&t.ID, &t.UserID, &t.Name, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return AccessToken{}, ErrNotFound
	}
	t.CreatedAt = time.Unix(created, 0).UTC()
	return t, err
}

// CreateAgentToken issues a temporary token for an agent and returns when it
// expires.
func (s *Store) CreateAgentToken(ctx context.Context, accessTokenID int64, agent, tokenHash string, ttl time.Duration) (time.Time, error) {
	now := s.now()
	expires := now.Add(ttl)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_tokens (access_token_id, agent, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		accessTokenID, agent, tokenHash, now.Unix(), expires.Unix())
	return expires.UTC(), err
}

// AgentByToken returns the agent behind an unexpired temporary token.
func (s *Store) AgentByToken(ctx context.Context, tokenHash string) (Agent, error) {
	var a Agent
	err := s.db.QueryRowContext(ctx, `
		SELECT a.user_id, t.agent
		FROM agent_tokens t JOIN access_tokens a ON a.id = t.access_token_id
		WHERE t.token_hash = ? AND t.expires_at > ?`,
		tokenHash, s.now().Unix()).Scan(&a.UserID, &a.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	return a, err
}

func (s *Store) CreateMessage(ctx context.Context, userID int64, agent, body string) (Message, error) {
	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (user_id, agent, body, created_at) VALUES (?, ?, ?, ?)`,
		userID, agent, body, now.Unix())
	if err != nil {
		return Message{}, err
	}
	id, err := res.LastInsertId()
	return Message{ID: id, Agent: agent, Body: body, CreatedAt: time.Unix(now.Unix(), 0).UTC()}, err
}

// ListMessages returns up to limit of a user's messages with IDs greater than
// after, oldest first.
func (s *Store) ListMessages(ctx context.Context, userID, after int64, limit int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, agent, body, created_at FROM messages
		WHERE user_id = ? AND id > ? ORDER BY id LIMIT ?`,
		userID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs := []Message{}
	for rows.Next() {
		var m Message
		var created int64
		if err := rows.Scan(&m.ID, &m.Agent, &m.Body, &created); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(created, 0).UTC()
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
