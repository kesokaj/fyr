---
name: json-api-style
description: "Google AIP JSON API contracts: lowerCamelCase keys, RFC 3339 timestamps, string enums, pageToken, error shape. Not parser code."
---

# JSON API Style

Conventions for the JSON a Go service here accepts and returns, taken from Google's API Improvement Proposals (<https://google.aip.dev>) and the proto3 JSON mapping (<https://protobuf.dev/programming-guides/json/>). The AIPs are the current home of Google's API design; the 2012 JSON Style Guide (`apiVersion`/`data` envelope, `error.errors[].domain`) is gone from google.github.io and no API published since uses it.

## When to use

- Designing a response or request payload, or a resource's field set
- Naming JSON properties and choosing their types
- Structuring errors, pagination, timestamps, enums
- Reviewing an API contract or spec against these rules

## When NOT to use

- Serialization code: struct tags and decoders are the `golang` skill (its tags follow this skill: lowerCamelCase)
- JSON Schema or OpenAPI authoring
- YAML, TOML or other config formats; frontend state shapes

## Core rules

1. **Keys are lowerCamelCase** (`pageSize`, `createTime`, `displayName`): the proto3 JSON mapping of the proto field names (AIP-140; proto spells them in lower snake form, JSON in lowerCamelCase). No prepositions in names (`reason`, not `reasonForChange`); booleans drop `is` (`enabled`, not `isEnabled`); a unit is a suffix (`distanceKm`, `timeoutSeconds`). A parser accepts the proto spelling too, but the service emits camelCase only.
2. **Arrays are plural, scalars singular**: `"items": […]`, `"name": "…"`.
3. **No envelope.** The resource IS the response body; a list response is `{"items": […], "nextPageToken": "…"}`; an error response is `{"error": {…}}` (rule 7). Never `apiVersion`, `data`, `context`, `method` wrappers.
4. **Flat by default**; nest only a grouping that is a thing of its own (an `address`, a `money` value).
5. **Timestamps are RFC 3339 UTC** with `Z` and 0, 3, 6 or 9 fractional digits, in fields named `*Time` (`createTime`, `updateTime`, `expireTime`; AIP-142); a calendar date is `{"year", "month", "day"}` or a `YYYY-MM-DD` string in a `*Date` field; a span is a duration string `"3.5s"` (`google.protobuf.Duration` form: seconds with up to nine fractional digits and the suffix `s`).
6. **Enums are strings** in `UPPER_SNAKE_CASE`, the first value `*_UNSPECIFIED` (AIP-126): `"state": "ACTIVE"`. A set that changes often is a plain `string` field documented as such.
7. **Errors follow AIP-193**: `{"error": {"code": 404, "status": "NOT_FOUND", "message": "…", "details": […]}}`, where `code` is the HTTP status, `status` the `google.rpc.Code` name, `message` a developer-facing sentence, and `details` carries typed objects: an `ErrorInfo` (`reason` in `UPPER_SNAKE_CASE`, `domain` = the service's name, `metadata` map) in every error response, `BadRequest.fieldViolations[]` for validation, `LocalizedMessage` for text shown to a user. Each detail type appears at most once.
8. **Pagination follows AIP-158**: request `pageSize` (0 or absent means the server default, above the maximum is coerced, negative is `INVALID_ARGUMENT`) and `pageToken` (opaque); response `items` first, then `nextPageToken`, EMPTY or absent at the end of the collection (the only end signal), and optionally `totalSize`, which may be an estimate.
9. **Field behaviour is stated** (AIP-203): every request field is one of required, optional, output-only (ignored on input: `name`, `createTime`), input-only, immutable or identifier, in the spec and in the handler's validation.
10. **Omit, do not null.** An unset optional field is absent; `null` is written only where the API documents it as a distinct state. 64-bit integers are strings on the wire; `int32` and `double` are numbers; bytes are standard base64.

## Go struct tags

```go
type Order struct {
	Name       string    `json:"name"`                 // "orders/123", output-only
	DisplayName string   `json:"displayName"`
	State      string    `json:"state"`                // "STATE_UNSPECIFIED" | "PENDING" | "PAID"
	TotalCents int64     `json:"totalCents,string"`    // int64 travels as a string
	CreateTime time.Time `json:"createTime,omitzero"`  // RFC 3339 by encoding/json; zero omitted
}

type ListOrdersResponse struct {
	Items         []Order `json:"items"`
	NextPageToken string  `json:"nextPageToken,omitempty"`
	TotalSize     int32   `json:"totalSize,omitempty"`
}
```

## Reference

`references/json-api-conventions.md`: the error shape with its detail types in full, pagination request and response end to end, naming rules and the reserved names (`name`, `etag`, `createTime`, `updateTime`, `displayName`, `labels`), value types and the 64-bit rule, worked examples.

## Further reading

- AIP index — <https://google.aip.dev> (140 names, 142 time, 126 enums, 158 pagination, 193 errors, 203 field behaviour)
- proto3 JSON mapping — <https://protobuf.dev/programming-guides/json/>
