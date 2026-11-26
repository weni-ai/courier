# Quickstart: ExternalV2 OAuth2 Access-Token Refresh

How to prove the feature works. Contracts: [contracts/e2-token-refresh.md](./contracts/e2-token-refresh.md). Entities: [data-model.md](./data-model.md). Test names are the ones in the spec's Test Strategy.

## Prerequisites

- Go 1.24 toolchain. Tests use `httptest` servers and `courier.MockBackend`; no database, Redis or network access is needed.
- Fixtures use fake values only (`test-access-token-1`, `fake-refresh-token`, `fake-client-secret`).

## Commands

```bash
cd /home/rafaelsoares/weni/weni-ai/external-oauth/courier
go test -p=1 ./handlers/externalv2/... -count=1
go vet ./handlers/externalv2/...
gofmt -l handlers/externalv2          # prints nothing when clean
```

Focused runs:

```bash
go test ./handlers/externalv2/ -count=1 -run 'TestParseTokenRefresh|TestTokenRefreshWhen|TestRequestAccessToken'
go test ./handlers/externalv2/ -count=1 -run 'TestSendingTokenRefresh|TestSendingWithAuth'
```

## Expected outcomes by criterion

| Criterion | Test | Expected outcome |
|-----------|------|------------------|
| AC-01 | `TestSendingWithAuth`, `TestSendingTokenRefreshDisabled` | status `W`; `Authorization` equals the raw `send_authorization`; the token URL (a closed port) is never needed |
| AC-02 | `TestSendingTokenRefreshExpired` (`custom`, `refresh`) | one token call, one partner call, partner `Authorization: Bearer <new>` |
| AC-03 | `TestSendingTokenRefreshReplay` | 401 → token → 200 gives `W` with one token call; two 401 give `E` with still one token call; a 200 with the `body_contains` needle is not replayed; a non-2xx with the needle is replayed once |
| AC-04 | `TestSendingTokenRefreshPersists` | `channel.Config()` holds `Bearer <new>`, `refresh_token`, `expires_in`; other keys unchanged; the map captured before the run is unchanged |
| AC-05 | `TestSendingTokenRefreshInParts` | one token call for the whole message; every part carries the new Bearer |
| AC-06 | token 4xx/5xx, missing field, invalid JSON, invalid config, refresh failure after a match, partner 5xx without a match (`TestSendingTokenRefreshPartnerServerError`) | no partner call with a stale token; status `E` (or the returned error for an invalid config) |
| AC-07 | `TestSendingTokenRefreshLogs` | `Token Refresh` log has empty request/response and no secrets; partner send logs show `Authorization: [REDACTED]` for opted-in and static channels; the log hook sees no secret |
| AC-08 | existing `TestHandler`, `TestHandler2`, `TestTemplateHandler`, `TestHandleWithUrnAuth*` | unchanged and green |

## Checks to read the diff against

- `git diff --stat main...HEAD` lists, as source and docs, only `handlers/externalv2/*` and `WENI-CHANGELOG.md`; the rest is `specs/009-externalv2-oauth2-refresh/**` and `.specify/feature.json`.
- `git diff -- handlers/externalv2/externalv2.go` does not touch `receiveMessage`, `receiveStatus`, `receiveStopContact`.
- `rg -n 'client_secret|refresh_token|access_token' handlers/externalv2/*.go` finds no value that is written to a logger or a channel log.

## Manual check on the pilot channel (staging)

1. Set `token_refresh_enabled`, `token_refresh_type`, `token_refresh_config` and, for `refresh`, `refresh_token` on one channel; set `expires_in` to a past timestamp.
2. Send a message. Expect one `Token Refresh` log (no bodies), one partner log with `Authorization: [REDACTED]`, status wired, and `send_authorization`, `refresh_token` (if rotated) and `expires_in` updated in the channel config.
3. Remove `token_refresh_enabled`: the next send uses the stored `Bearer <token>` unchanged.
4. Record the token calls and config writes per channel and compare them with the declared peak's steady state (one refresh per expiry window; spec, Peak Load) before enabling more channels.
