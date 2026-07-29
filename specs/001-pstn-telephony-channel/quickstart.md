# Quickstart: PSTN Telephony Channel (Courier)

## Run tests

```bash
cd /home/matheuscardoso/Dev/vtex/courier
go test ./handlers/telephony/... -v
```

## Local receive example

```bash
curl -sS -X POST http://localhost:8080/c/tph/receive \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "message",
    "origin": "pstn",
    "did": "+15551234567",
    "caller_id": "+15559876543",
    "call_id": "test-call-1",
    "message": {
      "type": "text",
      "timestamp": "1721567890",
      "text": "Hello"
    }
  }'
```

## Channel configuration (Flows)

- Channel type: `TPH`
- Address: DID assigned to the tenant
- Config: `base_url` pointing to the voice gateway HTTP endpoint

## Coordination

- Deploy Courier with this handler before enabling PSTN channels in Flows
- Gateway must call `/c/tph/receive` and implement `/send` per [gateway-api.md](./contracts/gateway-api.md)
