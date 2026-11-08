package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Command struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Cmd       string   `json:"cmd"`
	Tip       string   `json:"tip"`
	Tags      []string `json:"tags"`
	Scope     string   `json:"scope"` // "" = global
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
	LastRunAt int64    `json:"last_run_at"`
	RunCount  int64    `json:"run_count"`
}

const schema = `
CREATE TABLE IF NOT EXISTS commands (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT    NOT NULL,
  cmd         TEXT    NOT NULL,
  tip         TEXT    NOT NULL DEFAULT '',
  tags        TEXT    NOT NULL DEFAULT '',
  scope       TEXT    NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  last_run_at INTEGER NOT NULL DEFAULT 0,
  run_count   INTEGER NOT NULL DEFAULT 0,
  UNIQUE(scope, name)
);`

const cols = `id, name, cmd, tip, tags, scope, created_at, updated_at, last_run_at, run_count`

type Store struct{ db *sql.DB }

func dbPath() (string, error) {
	if p := os.Getenv("BUD_DB"); p != "" { // test/override hook
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "bud", "db.sqlite"), nil
}

func openStore() (*Store, error) {
	path, err := dbPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	p := path
	if runtime.GOOS == "windows" {
		p = "/" + filepath.ToSlash(path)
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init db %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func splitTags(s string) []string {
	out := []string{}
	for _, t := range strings.Split(strings.Trim(s, ","), ",") {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func joinTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return "," + strings.Join(tags, ",") + ","
}

type scanner interface{ Scan(dest ...any) error }

func scanCmd(s scanner) (*Command, error) {
	var c Command
	var tags string
	if err := s.Scan(&c.ID, &c.Name, &c.Cmd, &c.Tip, &tags, &c.Scope,
		&c.CreatedAt, &c.UpdatedAt, &c.LastRunAt, &c.RunCount); err != nil {
		return nil, err
	}
	c.Tags = splitTags(tags)
	return &c, nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

func (s *Store) List() ([]Command, error) {
	rows, err := s.db.Query(`SELECT ` + cols + ` FROM commands ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Command{}
	for rows.Next() {
		c, err := scanCmd(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) byName(scope, name string) (*Command, error) {
	return scanCmd(s.db.QueryRow(`SELECT `+cols+` FROM commands WHERE scope = ? AND name = ?`, scope, name))
}

// Resolve finds a command by name in the current scope, then globally.
func (s *Store) Resolve(name, scope string) (*Command, error) {
	for _, sc := range []string{scope, ""} {
		c, err := s.byName(sc, name)
		if err == nil {
			return c, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	all, err := s.List()
	if err == nil {
		var elsewhere []string
		for _, c := range all {
			if c.Name == name {
				elsewhere = append(elsewhere, scopeLabel(c.Scope))
			}
		}
		if len(elsewhere) > 0 {
			return nil, fmt.Errorf("no command %q in this scope or globally; it exists in: %s",
				name, strings.Join(elsewhere, ", "))
		}
	}
	return nil, fmt.Errorf("no command %q in this scope or globally", name)
}

func (s *Store) Create(c *Command) error {
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO commands (name, cmd, tip, tags, scope, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, c.Name, c.Cmd, c.Tip, joinTags(c.Tags), c.Scope, now, now)
	if err != nil {
		return err
	}
	c.ID, _ = res.LastInsertId()
	c.CreatedAt, c.UpdatedAt = now, now
	return nil
}

// Upsert inserts, or updates the existing row with the same scope+name.
func (s *Store) Upsert(c *Command) (created bool, err error) {
	existing, err := s.byName(c.Scope, c.Name)
	switch {
	case err == nil:
		c.ID = existing.ID
		_, err = s.db.Exec(`UPDATE commands SET cmd = ?, tip = ?, tags = ?, updated_at = ? WHERE id = ?`,
			c.Cmd, c.Tip, joinTags(c.Tags), time.Now().Unix(), c.ID)
		return false, err
	case errors.Is(err, sql.ErrNoRows):
		return true, s.Create(c)
	default:
		return false, err
	}
}

func (s *Store) Update(c *Command) error {
	res, err := s.db.Exec(`UPDATE commands SET name = ?, cmd = ?, tip = ?, tags = ?, scope = ?, updated_at = ?
		WHERE id = ?`, c.Name, c.Cmd, c.Tip, joinTags(c.Tags), c.Scope, time.Now().Unix(), c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) Delete(id int64) error {
	res, err := s.db.Exec(`DELETE FROM commands WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) RecordRun(id int64) {
	s.db.Exec(`UPDATE commands SET last_run_at = ?, run_count = run_count + 1 WHERE id = ?`, time.Now().Unix(), id)
}
