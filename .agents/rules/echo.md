# Echo 5 rules

MIA uses Echo 5.3.1. These rules supplement the Go, API, and security rules. Do
not duplicate those contracts here.

## Version and handlers

- Import Echo from `github.com/labstack/echo/v5`.
- Use the Echo 5 handler signature `func(c *echo.Context) error`. Do not use the
  Echo 4 `echo.Context` signature.
- Keep handlers focused on HTTP concerns: decode and validate input, authorize
  the action, call application logic, and encode the result.
- Pass `c.Request().Context()` into request-scoped work. Do not replace it with
  `context.Background()`.

## Input

- Bind requests into explicit input types. Never bind requests directly into
  persistence or domain types containing fields the caller may not set.
- Validate bound input before using it.
- Apply a body-size limit before reading or binding request bodies.
- Do not trust filenames, content types, forwarded headers, relationship IDs,
  role fields, or ownership fields supplied by a client.
- Return errors through the central error handler. Do not construct ad hoc error
  envelopes in individual handlers.

## Output and errors

- Use one central `HTTPErrorHandler` to map application errors to the MIA API
  error contract.
- Do not expose internal errors, stack traces, SQL details, filesystem paths,
  prompts, credentials, tokens, or personal data in responses.
- Set the documented media type for every API response. Binary and streaming
  endpoints must use only their explicitly documented media types.
- Do not use redirects or HTML error pages for API errors unless the API
  contract explicitly requires them.

## Middleware

- Register middleware in an intentional, documented order. Tests must protect
  security-relevant ordering.
- Recover panics at the HTTP boundary, log them with a request correlation ID,
  and return the central internal-error response without sensitive details.
- Add request correlation and structured request logging without logging
  credentials, cookies, authorization headers, MFA values, message bodies,
  uploads, prompts, or model responses.
- Apply authentication before authorization on protected routes.
- Apply CSRF protection according to the browser-session contract. Do not use
  CORS as a substitute for CSRF protection.
- Configure CORS with an explicit origin policy. Do not combine credentials
  with a wildcard origin.
- Apply secure response headers, rate limits, and body limits at the narrowest
  scope that covers every relevant route.
- Apply a global rate limit to every unauthenticated API route and stricter
  limits to login, recovery, invitation, registration, MFA, and SMS-code routes.
- Derive IP-based limiter keys only through the configured trusted-proxy client
  IP extractor. Bound limiter state and expiration.
- Use combined IP and account or challenge keys for authentication-sensitive
  routes. Do not create permanent account lockouts through middleware.
- Do not enable request or response body-dump middleware in production.

## Reverse proxy and server

- Configure Echo's client-IP extraction only for explicitly trusted proxies.
  Do not trust forwarded headers from arbitrary clients.
- Configure HTTP read-header, read, write, and idle timeouts. Streaming routes
  may use a separately justified write-timeout policy.
- Use Echo 5's context-driven startup and graceful shutdown support.
- Shutdown must stop accepting requests and allow in-flight requests to finish
  within a configured deadline. Coordinate worker shutdown separately rather
  than abandoning jobs.
- Treat listener and proxy settings as configuration. Do not hard-code
  deployment-specific addresses in handlers or route setup.

## Routes and files

- Register API routes under the documented version prefix.
- Apply authorization to the resource, action, course, student, and ownership
  scope. Route grouping alone is not an authorization check.
- Keep uploaded files and generated content outside the public static root.
- Authorize every file and audio download before serving content.
- Do not rely on a static-file route, sibling route, or route-registration order
  as an access-control boundary.
- Use safe download headers and never expose an internal storage path.

## Testing

- Exercise handlers and middleware through `httptest` and Echo's `ServeHTTP`.
- Test status, headers, media type, response body, and side effects.
- Test unauthenticated, unauthorized, wrong-course, and wrong-student access for
  protected resources.
- Test body limits, malformed input, validation errors, central error mapping,
  panic recovery, and trusted-proxy behavior where relevant.
- Do not make live calls to paid or production services from handler tests.
