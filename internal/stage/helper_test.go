package stage

import (
	"net/http"
	"net/http/httptest"
)

// httpTestServer is a thin alias so the test file reads without repeating the
// httptest package name in every signature.
type httpTestServer = httptest.Server

func newHTTPTestServer(h http.HandlerFunc) *httpTestServer {
	return httptest.NewServer(h)
}
