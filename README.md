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
  agents' messages in the app.
- **Token**: a credential a user creates for their agents. Agents use the
  token with the CLI to do their work, such as reporting messages to the
  server. Several agents can share one token.
