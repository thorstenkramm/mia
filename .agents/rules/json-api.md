# JSON:API Response Formatting in Go (Echo) – How-To Guide

This guide explains how to format HTTP requests and responses according to the JSON:API specification in a Go server
using the Echo framework. It’s written for junior developers and assumes you’re already familiar with basic REST concepts.
We’ll cover the key JSON:API response elements and show how to structure both successful data responses
(including lists of resources) and error responses. All examples are given in JSON/pseudocode (no Go code needed) so
you can apply the patterns immediately.

## JSON:API Requests

- <https://jsonapi.org/format/#fetching>
- <https://jsonapi.org/format/#crud>

## JSON:API Response Basics (Key Elements)

A JSON:API response is a JSON object with a defined structure. Important keys and concepts include:

- `data` – The primary resource data requested. For a successful request, the top-level JSON object must have a data
  member (unless it’s an error response). This can be a single resource object or an array of resource objects
  (use an array when returning a list/collection).
- `id` – A unique identifier for a resource. Every resource object in the data section must include an id
  (except when the object is sent by a client to create a new resource).
- `type` – A string that identifies the type of the resource (often a plural noun, e.g. "files").
  Every resource object must include a type along with its id.
- `attributes` – An object holding the resource’s data fields (apart from relationships). All the resource’s
  non-identifier fields (like name, size, dates, etc.) go under attributes.
  For example, a file resource’s attributes might include filename, size, and timestamps.
- `errors` – An array of error objects. This replaces the data section when a request results in an error.
  A response must contain either data or errors, never both. Each error object provides details about a specific
  problem (e.g. an error code, HTTP status, message).

Example of a response with a single object, e.g. `GET /files/some-folder/report.pdf`

```json
{
  "data": {
    "id": "report.pdf",
    "type": "files",
    "attributes": {
      "file_name": "report.pdf",
      "file_size": "2 MB",
      "last_modified": "2025-11-30T12:34:56Z",
      "size_bytes": 2100000
    },
    "links": {
      "self": "/files/some-folder/report.pdf"
    }
  }
}
```

> [!NOTE]
> JSON:API requires using the media type application/vnd.api+json for responses.
> In Echo, you can ensure this by setting the Content-Type header accordingly. Echo’s Context#JSON method will serialize
> a Go value to JSON and send it with a status code – you may override the content type if needed to comply with JSON:
> API.

## Representing Collections of Resources (List Example)

When your endpoint returns a list of resources (for example, `GET /files/some-folder/` returning all files in a folder),
format the response as an array of resource objects within data:

- Use an array for multiple resources: The top-level data should be an array of resource objects when returning a
  collection. Each element represents one file in the folder.
- Include id and type for each resource: Ensure every file object has an id (e.g. file ID or name) and a type
  (e.g. "files" for a file resource).
- Nest fields under attributes: Inside each resource object, provide file details under an attributes object.
  For a file, you might include attributes like fileName, fileSize (a human-readable size), lastModified (date),
  and sizeBytes (numeric size in bytes).

### Example (Correct JSON:API response)

A properly formatted JSON:API response for a list of files might look like this:

```json
{
  "data": [
    {
      "id": "a1b2c3",
      "type": "files",
      "attributes": {
        "file_name": "report.pdf",
        "file_size": "2 MB",
        "last_modified": "2025-11-30T12:34:56Z",
        "size_bytes": 2100000
      },
      "links": {
        "self": "/files/some-folder/report.pdf"
      }
    },
    {
      "id": "d4e5f6",
      "type": "files",
      "attributes": {
        "file_name": "photo.png",
        "file_size": "500 KB",
        "last_modified": "2025-12-01T08:15:30Z",
        "size_bytes": 512000
      },
      "links": {
        "self": "/files/some-folder/photo.png"
      }
    }
  ],
  "links": {
    "self": "/files/some-folder"
  }
}
```

