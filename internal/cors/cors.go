// Package cors provides a small, dependency-free CORS middleware for gin,
// configured from a list of allowed origins (typically sourced from an env var).
package cors

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// ParseOrigins splits a comma-separated origin list (e.g. the value of the
// CORS_ALLOWED_ORIGINS env var) into a cleaned slice, trimming whitespace and
// dropping empty entries.
func ParseOrigins(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Middleware returns a gin middleware that emits CORS headers for the given
// allowed origins. A single entry of "*" allows any origin. If origins is empty
// the middleware is a no-op, so CORS stays off unless explicitly configured.
//
// Requests are matched against the allowlist by exact Origin header value; a
// matching request gets Access-Control-Allow-Origin (and a Vary: Origin hint so
// caches don't serve the wrong origin's response). Preflight OPTIONS requests
// are answered with 204 once CORS is enabled.
func Middleware(origins []string) gin.HandlerFunc {
	allowAll := false
	allowed := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		switch {
		case o == "*":
			allowAll = true
		case o != "":
			allowed[o] = struct{}{}
		}
	}

	enabled := allowAll || len(allowed) > 0

	return func(c *gin.Context) {
		if !enabled {
			c.Next()
			return
		}

		origin := c.GetHeader("Origin")
		if origin != "" {
			var allow string
			switch {
			case allowAll:
				allow = "*"
			case contains(allowed, origin):
				allow = origin
			}

			if allow != "" {
				h := c.Writer.Header()
				h.Set("Access-Control-Allow-Origin", allow)
				// Only an exact-origin reflection is origin-dependent; "*" is not.
				if allow != "*" {
					h.Add("Vary", "Origin")
				}
				h.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Origin, Accept, Content-Type, Authorization")
				h.Set("Access-Control-Max-Age", "86400")
			}
		}

		// Short-circuit preflight requests; the body is irrelevant to the browser.
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func contains(set map[string]struct{}, key string) bool {
	_, ok := set[key]
	return ok
}
