# Research: Fail WAC Not-Owner Sends

## 1. Where to parse 131070 without changing WCD

- **Decision**: `requestWAC` always unmarshals `rr.Body` when present, including when `MakeHTTPRequest` returns a non-2xx `err`. It still does **not** talk to Redis or set `MsgFailed`. After each `requestWAC` return, `sendCloudAPIWhatsappMsg` calls a handler helper `failWACSendOnGraph131070` only if `msg.Channel().ChannelType() == "WAC"` and `EnableWACSendOwnershipBlock`. That helper inspects `resp.Error.Code`, applies Lua `not_owner`, sets `F`+`H`, logs `reason=131070`. `WCD` uses the same `requestWAC` and never calls the helper.
- **Rationale**: `requestWAC` is a package function with no `*handler` (facebookapp.go ~3800). Putting Redis there would either add a receiver used by WCD or thread config through every call site. `SendMsg` already branches WAC vs WCD (lines 2109–2112) but both call `sendCloudAPIWhatsappMsg`; the type check must stay at the 131070 apply site.
- **Alternatives considered**: make `requestWAC` a method and gate inside it (easy to miss WCD); duplicate HTTP in a WAC-only function (Principle XVI).

## 2. What counts as an allowed template

- **Decision**: Exempt only when `getTemplate` returns a non-nil templating whose `Template.Category` uppercased is `MARKETING`, `UTILITY`, or `AUTHENTICATION`, or when `msg.DirectSend()` is true. Empty or unknown category is a **service** message and is blocked when `not_owner`.
- **Rationale**: Matches spec AC-01/AC-02 and Meta’s three Cloud API template categories. DirectSend already forces utility payload fields (`getWACDirectSend`).
- **Alternatives considered**: any non-nil templating (would allow broken/empty category through); URL path `/marketing_messages` only (would still block UTILITY/AUTH on `/messages`).

## 3. `getTemplate` before the ownership gate

- **Decision**: Call `getTemplate` first. Decode error remains a returned error (today’s behaviour after the current block). Use the result both for the allow-list and for `isMarketingTemplate`.
- **Rationale**: Spec Flow step 1. Checking ownership first would keep blocking marketing templates (today’s bug).
- **Alternatives considered**: parse category with a second unmarshal (duplication).

## 4. `failed_reason` letter

- **Decision**: Courier constant `FailedReasonOtherOwner = "H"` on `courier` (or next to status). Empty string means the SQL `CASE` leaves the column unchanged.
- **Rationale**: Spec Implementation Decisions. Stock RapidPro uses S/C/L/E/O/D. Flows/RapidPro must register `H` before flag-on writes.
- **Alternatives considered**: metadata JSON only (Flows would not show the history label); reuse `E` (collides with error-limit).

## 5. Lua timestamp for Graph 131070

- **Decision**: `applyWACThreadOwner` mode `set`, state `not_owner`, `ts=time.Now().Unix()`, `source=graph_131070`.
- **Rationale**: Graph is a live refusal; `set` creates a missing key so later service sends fail closed. `now` should beat an older `owner` `ts`. Existing CAS still lets a later webhook with a greater `ts` restore `owner`.
- **Alternatives considered**: `update_if_exists` (would not mark unmarked numbers that Meta already refused); skip Redis (local block would not learn from Graph).

## 6. HTTP error body

- **Decision**: `MakeHTTPRequest` on non-2xx still fills `rr.Body` with the JSON (`utils/http.go` `newRRFromResponse`). Today `requestWAC` returns `&wacMTResponse{}` on that `err` and drops the body. Change: if `rr != nil && len(rr.Body) > 0`, `processResponse` even when `err != nil`. Connection failures (`rr.Body` is the Go error string) that fail JSON unmarshal keep `MsgErrored`.
- **Rationale**: Meta returns 4xx with `{"error":{"code":131070,...}}`.
- **Alternatives considered**: parse only on 2xx (would miss the real 131070 path).

## 7. Status SQL

- **Decision**: Add `failed_reason` to `updateMsgID`, `updateMsgExternalID`, and `bulkUpdateMsgStatusSQL` with `CASE WHEN :failed_reason != '' THEN :failed_reason ELSE failed_reason END`. `DBMsgStatus` gets `FailedReason_ string \`db:"failed_reason"\``. Test `schema.sql` adds nullable `varchar(1)`. No production migration.
- **Rationale**: Spec AC-05. Three writers already share the same status CASE pattern.
- **Alternatives considered**: only `updateMsgID` (external-id and bulk paths would drop the reason).
