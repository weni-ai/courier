# Research: ExternalV2 OAuth2 Access-Token Refresh

No `[NEEDS CLARIFICATION]` remains in the spec. The items below are design questions the plan had to settle from the code, plus the research note requested for `UpdateChannelConfig`.

## R1. `UpdateChannelConfig`, the local channel cache, and what the next `SendMsg` sees

- **Facts** (`backends/rapidpro/channel.go`, `backend.go`):
  - `UpdateChannelConfig` (line 738) marshals the whole map it receives and runs `UPDATE channels_channel SET config = $1 WHERE uuid = $2 AND is_active = true` (line 726). The statement replaces the column, it does not merge. The update autocommits before the function continues.
  - It then calls `clearLocalChannel(channel.UUID())` and, when the channel has an address, `clearLocalChannelByAddress`. Both delete entries from the in-process `channelCache` map only. Nothing is published to other instances.
  - `getChannel` serves a channel from `channelCache` while `expiration` is in the future; `localTTL` is 5 minutes (line 164). On a miss it loads the row from Postgres (`loadChannelFromDB`) and caches it.
  - `PopNextOutgoingMsg` (`backend.go:244`) resolves `dbMsg.channel` through `GetChannel` when the message is popped, so a `msg.Channel()` is a pointer to a cached `*DBChannel` whose `Config_.Map` is shared by every concurrent send on that channel.
- **Decision**:
  - A message popped on the **same instance** after the save reads the refreshed config: the cache entry is gone, so `GetChannel` reloads the row.
  - The `SendMsg` that performed the refresh does **not** see it through `msg.Channel()`: its `*DBChannel` still holds the old map. The spec's in-memory auth state, shared by all parts of that `SendMsg`, is therefore required and is the source of truth inside the call.
  - Messages already popped on the same instance (other sender goroutines) keep the old channel object, and so do all messages on **other instances** until their entry expires, up to 5 minutes. Such a send can find `expires_in` in the past or a token the partner already replaced and refresh again. Consequence: one extra token call and one extra config write, last write wins. If the partner rotates refresh tokens and the stale `refresh_token` was already invalidated, that refresh fails, the message ends errored, and the next attempt loads the refreshed config. The failure is bounded and self-healing, and it is the risk the spec documents in `doc.md`.
  - A narrow race exists inside one instance: a loader that read the row before the `UPDATE` and calls `cacheChannel` after `clearLocalChannel` can re-cache the old config for up to 5 minutes. Same consequence, same bound.
  - The shared `Config_.Map` must never be mutated by the handler (concurrent readers). The handler clones before writing; this matches the spec and the Teams pattern.
- **Alternatives considered**: reading the config again from the backend after the save (no such method on `courier.Backend`; adding one touches `backend.go`, Principle XVI); a cross-instance invalidation or lock (Redis: out of scope by decision).

## R2. Dotted-path lookup: `encoding/json` rather than `jsonparser`

- **Decision**: decode the token response with `encoding/json` (`Decoder.UseNumber`) into `interface{}` and walk `map[string]interface{}` segments split on `.`, as `mailroom/services/tickets/generic/token_refresh.go` does.
- **Rationale**: the spec says a dotted path walks JSON **objects** and accepts only strings, numbers and booleans. `jsonparser.Get` also resolves `[n]` array indices from the same key list, which would widen the contract, and it returns raw bytes that need a second pass to classify the value type. The standard library gives the exact semantics with no extra code. `jsonparser` is allowed by the request and already in `go.mod`; it is not needed.
- **Alternatives considered**: `jsonparser.Get(body, strings.Split(path, ".")...)` plus a guard that rejects `[`-prefixed segments (more code to restrict a library to what `encoding/json` does by default).

## R3. Port from Mailroom: where the code must differ

The port keeps Mailroom's structure and the same keys. Four behaviours of the Mailroom code do not match the spec and are changed on purpose, each with a unit test:

| Mailroom behaviour | Spec rule | Port |
|--------------------|-----------|------|
| `parseExpiresInSeconds` accepts a float and truncates it (`3600.5` → `3600`), and accepts a numeric string (`"3600"`) | `expires_in_field` accepts a JSON number that is a whole number of seconds, greater than zero | reject a non-whole float and a JSON string; a rejected value counts as absent so `expires_in_default` applies |
| `lookupJSONPath` turns an object or array into text with `fmt.Sprint` | only strings, numbers and booleans are accepted | an object, array or `null` is a miss |
| `matchesRefreshWhen` matches `body_contains` on any status | Q3 option A: body match only on a non-2xx response; `status_codes` unchanged | add the non-2xx condition |
| `headers["Content-Type"] == ""` is a case-sensitive map lookup | set the form content type "if not already set" | check the request's headers case-insensitively (`req.Header.Get`) so `content-type` from the operator is not duplicated |

Also adapted, not changed in meaning: time comes from `dates.Now()` (same as Mailroom, controllable in tests with `dates.SetNowSource`); the HTTP call goes through `utils.MakeHTTPRequest` instead of `httpx`; the token-refresh state is a value owned by one `SendMsg`, so Mailroom's mutex and `OnUpdate` callback are not ported.

## R4. Token response handling with `utils.MakeHTTPRequest`

