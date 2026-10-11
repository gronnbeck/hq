// Package server implements the hq HTTP API.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"

	"github.com/gronnbeck/hq/internal/api"
	"github.com/gronnbeck/hq/internal/store"
	"github.com/gronnbeck/hq/internal/token"
)

const (
	SessionTTL    = 30 * 24 * time.Hour
	AgentTokenTTL = time.Hour

	minPasswordLen = 8
	maxNameLen     = 64
	maxMessageLen  = 10_000
	maxBodyBytes   = 64 << 10
	defaultLimit   = 100
	maxLimit       = 500
)

// dummyHash is compared against when a login names an unknown email, so the
// response takes as long as a wrong password would.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("hq-dummy-password"), bcrypt.DefaultCost)

type Options struct {
	// AdminToken, when set, lets its bearer create users. Empty disables
	// admin access.
	AdminToken string
	// OpenSignup lets anyone create an account. Off by default: a public
	// server should only have users an admin created.
	OpenSignup bool
}

type Server struct {
	store *store.Store
	opts  Options
	mux   *http.ServeMux
}

func New(st *store.Store, opts Options) *Server {
	s := &Server{store: st, opts: opts, mux: http.NewServeMux()}

	s.mux.HandleFunc("GET /up", s.health)
	s.mux.HandleFunc("POST /v1/users", s.signup)
	s.mux.HandleFunc("POST /v1/sessions", s.login)
	s.mux.HandleFunc("DELETE /v1/sessions/current", s.withUser(s.logout))

	s.mux.HandleFunc("POST /v1/access-tokens", s.withUser(s.createAccessToken))
	s.mux.HandleFunc("GET /v1/access-tokens", s.withUser(s.listAccessTokens))
	s.mux.HandleFunc("DELETE /v1/access-tokens/{name}", s.withUser(s.revokeAccessToken))

	s.mux.HandleFunc("POST /v1/agent-tokens", s.createAgentToken)
	s.mux.HandleFunc("POST /v1/messages", s.withAgent(s.createMessage))
	s.mux.HandleFunc("GET /v1/messages", s.withUser(s.listMessages))

	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// Handlers.

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		internalError(w, err)
		return
	}
	w.Write([]byte("ok\n"))
}

// signup creates a user. An admin creating a user gets the user back; someone
// signing themselves up (only with OpenSignup) is also logged in.
func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	tok, hasToken := bearer(r)
	asAdmin := hasToken && s.isAdmin(tok)
	switch {
	case hasToken && !asAdmin:
		writeError(w, http.StatusUnauthorized, "invalid admin token")
		return
	case !asAdmin && !s.opts.OpenSignup:
		writeError(w, http.StatusForbidden, "signup is closed; ask an admin to create your account")
		return
	}

	var req api.Credentials
	if !decode(w, r, &req) {
		return
	}
	email, err := mail.ParseAddress(req.Email)
	if err != nil || email.Address != strings.TrimSpace(req.Email) {
		writeError(w, http.StatusBadRequest, "invalid email address")
		return
	}
	if len(req.Password) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		// bcrypt rejects passwords over 72 bytes.
		writeError(w, http.StatusBadRequest, "invalid password")
		return
	}
	userID, err := s.store.CreateUser(r.Context(), email.Address, string(hash))
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "email is already registered")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	if asAdmin {
		writeJSON(w, http.StatusCreated, api.User{Email: email.Address})
		return
	}
	s.startSession(w, r, userID)
}

// isAdmin compares in constant time so the token can't be guessed byte by
// byte from response times.
func (s *Server) isAdmin(tok string) bool {
	if s.opts.AdminToken == "" {
		return false
	}
	want := sha256.Sum256([]byte(s.opts.AdminToken))
	got := sha256.Sum256([]byte(tok))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req api.Credentials
	if !decode(w, r, &req) {
		return
	}
	u, err := s.store.UserByEmail(r.Context(), strings.TrimSpace(req.Email))
	if errors.Is(err, store.ErrNotFound) {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
		writeError(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	s.startSession(w, r, u.ID)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID int64) {
	tok := token.New(token.Session)
	expires, err := s.store.CreateSession(r.Context(), userID, token.Hash(tok), SessionTTL)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, api.Session{Token: tok, ExpiresAt: expires})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, _ int64) {
	tok, _ := bearer(r)
	if err := s.store.DeleteSession(r.Context(), token.Hash(tok)); err != nil {
		internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createAccessToken(w http.ResponseWriter, r *http.Request, userID int64) {
	var req api.CreateAccessToken
	if !decode(w, r, &req) {
		return
	}
	if msg := validateName(req.Name); msg != "" {
		writeError(w, http.StatusBadRequest, "token name "+msg)
		return
	}
	tok := token.New(token.Access)
	if err := s.store.CreateAccessToken(r.Context(), userID, req.Name, token.Hash(tok)); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "a token with that name already exists")
			return
		}
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, api.AccessToken{Name: req.Name, CreatedAt: time.Now().UTC().Truncate(time.Second), Token: tok})
}

