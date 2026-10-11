# hq

## Purpose

hq is a native frontend for agents working on a server. "Agents" here means
LLMs running in a loop. They do the work on the server, and hq is the app
you use to see and interact with them.

## First version

The first version lets agents send messages to hq, and users read those
messages in the app. Agents send messages with the hq CLI, which reports
them to the hq server.

Messages go one way only, from agents to users. Users can't reply to agents
in the first version.

## Parts

- **App**: the native frontend. iOS is the first target.
- **Server**: the hq server. It receives messages from agents and serves
  them to the app. It is multi-tenant, so one server hosts many tenants.
- **CLI**: the tool agents run to report messages to the hq server.

## Concepts

- **User**: a person who uses hq. The server has users, and users read
  agents' messages in the app. Each user is tied to an email address.
- **Token**: an access token a user creates for their agents. Agents use the
  token with the CLI to do their work, such as reporting messages to the
  server. Several agents can share one access token.
- **Agent**: an LLM running in a loop that reports to hq. Agents identify
  themselves; hq doesn't assign their identity.
- **Temporary token**: a short-lived token tied to one agent's identity. An
  agent sends its access token plus its identity to the server and gets back
  a temporary token, which it then uses for its work. This is how the server
  tells apart agents that share an access token.

## Setting up agents

Users manage access tokens with the same CLI that agents use. A user logs in
once on a machine, creates access tokens there, and hands them to agents.
Agents never see the user's credentials.

```sh
# User, once per machine
hq login                          # email and password; stores a user session
hq tokens create --name build-box # prints the access token once
hq tokens list
hq tokens revoke build-box

# Agent setup: give it the access token
export HQ_TOKEN=...

# Agent at work
hq report --as reviewer-1 "PR #12 is ready"
```

When an agent runs `report`, the CLI exchanges the access token plus the
agent's identity for a temporary token. It caches that token and refreshes it
when it expires.

- **User sessions and access tokens are separate.** A user session can
  manage tokens. An access token can only do agent work, such as reporting
  messages, so a leaked one can't create tokens or read the user's messages.
- **Access tokens are shown once.** The server stores only a hash in SQLite.
  Tokens have names so they can be listed and revoked.
- **There's no agent registry.** Setting up an agent means giving it an
  access token. An agent first appears when it exchanges a token.

## Stack

- **iOS app**: Swift.
- **Server**: Go, with SQLite for storage.
- **CLI**: Go.

## Development

Requires Go 1.26 or later.

```sh
go test ./...

# Run the server (stores data in ./hq.db)
go run ./cmd/hqd -addr :8080 -db hq.db

# Use the CLI against it
go run ./cmd/hq signup
```

The repository has two commands:

- `cmd/hqd`: the server.
- `cmd/hq`: the CLI, for both users and agents.

Defaults that may change: user sessions last 30 days, temporary agent tokens
last 1 hour, and the iOS app fetches new messages by polling
`GET /v1/messages?after=<last id>`.

## License

MIT. See [LICENSE](LICENSE).
