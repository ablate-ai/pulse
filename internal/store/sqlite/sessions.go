package sqlite

import (
	"database/sql"
	"time"
)

// SessionStore 将登录 session 持久化到 SQLite。
type SessionStore struct {
	db *sql.DB
}

func (s *SessionStore) Create(token, username string) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO sessions (token, username, created_at) VALUES (?, ?, ?)`,
		token, username, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (s *SessionStore) GetUsername(token string) (string, bool) {
	var username string
	err := s.db.QueryRow(`SELECT username FROM sessions WHERE token = ?`, token).Scan(&username)
	if err != nil {
		return "", false
	}
	return username, true
}

func (s *SessionStore) Delete(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}