In this correct example, the response is a JSON object with a top-level data array. Each file has its id and
type="files", and all file details are neatly under attributes. A client can easily iterate over data to get each file’s
information.

### Incorrect JSON Example (Non–JSON:API Format)

For comparison, here’s an incorrectly formatted response and why it violates JSON:API:

```json
{
  "data": [
    {
      "id": "a1b2c3",
      "type": "files",
      "file_name": "report.pdf",
      "file_size": "2 MB",
      "last_modified": "2025-11-30T12:34:56Z",
      "size_bytes": 2100000
    }
  ]
}
```

**What’s wrong with this?** The file fields (fileName, fileSize, etc.) are placed at the same level as id and type.
According to JSON:API, resource attributes must be inside an attributes object, not mixed into the top-level of the
resource. A correct format would nest those fields under an "attributes" key (as shown in the correct example above).
Always structure each resource as: { "id": ..., "type": ..., "attributes": { ... } }. Also note that we used a top-level
data key – omitting data entirely or using a custom key like "files" would not conform to JSON:API.

## Error Responses with JSON:API

When something goes wrong (e.g., a file is not found or a request is invalid), JSON:API specifies using an errors array
in the response instead of data. Key guidelines for error responses:

- Do not return a data key on errors: The response should have a top-level "errors" field containing an array of error
  objects. (No data field should appear when using errors.)
- Use appropriate HTTP status codes: Echo will still send an HTTP status like 404 or 400, and the error object should
  include that status as a string. (JSON:API error objects typically include a "status" field reflecting the HTTP
  status, among other details.)
- Provide helpful error details: Each error object can include members such as:
  - `status`: the HTTP status code (as a string).
  - `title`: a short, human-readable title for the error.
  - `detail`: a human-readable description of the specific problem.
  - `code`: an application-specific error code (string identifier for the error type).
  - (Optional) `meta`: any extra data about the error (not for routine use).

Typically, you should include at least a status and a detail or title so the client knows what happened.

Example Error Response: Imagine a request for a file that doesn’t exist. You might return a 404 status and a
JSON body like:

```json
{
  "errors": [
    {
      "status": "404",
      "title": "Not Found",
      "detail": "No file exists at the given path."
    }
  ]
}
```

This response has no data – instead, the top-level "errors" array contains one error object. The error object’s fields
describe the problem: a 404 status and a message explaining that the file was not found. (You could also include an
error "code" or a link to documentation in a real API.) According to JSON:API, error objects are returned as an array
under the errors key, and you can include multiple error objects if there are multiple issues to report (for example,
validation errors for several fields).

## Optional JSON:API Features: meta, links, included

JSON:API supports additional top-level members to enrich responses. These are optional but recommended:

- `meta` – A meta object for any extra information that doesn’t fit into the standard data or errors. For example, you
  could include a total record count or a response timestamp in meta. The value of meta is a JSON object (free-form
  content).
- `links` – A links object for URLs related to the response or resources. This shall include a "self" link (URL of the
  current request) and pagination links (e.g. "next", "prev") for collections. Each resource shall also have its own
  links (like a self-link to that resource) if needed.
- `included` – An array of resource objects that are related to the primary data, included side-by-side. This is used for
  compound documents, where you include related resources to avoid extra fetches. For example, if files had related
  “owner” user resources, and you wanted to include those user details in the same response, you’d use an included array
  containing those user resource objects. (Each included resource still has its own id, type, and attributes.)

**These features are optional** – you don’t need them in every response. However, they can be useful: e.g., meta for
conveying extra stats, links for HATEOAS-style navigation, and included for bundling related data. If you use them, just
ensure they follow the spec’s structure.

## Case style for API responses

- For attributes always use snake_case. Like `first_name`, `created_at`.
- For resource type, use a kebab-case, like `user-profiles` or `line-items`

## Trailing slashes

General REST best practice for trailing slashes must be implemented.  
`/api/v1/files/foo` and `/api/v1/files/foo/` are two different URIs from an HTTP and RFC standpoint.
Accessing a resource with a trailing slash must lead to an error.

