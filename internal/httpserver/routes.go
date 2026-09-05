package httpserver

import (
	"mime"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
)

// Representation classifies a route's HTTP representation for JSON:API media
// negotiation. Routes that do not produce JSON:API documents are exempt from
// the spec-minimal Accept check.
type Representation uint8

const (
	// RepresentationJSONAPI marks a route producing JSON:API documents.
	RepresentationJSONAPI Representation = iota
	// RepresentationBinary marks binary or PNG download routes.
	RepresentationBinary
	// RepresentationMultipart marks multipart/form-data upload routes.
	RepresentationMultipart
	// RepresentationImageUpload marks raw image/jpeg or image/png upload routes.
	RepresentationImageUpload
	// RepresentationNDJSON marks application/x-ndjson streaming routes.
	RepresentationNDJSON
	// RepresentationSSE marks text/event-stream routes.
	RepresentationSSE
)

// routeClass classifies a route's authentication exposure. Public and
// authentication-sensitive routes consume the global unauthenticated API
// limit; authenticated routes must not.
type routeClass uint8

const (
	routePublic routeClass = iota
	routeAuthenticationSensitive
	routeAuthenticated
)

type routeRegistration struct {
	class          routeClass
	representation Representation
}

// classify records a route's authentication class and representation. Route
// registration happens during single-threaded startup wiring; the registry is
// read-only once the server starts handling requests.
func (server *Server) classify(method, path string, class routeClass, representation Representation) {
	server.routes[method+" "+path] = routeRegistration{class: class, representation: representation}
}

// matchedRoute resolves the registration of the route matched by the router.
func (server *Server) matchedRoute(c *echo.Context) (routeRegistration, bool) {
	registration, ok := server.routes[c.Request().Method+" "+c.Path()]
	return registration, ok
}

// Public registers a publicly accessible JSON:API route. Public routes consume
// the global unauthenticated API limit.
func (server *Server) Public(method, path string, next echo.HandlerFunc) {
	server.classify(method, path, routePublic, RepresentationJSONAPI)
	server.register(method)(path, next)
}

// AuthenticationSensitive registers a public JSON:API route that carries
// dedicated layered limits in addition to the global unauthenticated API
// limit, such as login, recovery, reset, and invitation routes.
func (server *Server) AuthenticationSensitive(method, path string, next echo.HandlerFunc) {
	server.classify(method, path, routeAuthenticationSensitive, RepresentationJSONAPI)
	server.register(method)(path, next)
}

// AuthenticatedRoute registers an ordinary protected route with an explicit
// non-default representation, such as a binary download or multipart upload.
func (server *Server) AuthenticatedRoute(method, path string, representation Representation, next echo.HandlerFunc) {
	server.classify(method, path, routeAuthenticated, representation)
	server.authenticated(path, "authenticated", next, server.register(method))
}

func (server *Server) register(method string) func(string, echo.HandlerFunc, ...echo.MiddlewareFunc) echo.RouteInfo {
	switch method {
	case http.MethodGet:
		return server.Echo.GET
	case http.MethodPost:
		return server.Echo.POST
	case http.MethodPatch:
		return server.Echo.PATCH
	case http.MethodPut:
		return server.Echo.PUT
	case http.MethodDelete:
		return server.Echo.DELETE
	default:
		panic("unsupported route method")
	}
}

// stageClass maps an auth login stage to a route class. Challenge-stage routes
// keep the global public limit in addition to their layered limits.
func stageClass(stage string) routeClass {
	if stage == "mfa" || stage == "password-change" {
		return routeAuthenticationSensitive
	}
	return routeAuthenticated
}

// acceptMiddleware applies the spec-minimal JSON:API Accept check to API
// routes that produce JSON:API documents. Unknown routes reach ordinary router
// handling before negotiation; binary, upload, NDJSON, and SSE routes are exempt.
func (server *Server) acceptMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if !isAPIPath(c.Request().URL.Path) {
			return next(c)
		}
		registration, ok := server.matchedRoute(c)
		if !ok || registration.representation != RepresentationJSONAPI {
			return next(c)
		}
		if !jsonAPIAcceptable(c.Request().Header.Values("Accept")) {
			return NewError(CodeNotAcceptable)
		}
		return next(c)
	}
}

// jsonAPIAcceptable implements the spec-minimal negotiation rule: reject only
// when the Accept header contains application/vnd.api+json instances and every
// one of them carries media-type parameters. A missing Accept header, */*, and
// unrelated media types are acceptable. The q weight is an Accept parameter,
// not a media-type parameter, and is ignored; unparsable instances are ignored.
func jsonAPIAcceptable(headers []string) bool {
	sawJSONAPI := false
	totalLength := 0
	instances := 0
	for _, header := range headers {
		totalLength += len(header)
		if totalLength > 16<<10 {
			return false
		}
		parts, ok := splitAcceptValues(header)
		if !ok {
			return false
		}
		instances += len(parts)
		if instances > 256 {
			return false
		}
		for _, instance := range parts {
			instance = strings.TrimSpace(instance)
			if instance == "" {
				continue
			}
			mediaType, parameters, err := mime.ParseMediaType(instance)
			if err != nil || mediaType != jsonAPI {
				continue
			}
			sawJSONAPI = true
			delete(parameters, "q")
			if len(parameters) == 0 {
				return true
			}
		}
	}
	return !sawJSONAPI
}

// splitAcceptValues separates list members only at commas outside quoted
// parameter values. Backslash escapes quote the following byte inside a quote.
func splitAcceptValues(header string) ([]string, bool) {
	parts := make([]string, 0, 4)
	start := 0
	quoted := false
	escaped := false
	for index := 0; index < len(header); index++ {
		switch {
		case escaped:
			escaped = false
		case quoted && header[index] == '\\':
			escaped = true
		case header[index] == '"':
			quoted = !quoted
		case header[index] == ',' && !quoted:
			parts = append(parts, header[start:index])
			start = index + 1
		}
	}
	if quoted || escaped {
		return nil, false
	}
	return append(parts, header[start:]), true
}
