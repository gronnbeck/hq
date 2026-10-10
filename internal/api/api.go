// Package api defines the JSON bodies the hq server and its clients exchange.
package api

import "time"

type Credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type Session struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type CreateAccessToken struct {
	Name string `json:"name"`
}

type AccessToken struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	// Token is set only in the response that creates it.
	Token string `json:"token,omitempty"`
}

type CreateAgentToken struct {
	Agent string `json:"agent"`
}

type AgentToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type CreateMessage struct {
	Body string `json:"body"`
}

type Message struct {
	ID        int64     `json:"id"`
	Agent     string    `json:"agent"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type Error struct {
	Error string `json:"error"`
}