func (s *Server) listAccessTokens(w http.ResponseWriter, r *http.Request, userID int64) {
	tokens, err := s.store.ListAccessTokens(r.Context(), userID)
	if err != nil {
		internalError(w, err)
		return
	}
	out := make([]api.AccessToken, len(tokens))
	for i, t := range tokens {
		out[i] = api.AccessToken{Name: t.Name, CreatedAt: t.CreatedAt}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) revokeAccessToken(w http.ResponseWriter, r *http.Request, userID int64) {
	err := s.store.DeleteAccessToken(r.Context(), userID, r.PathValue("name"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no token with that name")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// createAgentToken exchanges an access token plus an agent's self-chosen
// identity for a temporary token tied to that identity.
func (s *Server) createAgentToken(w http.ResponseWriter, r *http.Request) {
	tok, ok := bearer(r)
	if !ok || !strings.HasPrefix(tok, token.Access) {
		writeError(w, http.StatusUnauthorized, "an access token is required")
		return
	}
	at, err := s.store.AccessTokenByHash(r.Context(), token.Hash(tok))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "invalid access token")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	var req api.CreateAgentToken
	if !decode(w, r, &req) {
		return
	}
	if msg := validateName(req.Agent); msg != "" {
		writeError(w, http.StatusBadRequest, "agent identity "+msg)
		return
	}
	agentTok := token.New(token.Agent)
	expires, err := s.store.CreateAgentToken(r.Context(), at.ID, req.Agent, token.Hash(agentTok), AgentTokenTTL)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, api.AgentToken{Token: agentTok, ExpiresAt: expires})
}

func (s *Server) createMessage(w http.ResponseWriter, r *http.Request, agent store.Agent) {
	var req api.CreateMessage
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Body) == "" {
		writeError(w, http.StatusBadRequest, "message body is empty")
		return
	}
	if len(req.Body) > maxMessageLen {
		writeError(w, http.StatusBadRequest, "message body is over 10000 bytes")
		return
	}
	m, err := s.store.CreateMessage(r.Context(), agent.UserID, agent.Name, req.Body)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toAPIMessage(m))
}

// listMessages returns the user's messages oldest first. Clients poll with
// ?after=<last id seen> to fetch only new ones.
func (s *Server) listMessages(w http.ResponseWriter, r *http.Request, userID int64) {
	after, err := queryInt(r, "after", 0)
	if err != nil || after < 0 {
		writeError(w, http.StatusBadRequest, "after must be a non-negative integer")
		return
	}
	limit, err := queryInt(r, "limit", defaultLimit)
	if err != nil || limit < 1 || limit > maxLimit {
		writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
		return
	}
	msgs, err := s.store.ListMessages(r.Context(), userID, after, int(limit))
	if err != nil {
		internalError(w, err)
		return
	}
	out := make([]api.Message, len(msgs))
	for i, m := range msgs {
		out[i] = toAPIMessage(m)
	}
	writeJSON(w, http.StatusOK, out)
}

// Authentication.

func (s *Server) withUser(h func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := bearer(r)
		if !ok || !strings.HasPrefix(tok, token.Session) {
			writeError(w, http.StatusUnauthorized, "log in first")
			return
		}
		userID, err := s.store.UserIDBySession(r.Context(), token.Hash(tok))
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "session is invalid or expired; log in again")
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
		h(w, r, userID)
	}
}

func (s *Server) withAgent(h func(http.ResponseWriter, *http.Request, store.Agent)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := bearer(r)
		if !ok || !strings.HasPrefix(tok, token.Agent) {
			writeError(w, http.StatusUnauthorized, "an agent token is required")
			return
		}
		agent, err := s.store.AgentByToken(r.Context(), token.Hash(tok))
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "agent token is invalid or expired")
			return
		}
		if err != nil {
			internalError(w, err)
			return
		}
		h(w, r, agent)
	}
}

func bearer(r *http.Request) (string, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return tok, ok && tok != ""
}

// Helpers.

func validateName(name string) string {
	if name == "" {
		return "is empty"
	}
	if len(name) > maxNameLen {
		return "is over 64 bytes"
	}
	for _, c := range name {
		if unicode.IsControl(c) || unicode.IsSpace(c) || c == '/' {
			return "can't contain spaces, slashes or control characters"
		}
	}
	return ""
}

func toAPIMessage(m store.Message) api.Message {
	return api.Message{ID: m.ID, Agent: m.Agent, Body: m.Body, CreatedAt: m.CreatedAt}
}

func queryInt(r *http.Request, key string, def int64) (int64, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def, nil
	}
	return strconv.ParseInt(v, 10, 64)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, api.Error{Error: msg})
}

func internalError(w http.ResponseWriter, err error) {
	if !errors.Is(err, context.Canceled) {
		log.Printf("internal error: %v", err)
	}
	writeError(w, http.StatusInternalServerError, "internal error")
}