- **Facts** (`utils/http.go`): `MakeHTTPRequest` always returns a non-nil `RequestResponse`. It also returns an error for any non-2xx status, with `StatusCode` and `Body` filled, and for a connection failure with `StatusCode == 0`. It takes no context and the shared client times out at 60s.
- **Decision**: classify from the `RequestResponse`, not from the error: a transport failure (`StatusCode == 0`) fails with reason `connection`; `StatusCode >= 400` fails with `status_4xx` or `status_5xx` and the body is never read; a status below 400 that is not 2xx (a final 3xx the client did not follow) is parsed like a success, which is what the spec's "status 400 or higher fails" says. A 2xx body that is not JSON fails with `invalid_json`; a missing, empty or non-scalar `token_field` fails with `missing_token_field`.
- **Rationale**: the spec states the failure rule by status code, and `err != nil` would also reject the non-2xx statuses below 400.
- **Alternatives considered**: treat every `err != nil` as failure (stricter than the spec, simpler); pass the `SendMsg` context to the token request (rejected in the spec: the partner call does not use it and the harness runs under 10ms).

## R5. Shape of the `Token Refresh` channel log

- **Facts**: `courier.NewChannelLog(description, channel, msgID, method, url, statusCode, request, response, elapsed, err)` builds a log with whatever strings are passed; `NewChannelLogFromRR` copies the dumped request and response, so it must not be used for the token call.
- **Decision**: build the token log with `courier.NewChannelLog`, passing empty `request` and `response`, the method, the token URL stripped of query string and user info, the status code (0 on a transport failure), the elapsed time from `rr.Elapsed`, and a fixed reason string as the error (or `nil` on success). Return it for every attempt and add it to the part's `MsgStatus` so it is stored with the message's other logs.
- **Rationale**: `NewChannelLog` is the only constructor that does not copy a dump. The reason strings are the same classification used in the log fields.

## R6. `Authorization` redaction and the "byte-for-byte" disabled path

- **Decision**: the provider request on the disabled path is unchanged. The stored partner send log is not: the spec's Q2 decision redacts the `Authorization` value for every E2 send log, opted in or not. A plan instruction asked for the disabled path to stay byte-for-byte equivalent to today's behaviour; the two are reconciled as "same request on the wire, redacted log". The implementation passes `NewChannelLogFromRR` a copy of the `RequestResponse` whose `Request` has been through `redactAuthorizationHeader`, a function that replaces the value of every `Authorization:` line with `[REDACTED]`, matching the header name case-insensitively and stopping at the end of the line (`(?im)^(authorization:[ \t]*)[^\r\n]*`), so line endings and the rest of the dump are untouched.
- **Rationale**: a regular expression over the dump keeps the redaction in the handler without changing `courier/utils` or `channel_log.go`. A value-based replace of the known token would miss the `Authorization` header set from `send_authorization` on channels that did not opt in.
- **Alternatives considered**: redact only on opted-in channels (leaves the plain-text `send_authorization` the spec decided to remove); replace the known token value (misses non-opted-in channels and non-`Bearer` values).

## R7. Replay structure: rebuild the request, keep the disabled path's lines

- **Facts**: `sendMsgPart` renders the template once, builds an `http.Request` whose body is a `strings.Reader`, sets headers and calls `MakeHTTPRequest`. A used `http.Request` body cannot be sent twice.
- **Decision**: keep the rendering code as is and turn the request construction into a local closure `newRequest(authorization string) (*http.Request, error)` that reuses the already rendered body string, applies the same `Content-Type` and sets `Authorization` only when the value is non-empty (disabled path: from `send_authorization`, as today) or always as `Bearer <raw>` (enabled path). The disabled path calls it once with the same value and the same order of header writes as today. The enabled path calls it again for the replay.
- **Rationale**: this is the smallest change that gives the replay a fresh request and a body snapshot, and it leaves the template, URL and content-type logic untouched.
- **Alternatives considered**: clone the request with `req.Clone` plus `GetBody` (the body is built from a `strings.Reader`, so `GetBody` is already set, but `Clone` shares the header map semantics; rebuilding is clearer).

## R8. Test seams

- `RunChannelSendTestCases` shares one channel across its cases and `MockBackend.UpdateChannelConfig` replaces `MockChannel.config`, so a refresh in one case would leak into the next. Tests that refresh build their own channel per run, and the persistence test runs a one-case slice and inspects `channel.Config()` afterwards.
- The harness cannot assert that a request did not happen. Absence is shown through exact `Responses` counts (`mockRRCount == len(Responses)`) and, for the disabled path, through a token URL on a closed port: if the handler called it the send would end `E`.
- The harness's `Responses` map cannot return two answers to one request shape, so the 401-then-200 sequences use a stateful `httptest` server and call `SendMsg` directly.
- The save failure is exercised through `handlers.NewTestServer(backend)` with a thin type embedding `*courier.MockBackend` and overriding `UpdateChannelConfig`; `test.go` is not modified.
- Application-log assertions use `github.com/sirupsen/logrus/hooks/test`, which ships inside the `logrus` module already in `go.mod`.
- Time: `dates.SetNowSource(dates.NewFixedNowSource(...))` for expiry arithmetic, as `handlers/telesom` does; reset in a `defer`.
