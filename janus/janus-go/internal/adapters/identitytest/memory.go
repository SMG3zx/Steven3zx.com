// Package identitytest holds an in-memory app.Identities and the contract
// suite every app.Identities adapter, real or test, must pass.
package identitytest

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/google/uuid"

	"janus/internal/app"
)

type user struct{ id, email, displayName string }

type provider struct{ name, subject string }

type gitHub struct{ subject, token string }

// Memory is an app.Identities over maps, safe for concurrent use.
type Memory struct {
	mu         sync.Mutex
	users      map[string]*user
	passwords  map[string]string // user ID to password hash, for local users
	identities map[provider]string
	github     map[string]gitHub
}

var _ app.Identities = (*Memory)(nil)

// NewMemory returns an empty Memory.
func NewMemory() *Memory {
	return &Memory{users: map[string]*user{}, passwords: map[string]string{}, identities: map[provider]string{}, github: map[string]gitHub{}}
}

func (m *Memory) CreateLocalUser(_ context.Context, local app.CreateLocalUser) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	email := strings.TrimSpace(local.Email)
	if local.ID == "" || email == "" || local.PasswordHash == "" {
		return errors.New("a local user needs an ID, an email and a password hash")
	}
	if _, ok := m.users[local.ID]; ok {
		return &app.Conflict{Reason: "account already exists"}
	}
	for id := range m.passwords {
		if strings.EqualFold(m.users[id].email, email) {
			return &app.Conflict{Reason: "account already exists"}
		}
	}
	m.users[local.ID] = &user{id: local.ID, email: email}
	m.passwords[local.ID] = local.PasswordHash
	return nil
}

func (m *Memory) FindLocalUser(_ context.Context, email string) (app.LocalUserFound, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	email = strings.TrimSpace(email)
	for id, hash := range m.passwords {
		if u := m.users[id]; strings.EqualFold(u.email, email) {
			return app.LocalUserFound{ID: id, Email: u.email, PasswordHash: hash, Found: true}, nil
		}
	}
	return app.LocalUserFound{}, nil
}

func (m *Memory) FindUser(_ context.Context, id string) (app.UserFound, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return app.UserFound{}, nil
	}
	return app.UserFound{ID: u.id, Email: u.email, DisplayName: u.displayName, Found: true}, nil
}

func (m *Memory) DeleteUser(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[id]; !ok {
		return false, nil
	}
	for key, owner := range m.identities {
		if owner == id {
			delete(m.identities, key)
		}
	}
	delete(m.github, id)
	delete(m.passwords, id)
	delete(m.users, id)
	return true, nil
}

func (m *Memory) ResolveGoogleIdentity(_ context.Context, identity app.ResolveGoogleIdentity) (app.GoogleIdentityResolved, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if identity.Subject == "" {
		return app.GoogleIdentityResolved{}, errors.New("a Google identity needs a subject")
	}
	key := provider{"google", identity.Subject}
	if id, ok := m.identities[key]; ok {
		u := m.users[id]
		return app.GoogleIdentityResolved{ID: u.id, Email: u.email, DisplayName: u.displayName}, nil
	}
	id := uuid.NewString()
	m.users[id] = &user{id: id, email: identity.Email, displayName: identity.DisplayName}
	m.identities[key] = id
	return app.GoogleIdentityResolved{ID: id, Email: identity.Email, DisplayName: identity.DisplayName}, nil
}

func (m *Memory) ResolveGitHubLogin(_ context.Context, login app.ResolveGitHubLogin) (app.GitHubLoginResolved, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if login.Subject == "" {
		return app.GitHubLoginResolved{}, errors.New("a GitHub login needs a subject")
	}
	key := provider{"github", login.Subject}
	if id, ok := m.identities[key]; ok {
		u := m.users[id]
		return app.GitHubLoginResolved{ID: u.id, Email: u.email, DisplayName: u.displayName}, nil
	}
	email := strings.TrimSpace(login.Email)
	if email == "" {
		return app.GitHubLoginResolved{}, errors.New("GitHub did not return a verified primary email")
	}
	for _, u := range m.users {
		if u.email != "" && strings.EqualFold(u.email, email) {
			return app.GitHubLoginResolved{EmailInUse: true}, nil
		}
	}
	id := uuid.NewString()
	name := strings.TrimSpace(login.DisplayName)
	m.users[id] = &user{id: id, email: email, displayName: name}
	m.identities[key] = id
	return app.GitHubLoginResolved{ID: id, Email: email, DisplayName: name}, nil
}

func (m *Memory) ConnectGitHub(_ context.Context, connection app.ConnectGitHub) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if connection.UserID == "" || connection.Subject == "" || connection.Token == "" {
		return errors.New("incomplete GitHub connection")
	}
	if _, ok := m.users[connection.UserID]; !ok {
		return errors.New("no such User")
	}
	key := provider{"github", connection.Subject}
	if linked, ok := m.identities[key]; ok && linked != connection.UserID {
		return &app.Conflict{Reason: "GitHub account is connected to another Janus account"}
	}
	if existing, ok := m.github[connection.UserID]; ok && existing.subject != connection.Subject {
		return &app.Conflict{Reason: "a different GitHub account is already connected"}
	}
	m.identities[key] = connection.UserID
	m.github[connection.UserID] = gitHub{subject: connection.Subject, token: connection.Token}
	return nil
}

func (m *Memory) FindGitHubToken(_ context.Context, userID string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	connection, ok := m.github[userID]
	return connection.token, ok, nil
}

func (m *Memory) DisconnectGitHub(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.github, userID)
	return nil
}
