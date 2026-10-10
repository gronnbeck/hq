package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gronnbeck/hq/internal/client"
	"github.com/gronnbeck/hq/internal/server"
	"github.com/gronnbeck/hq/internal/store"
)

func newServer(t *testing.T) string {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hq.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ts := httptest.NewServer(server.New(st))
	t.Cleanup(ts.Close)
	return ts.URL
}

func signup(t *testing.T, url, email string) *client.Client {
	t.Helper()
	c := &client.Client{BaseURL: url}
	sess, err := c.Signup(context.Background(), email, "correct horse")
	if err != nil {
		t.Fatalf("signup %s: %v", email, err)
	}
	c.Token = sess.Token
	return c
}

// agent exchanges an access token for an agent token and returns a client
// that reports as that agent.
func agent(t *testing.T, url, accessToken, name string) *client.Client {
	t.Helper()
	c := &client.Client{BaseURL: url, Token: accessToken}
	at, err := c.CreateAgentToken(context.Background(), name)
	if err != nil {
		t.Fatalf("agent token for %s: %v", name, err)
	}
	c.Token = at.Token
	return c
}

func wantStatus(t *testing.T, err error, status int) {
	t.Helper()
	var apiErr *client.Error
	if !errors.As(err, &apiErr) || apiErr.Status != status {
		t.Fatalf("got error %v, want status %d", err, status)
	}
}

func TestAgentsReportToTheirUser(t *testing.T) {
	ctx := context.Background()
	url := newServer(t)
	user := signup(t, url, "ada@example.com")

	tok, err := user.CreateAccessToken(ctx, "build-box")
	if err != nil {
		t.Fatal(err)
	}
	// Two agents share one access token but keep their own identities.
	for _, a := range []struct{ name, body string }{
		{"reviewer", "PR #12 is ready"},
		{"builder", "build is green"},
	} {
		if _, err := agent(t, url, tok.Token, a.name).CreateMessage(ctx, a.body); err != nil {
			t.Fatalf("report as %s: %v", a.name, err)
		}
	}

	msgs, err := user.ListMessages(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Agent != "reviewer" || msgs[0].Body != "PR #12 is ready" || msgs[1].Agent != "builder" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}

	newer, err := user.ListMessages(ctx, msgs[0].ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(newer) != 1 || newer[0].ID != msgs[1].ID {
		t.Fatalf("after=%d returned %+v", msgs[0].ID, newer)
	}
}

func TestUsersOnlySeeTheirOwnMessages(t *testing.T) {
	ctx := context.Background()
	url := newServer(t)
	ada := signup(t, url, "ada@example.com")
	bob := signup(t, url, "bob@example.com")

	tok, err := ada.CreateAccessToken(ctx, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent(t, url, tok.Token, "a1").CreateMessage(ctx, "for ada"); err != nil {
		t.Fatal(err)
	}

	msgs, err := bob.ListMessages(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("bob sees ada's messages: %+v", msgs)
	}
	ts, err := bob.ListAccessTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 0 {
		t.Fatalf("bob sees ada's tokens: %+v", ts)
	}
	wantStatus(t, bob.RevokeAccessToken(ctx, "laptop"), http.StatusNotFound)
}

func TestRevokingAnAccessTokenRevokesItsAgentTokens(t *testing.T) {
	ctx := context.Background()
	url := newServer(t)
	user := signup(t, url, "ada@example.com")
	tok, err := user.CreateAccessToken(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	a := agent(t, url, tok.Token, "a1")

	if err := user.RevokeAccessToken(ctx, "box"); err != nil {
		t.Fatal(err)
	}
	_, err = a.CreateMessage(ctx, "hello")
	wantStatus(t, err, http.StatusUnauthorized)

	_, err = (&client.Client{BaseURL: url, Token: tok.Token}).CreateAgentToken(ctx, "a2")
	wantStatus(t, err, http.StatusUnauthorized)
}

func TestTokensOnlyWorkForTheirOwnPurpose(t *testing.T) {
	ctx := context.Background()
	url := newServer(t)
	user := signup(t, url, "ada@example.com")
	tok, err := user.CreateAccessToken(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	access := &client.Client{BaseURL: url, Token: tok.Token}
	a := agent(t, url, tok.Token, "a1")

	// An access token can't act as a user or report directly.
	_, err = access.ListMessages(ctx, 0, 10)
	wantStatus(t, err, http.StatusUnauthorized)
	_, err = access.CreateAccessToken(ctx, "more")
	wantStatus(t, err, http.StatusUnauthorized)
	_, err = access.CreateMessage(ctx, "hi")
	wantStatus(t, err, http.StatusUnauthorized)

	// An agent token can't read messages or exchange for more tokens.
	_, err = a.ListMessages(ctx, 0, 10)
	wantStatus(t, err, http.StatusUnauthorized)
	_, err = a.CreateAgentToken(ctx, "a2")
	wantStatus(t, err, http.StatusUnauthorized)

	// A session can't report.
	_, err = user.CreateMessage(ctx, "hi")
	wantStatus(t, err, http.StatusUnauthorized)
}

func TestLogin(t *testing.T) {
	ctx := context.Background()
	url := newServer(t)
	signup(t, url, "ada@example.com")
	c := &client.Client{BaseURL: url}

	_, err := c.Login(ctx, "ada@example.com", "wrong password")
	wantStatus(t, err, http.StatusUnauthorized)
	_, err = c.Login(ctx, "nobody@example.com", "correct horse")
	wantStatus(t, err, http.StatusUnauthorized)

	sess, err := c.Login(ctx, "ADA@example.com", "correct horse")
	if err != nil {
		t.Fatalf("email should match case-insensitively: %v", err)
	}
	c.Token = sess.Token
	if err := c.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = c.ListMessages(ctx, 0, 10)
	wantStatus(t, err, http.StatusUnauthorized)
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	url := newServer(t)
	c := &client.Client{BaseURL: url}

	_, err := c.Signup(ctx, "not-an-email", "correct horse")
	wantStatus(t, err, http.StatusBadRequest)
	_, err = c.Signup(ctx, "ada@example.com", "short")
	wantStatus(t, err, http.StatusBadRequest)

	user := signup(t, url, "ada@example.com")
	_, err = c.Signup(ctx, "ada@example.com", "correct horse")
	wantStatus(t, err, http.StatusConflict)

	_, err = user.CreateAccessToken(ctx, "has space")
	wantStatus(t, err, http.StatusBadRequest)
	tok, err := user.CreateAccessToken(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	_, err = user.CreateAccessToken(ctx, "box")
	wantStatus(t, err, http.StatusConflict)

	_, err = (&client.Client{BaseURL: url, Token: tok.Token}).CreateAgentToken(ctx, "")
	wantStatus(t, err, http.StatusBadRequest)
	_, err = agent(t, url, tok.Token, "a1").CreateMessage(ctx, "   ")
	wantStatus(t, err, http.StatusBadRequest)
}
