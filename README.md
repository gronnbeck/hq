# hq

## Purpose

hq is a native frontend for agents working on a server. "Agents" here means
LLMs running in a loop. They do the work on the server, and hq is the app
you use to see and interact with them.

## First version

The first version lets agents send messages to hq, and users read those
messages in the app.

## Parts

- **App**: the native frontend. iOS is the first target.
- **Server**: where the agents run.
