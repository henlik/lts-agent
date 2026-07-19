# LTS Core Agent API v1

This document defines the client-side contract introduced by LTS Agent v0.6 and
retained by v0.7.
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
  "agent_version": "0.7.0",
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
  "agent_version": "0.7.0",
  "inventory": {}
}
```

`sent_at` is UTC RFC3339Nano. `inventory` is the current local report without
the final `core` synchronization summary. Any 2xx response is accepted and no
response body is required.

## Failure behavior

The agent performs no retries. Non-2xx responses, TLS failures, timeouts, and
invalid response documents produce sanitized local warnings. Existing node
state is retained, local inventory is still emitted, and the process exits zero
unless configuration or stdout encoding itself is invalid.
