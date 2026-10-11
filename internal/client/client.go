// Package client is a Go client for the hq HTTP API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gronnbeck/hq/internal/api"
)

type Client struct {
	BaseURL string
	// Token is sent as a bearer token: a session, access or agent token,
	// depending on the call.
	Token string
	HTTP  *http.Client
}

// Error is a non-2xx response from the server.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

func (c *Client) Signup(ctx context.Context, email, password string) (api.Session, error) {
	var out api.Session
	err := c.do(ctx, "POST", "/v1/users", api.Credentials{Email: email, Password: password}, &out)
	return out, err
}

// CreateUser creates a user as an admin; the client's token must be the
// server's admin token.
func (c *Client) CreateUser(ctx context.Context, email, password string) (api.User, error) {
	var out api.User
	err := c.do(ctx, "POST", "/v1/users", api.Credentials{Email: email, Password: password}, &out)
	return out, err
}

func (c *Client) Login(ctx context.Context, email, password string) (api.Session, error) {
	var out api.Session
	err := c.do(ctx, "POST", "/v1/sessions", api.Credentials{Email: email, Password: password}, &out)
	return out, err
}

func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, "DELETE", "/v1/sessions/current", nil, nil)
}

func (c *Client) CreateAccessToken(ctx context.Context, name string) (api.AccessToken, error) {
	var out api.AccessToken
	err := c.do(ctx, "POST", "/v1/access-tokens", api.CreateAccessToken{Name: name}, &out)
	return out, err
}

func (c *Client) ListAccessTokens(ctx context.Context) ([]api.AccessToken, error) {
	var out []api.AccessToken
	err := c.do(ctx, "GET", "/v1/access-tokens", nil, &out)
	return out, err
}

func (c *Client) RevokeAccessToken(ctx context.Context, name string) error {
	return c.do(ctx, "DELETE", "/v1/access-tokens/"+url.PathEscape(name), nil, nil)
}

// CreateAgentToken exchanges the client's access token for a temporary token
// tied to the agent's identity.
func (c *Client) CreateAgentToken(ctx context.Context, agent string) (api.AgentToken, error) {
	var out api.AgentToken
	err := c.do(ctx, "POST", "/v1/agent-tokens", api.CreateAgentToken{Agent: agent}, &out)
	return out, err
}

func (c *Client) CreateMessage(ctx context.Context, body string) (api.Message, error) {
	var out api.Message
	err := c.do(ctx, "POST", "/v1/messages", api.CreateMessage{Body: body}, &out)
	return out, err
}

// ListMessages returns up to limit messages with IDs greater than after.
func (c *Client) ListMessages(ctx context.Context, after int64, limit int) ([]api.Message, error) {
	q := url.Values{}
	q.Set("after", strconv.FormatInt(after, 10))
	q.Set("limit", strconv.Itoa(limit))
	var out []api.Message
	err := c.do(ctx, "GET", "/v1/messages?"+q.Encode(), nil, &out)
	return out, err
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e api.Error
		if json.NewDecoder(resp.Body).Decode(&e) != nil || e.Error == "" {
			e.Error = fmt.Sprintf("server returned %s", resp.Status)
		}
		return &Error{Status: resp.StatusCode, Message: e.Error}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
