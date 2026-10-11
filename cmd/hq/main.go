// Command hq is the hq CLI. Users run it to log in and manage access tokens;
// agents run it to report messages.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/gronnbeck/hq/internal/client"
)

const defaultServer = "https://hq.np.lol"

const usage = `hq is the CLI for hq.

User commands:
  hq signup [--email EMAIL]       create an account and log in
  hq login [--email EMAIL]        log in on this machine
  hq logout                       log out on this machine
  hq tokens create NAME           create an access token for agents
  hq tokens list                  list access tokens
  hq tokens revoke NAME           revoke an access token
  hq messages [--follow]          show messages from agents
  hq config                       show the config file and current settings

Admin commands (need HQ_ADMIN_TOKEN):
  hq users create --email EMAIL   create a user; reads the password like login

Agent commands:
  hq report [--as AGENT] MESSAGE  send a message to the token's user
                                  (MESSAGE "-" reads it from stdin)

Global flags:
  --server URL                    hq server (or HQ_SERVER; default ` + defaultServer + `)

Agents pass their access token in HQ_TOKEN.

Defaults live in ~/.config/hq/config.yaml, which hq creates on first run.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "hq:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	global := flag.NewFlagSet("hq", flag.ContinueOnError)
	global.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	serverFlag := global.String("server", "", "")
	if err := global.Parse(args); err != nil {
		return err
	}
	args = global.Args()
	if len(args) == 0 {
		global.Usage()
		return errors.New("no command given")
	}

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	sess, err := loadSession()
	if err != nil {
		return fmt.Errorf("read session: %w", err)
	}
	server := strings.TrimRight(firstNonEmpty(*serverFlag, os.Getenv("HQ_SERVER"), cfg.Server, defaultServer), "/")
	c := &client.Client{BaseURL: server, HTTP: &http.Client{Timeout: 30 * time.Second}}

	cmd, args := args[0], args[1:]
	switch cmd {
	case "signup":
		return authenticate(ctx, c, args, true)
	case "login":
		return authenticate(ctx, c, args, false)
	case "logout":
		return logout(ctx, c, sess)
	case "tokens":
		return tokens(ctx, c, sess, args)
	case "messages":
		return messages(ctx, c, sess, args)
	case "users":
		return users(ctx, c, args)
	case "report":
		return report(ctx, c, cfg, args)
	case "config":
		return showConfig(c, cfg, sess)
	case "help", "-h", "--help":
		global.Usage()
		return nil
	}
	return fmt.Errorf("unknown command %q; run hq help", cmd)
}

func authenticate(ctx context.Context, c *client.Client, args []string, signup bool) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	email := fs.String("email", "", "account email")
	if err := fs.Parse(args); err != nil {
		return err
	}
	in := bufio.NewReader(os.Stdin)
	if *email == "" {
		var err error
		if *email, err = prompt(in, "Email: "); err != nil {
			return err
		}
	}
	password, err := readPassword(in, "Password: ")
	if err != nil {
		return err
	}
	if signup && term.IsTerminal(int(os.Stdin.Fd())) {
		again, err := readPassword(in, "Password again: ")
		if err != nil {
			return err
		}
		if again != password {
			return errors.New("passwords don't match")
		}
	}

	auth := c.Login
	if signup {
		auth = c.Signup
	}
	sess, err := auth(ctx, *email, password)
	if err != nil {
		return err
	}
	if err := saveSession(session{Server: c.BaseURL, Token: sess.Token}); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	fmt.Printf("Logged in to %s as %s.\n", c.BaseURL, *email)
	return nil
}

func logout(ctx context.Context, c *client.Client, sess session) error {
	if sess.Token == "" {
		fmt.Println("Not logged in.")
		return nil
	}
	// Log out of the server the session belongs to, whatever the current
	// default is.
	c.BaseURL, c.Token = sess.Server, sess.Token
	var apiErr *client.Error
	if err := c.Logout(ctx); err != nil && !(errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized) {
		return err
	}
	if err := deleteSession(); err != nil {
		return err
	}
	fmt.Println("Logged out.")
	return nil
}

func tokens(ctx context.Context, c *client.Client, sess session, args []string) error {
	if err := useSession(c, sess); err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("usage: hq tokens create|list|revoke")
	}
	switch sub, args := args[0], args[1:]; sub {
	case "create":
		if len(args) != 1 {
			return errors.New("usage: hq tokens create NAME")
		}
		t, err := c.CreateAccessToken(ctx, args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Created access token %q. It won't be shown again; give it to your agents as HQ_TOKEN.\n", t.Name)
		fmt.Println(t.Token)
		return nil
	case "list":
		ts, err := c.ListAccessTokens(ctx)
		if err != nil {
			return err
		}
		if len(ts) == 0 {
			fmt.Println("No access tokens.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tCREATED")
		for _, t := range ts {
			fmt.Fprintf(w, "%s\t%s\n", t.Name, t.CreatedAt.Local().Format(time.DateTime))
		}
		return w.Flush()
	case "revoke":
		if len(args) != 1 {
			return errors.New("usage: hq tokens revoke NAME")
		}
		if err := c.RevokeAccessToken(ctx, args[0]); err != nil {
			return err
		}
		fmt.Printf("Revoked access token %q.\n", args[0])
		return nil
	default:
		return fmt.Errorf("unknown tokens command %q", sub)
	}
}

func users(ctx context.Context, c *client.Client, args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return errors.New("usage: hq users create --email EMAIL")
	}
	fs := flag.NewFlagSet("users create", flag.ContinueOnError)
	email := fs.String("email", "", "the new user's email")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("--email is required")
	}
	c.Token = os.Getenv("HQ_ADMIN_TOKEN")
	if c.Token == "" {
		return errors.New("HQ_ADMIN_TOKEN isn't set")
	}
	password, err := readPassword(bufio.NewReader(os.Stdin), "Password for "+*email+": ")
	if err != nil {
		return err
	}
	u, err := c.CreateUser(ctx, *email, password)
	if err != nil {
		return err
	}
	fmt.Printf("Created user %s on %s.\n", u.Email, c.BaseURL)
	return nil
}

func messages(ctx context.Context, c *client.Client, sess session, args []string) error {
	fs := flag.NewFlagSet("messages", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "keep waiting for new messages")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := useSession(c, sess); err != nil {
		return err
	}
	const pageSize = 500
	var after int64
	for {
		msgs, err := c.ListMessages(ctx, after, pageSize)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		for _, m := range msgs {
			fmt.Printf("%s  %s: %s\n", m.CreatedAt.Local().Format(time.DateTime), m.Agent, m.Body)
			after = m.ID
		}
		if len(msgs) == pageSize {
			continue
		}
		if !*follow {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

func report(ctx context.Context, c *client.Client, cfg config, args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	agentFlag := fs.String("as", "", "the agent's identity")
	if err := fs.Parse(args); err != nil {
		return err
	}
	agent := firstNonEmpty(*agentFlag, os.Getenv("HQ_AGENT"), cfg.Agent)
	if agent == "" {
		return errors.New("agents identify themselves: pass --as, set HQ_AGENT, or set agent in the config")
	}
	accessToken := os.Getenv("HQ_TOKEN")
	if accessToken == "" {
		return errors.New("HQ_TOKEN isn't set; ask your user for an access token")
	}
	body := strings.Join(fs.Args(), " ")
	if body == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		body = strings.TrimRight(string(b), "\r\n")
	}
	if strings.TrimSpace(body) == "" {
		return errors.New("usage: hq report --as AGENT MESSAGE")
	}

	tok, err := agentToken(ctx, c, accessToken, agent, false)
	if err != nil {
		return err
	}
	c.Token = tok
	_, err = c.CreateMessage(ctx, body)
	var apiErr *client.Error
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
		// The cached token was revoked or expired early; get a new one once.
		if tok, err = agentToken(ctx, c, accessToken, agent, true); err != nil {
			return err
		}
		c.Token = tok
		_, err = c.CreateMessage(ctx, body)
	}
	return err
}

// agentToken returns a temporary token for the agent, from the cache when one
// is still good for at least a minute, otherwise by exchanging the access token.
func agentToken(ctx context.Context, c *client.Client, accessToken, agent string, refresh bool) (string, error) {
	path, err := agentTokenPath(c.BaseURL, accessToken, agent)
	if err != nil {
		return "", err
	}
	var cached cachedAgentToken
	if !refresh && readJSON(path, &cached) == nil && time.Until(cached.ExpiresAt) > time.Minute {
		return cached.Token, nil
	}
	exchange := *c
	exchange.Token = accessToken
	t, err := exchange.CreateAgentToken(ctx, agent)
	if err != nil {
		return "", err
	}
	// A failed cache write only costs an extra exchange next time.
	writeJSON(path, cachedAgentToken{Token: t.Token, ExpiresAt: t.ExpiresAt})
	return t.Token, nil
}

// useSession authenticates c as the logged-in user. It refuses when the
// session belongs to another server, so the token never leaves for one.
func useSession(c *client.Client, sess session) error {
	if sess.Token == "" {
		return fmt.Errorf("not logged in to %s; run hq login", c.BaseURL)
	}
	if sess.Server != c.BaseURL {
		return fmt.Errorf("logged in to %s, not %s; run hq login to switch", sess.Server, c.BaseURL)
	}
	c.Token = sess.Token
	return nil
}

func showConfig(c *client.Client, cfg config, sess session) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	fmt.Printf("Config file: %s\n", p)
	fmt.Printf("Server:      %s\n", c.BaseURL)
	fmt.Printf("Agent:       %s\n", firstNonEmpty(os.Getenv("HQ_AGENT"), cfg.Agent, "(not set)"))
	if sess.Token == "" {
		fmt.Println("Logged in:   no")
	} else {
		fmt.Printf("Logged in:   to %s\n", sess.Server)
	}
	return nil
}

func prompt(in *bufio.Reader, label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// readPassword reads without echo from a terminal, or a plain line otherwise
// so scripts can pipe the password in.
func readPassword(in *bufio.Reader, label string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, label)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