The common convention in APIs is:

- Collections as plural nouns, no trailing slash: /files
- Elements without trailing slash: /files/foo

Requests to the “wrong” form:

- send 308 redirect to the canonical URL
- 404 if you want to be strict.
- Serving identical content on both without redirect is discouraged (duplicate URLs, cache fragmentation, SEO / client confusion).

## Implementing JSON:API Responses in Echo

In your Go Echo application, you will construct responses according to this JSON:API structure and return them as JSON.
Echo makes it straightforward to send JSON:

- Construct the response data: Create a Go struct or map that mirrors the JSON:API format (e.g., with fields for Data,
  which contains a slice of resource objects, etc.). Populate it with your resource data or error info.
- Use Echo’s JSON response method: Echo’s Context#JSON(status, data) will serialize your response struct/map to JSON and
  send it with the given HTTP status. For example, after building the response object (containing data or errors), you
  might call return `c.JSON`(http.StatusOK, response) in your handler. Echo will output the JSON to the client.
- Set the correct Content-Type: By default, Echo will use application/json; charset=UTF-8 as the Content-Type for JSON.
  For strict JSON:API compliance, set the header to Content-Type: application/vnd.api+json. This tells clients that the
  response follows the JSON:API specification. You can configure Echo to use this content type, for instance by setting
  the header on the response (c.Response().Header().Set("Content-Type", "application/vnd.api+json")) before calling
  `c.JSON`, if needed.

By following this guide, you ensure your Go Echo server’s responses are formatted per JSON:API standards. This yields
consistent, self-descriptive JSON output that clients (and other developers) can reliably understand and use. With the
examples above as templates, you can confidently construct both successful resource responses and error messages in a
JSON:API-compliant way. Good luck, and happy coding!

## Date and Time

- Every API field representing an instant must use RFC 3339 in UTC with the `Z`
  suffix in requests and responses.
- Normalize persisted and returned instants to exactly six fractional digits.
- Accept at most nine fractional digits in requests.
- Clients convert local input to UTC before sending it. MIA rejects instant
  values with a non-zero numeric offset.
- Use `_at` for the time of an event, `_from` and `_until` for range boundaries,
  and `_for` for a scheduled target time.
- A local-time schedule uses separate local-time and IANA time-zone fields, such
  as `16:00:00` and `Europe/Berlin`. A numeric offset does not preserve local
  scheduling semantics across daylight-saving changes.
- API clients display instants in the viewer's preferred IANA time zone. The
  server uses that preference for email, SMS, and other generated
  communications.
- Student-only accounts cannot change their preferred time zone. Any supervisor
  who shares an assigned course with a student can edit that student's
  non-security profile fields, including the preferred time zone. Every change
  must be audited.

  ```json
  {
    "created_at": "2025-01-14T16:32:45.000000Z",
    "valid_until": "2025-12-31T21:59:59.000000Z"
  }
  ```

## Pagination

Pagination will be implemented offset-based supporting `page[limit]` and page`[offset]`.

## Rate limiting

- Every unauthenticated API endpoint must be rate limited.
- Return HTTP `429 Too Many Requests` using the standard JSON:API error format
  and a stable machine-readable error code.
- Include `Retry-After` when the next permitted retry time is known.
- Rate-limit responses must not reveal whether an account, invitation, mobile
  number, or other sensitive identifier exists.
- Authentication, MFA, invitation, registration, password-reset, and SMS-code
  endpoints require dedicated limits in addition to the global unauthenticated
  limit.
- The MVP has no separate authenticated tutoring, upload, material-finalization,
  or speech rate limits.

## API documentation

Documentation goes to `./api-doc` and must follow the OpenAPI 3.2 standard.
The documentation must be split into multiple files with ./api-doc/openapi.yaml as the entry point.

API documentation must be linted and validate using

```shell
npx @redocly/cli lint --config .redocly.yaml ./api-doc/openapi.yaml
```
