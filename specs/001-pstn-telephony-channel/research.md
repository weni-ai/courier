# Research: PSTN Telephony Channel (Courier)

## Decision 1: Channel type code

- **Decision**: Use `TPH` (Telephony) as the 3-letter Courier channel type code
- **Rationale**: Consistent with existing codes (`WWC`, `TQ`, `TW`); distinct from TWIML/voice IVR types
- **Alternatives considered**: `PST` (less descriptive), `VCE` (ambiguous with other voice products)

## Decision 2: Channel resolution

- **Decision**: Address-based routing with custom `GetChannel` using `GetChannelByAddress(ctx, TPH, did)`
- **Rationale**: Product spec BD-010 — DID is channel config/address; matches Facebook/WAC pattern
- **Alternatives considered**: UUID in URL (rejected — gateway learns DID from SIP, not Courier UUID)

## Decision 3: Gateway wire contract

- **Decision**: JSON payloads symmetric with `weni-webchat` style — `type`, `origin`, `did`, `caller_id`, `call_id`, nested `message`
- **Rationale**: Existing gateway patterns in the org; minimal new contract surface
- **Alternatives considered**: Form-encoded (rejected — gateway already uses JSON)

## Decision 4: Withheld caller ID

- **Decision**: Synthetic URN `tel:withheld-<call_id>` when `caller_id` is empty
- **Rationale**: Stable per-call identity without inventing a phone number; satisfies product edge case
- **Alternatives considered**: `external:` scheme (rejected — BD-010 requires `tel:` for PSTN)
