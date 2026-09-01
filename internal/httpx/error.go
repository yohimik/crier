package httpx

import (
	"fmt"
	"net/http"
	"strings"
)

// MaxErrorBody is how much of a failing response is kept for the error
// message. Enough for a JSON error object, small enough for a log line.
const MaxErrorBody = 4096

// APIError is a non-2xx response from a platform API.
//
// It keeps the status, the request that produced it and a bounded prefix of
// the body, because "publishing failed" without the platform's own words is
// not something an operator can act on.
type APIError struct {
	Method string
	URL    string
	Status int
	Header http.Header
	Body   []byte
}

func (e *APIError) Error() string {
	body := strings.TrimSpace(string(e.Body))
	if body == "" {
		return fmt.Sprintf("%s %s: %s", e.Method, e.URL, http.StatusText(e.Status))
	}
	return fmt.Sprintf("%s %s: %d %s: %s", e.Method, e.URL, e.Status, http.StatusText(e.Status), body)
}

// StatusCode lets callers switch on the status without a type assertion dance.
func (e *APIError) StatusCode() int { return e.Status }

// Temporary reports whether the status is one that may succeed on a retry.
func (e *APIError) Temporary() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// RateLimited reports whether the platform asked us to slow down.
func (e *APIError) RateLimited() bool { return e.Status == http.StatusTooManyRequests }
