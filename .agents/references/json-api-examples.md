# JSON:API Examples (Non-Normative)

These examples illustrate the normative contract in `.agents/rules/json-api.md`. When they disagree with the rule,
the rule wins.

## Single Resource Response

`GET /api/v1/materials/mat_1f2e3d`:

```json
{
  "data": {
    "id": "mat_1f2e3d",
    "type": "materials",
    "attributes": {
      "name": "Algebra Basics",
      "state": "ready",
      "created_at": "2026-01-14T16:32:45.000000Z"
    },
    "relationships": {
      "course": {
        "data": { "type": "courses", "id": "crs_9a8b7c" }
      }
    }
  }
}
```

## Collection Response With Pagination Links

`GET /api/v1/invitations?page%5Blimit%5D=2&page%5Boffset%5D=2`:

```json
{
  "data": [
    {
      "id": "inv_a1b2c3",
      "type": "invitations",
      "attributes": {
        "email": "mentor@example.org",
        "role": "mentor",
        "status": "pending",
        "created_at": "2026-01-14T16:32:45.000000Z"
      }
    },
    {
      "id": "inv_d4e5f6",
      "type": "invitations",
      "attributes": {
        "email": "supervisor@example.org",
        "role": "supervisor",
        "status": "accepted",
        "created_at": "2026-01-15T08:15:30.000000Z"
      }
    }
  ],
  "links": {
    "prev": "/api/v1/invitations?page%5Blimit%5D=2&page%5Boffset%5D=0",
    "next": "/api/v1/invitations?page%5Blimit%5D=2&page%5Boffset%5D=4"
  },
  "meta": { "has_more": true }
}
```

An empty page returns `"data": []`, never `null`.

## Incorrect Resource Shape

```json
{
  "data": [
    {
      "id": "a1b2c3",
      "type": "files",
      "file_name": "report.pdf",
      "size_bytes": 2100000
    }
  ]
}
```

The attribute fields sit at the same level as `id` and `type`. They must be nested under an `attributes` object.

## Error Response

`GET /api/v1/materials/unknown` (authorized but absent or out of scope):

```json
{
  "errors": [
    {
      "status": "404",
      "code": "material_not_found",
      "title": "Not Found",
      "detail": "The request could not be completed."
    }
  ]
}
```

A response contains either `data` or `errors`, never both.

## Rate-Limit Error

HTTP 429 with a `Retry-After` header:

```json
{
  "errors": [
    {
      "status": "429",
      "code": "rate_limited",
      "title": "Too Many Requests",
      "detail": "The request could not be completed."
    }
  ]
}
```

## Instant Fields

```json
{
  "created_at": "2025-01-14T16:32:45.000000Z",
  "valid_until": "2025-12-31T21:59:59.000000Z",
  "finished_at": null
}
```

## Local-Time Schedule

A schedule that must survive daylight-saving changes uses separate local-time and IANA time-zone fields:

```json
{
  "scheduled_for_local_time": "16:00:00",
  "scheduled_for_time_zone": "Europe/Berlin"
}
```
