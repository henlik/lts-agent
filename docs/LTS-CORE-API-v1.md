# LTS Core Agent API v1

This document defines the client-side contract introduced by LTS Agent v0.6 and
extended by v0.8 with observation-only desired state.
It does not implement the LTS Core server.

All endpoints are relative to the configured HTTPS Core base URL. Requests use
`Content-Type: application/json`, `Accept: application/json`, and bearer
authorization. Redirects are not followed.

## Registration

`POST v1/nodes/register`

The bearer credential is the one-time enrollment token. The request is:

```json
{
  "schema_version": 1,
  "node_fingerprint": "sha256-hex-value",
  "agent_version": "0.8.0",
  "inventory": {}
}
```

The fingerprint is SHA-256 of the UTF-8 string
`lts-agent-registration-v1:` followed by the lowercase machine ID. Core should
treat it as an idempotency identity for registration recovery.

A successful 2xx response must contain exactly one JSON object:

```json
{
  "schema_version": 1,
  "node_id": "node-123",
  "agent_token": "opaque-node-bearer-token"
}
```

## Heartbeat

`POST v1/nodes/{node_id}/heartbeat`

The bearer credential is the node-specific agent token. The request is:

```json
{
  "schema_version": 1,
  "sent_at": "2026-07-19T12:00:00.123456789Z",
  "agent_version": "0.8.0",
  "inventory": {}
}
```

`sent_at` is UTC RFC3339Nano. `inventory` is the current local report without
the final `core` synchronization summary or `desired_state`. Any 2xx response is
accepted and no response body is required.

## Desired state

`GET v1/nodes/{node_id}/desired-state`

The bearer credential is the node-specific agent token. The request has no
body and occurs after the heartbeat. A normal heartbeat HTTP or transport
failure does not prevent this request; caller cancellation skips it.

A successful 2xx response must contain exactly one strict schema-v1 JSON value:

```json
{
  "schema_version": 1,
  "revision": "rev-123",
  "roles": ["application-node"],
  "capabilities": ["docker", "postgresql"]
}
```

All fields are required and arrays must be non-null. Revisions match
`[A-Za-z0-9][A-Za-z0-9._:-]{0,127}`. Role and capability identifiers contain
lowercase letters, digits, and internal hyphens. Unknown fields, invalid
identifiers, malformed or trailing JSON, and unsupported schemas reject the
whole response. Complete valid arrays are deduplicated and sorted.

The result is reported as unapplied intent. It is not cached, written to the
local assignment file, or embedded in later registration/heartbeat snapshots.
A 404 or an empty successful response is a retrieval failure; Core returns a
schema-v1 document with empty arrays to represent no desired assignments.

## Failure behavior

The agent performs no retries. Non-2xx responses, TLS failures, timeouts, and
invalid response documents produce sanitized local warnings. Existing node
state is retained, local inventory is still emitted, and the process exits zero
unless configuration or stdout encoding itself is invalid.
