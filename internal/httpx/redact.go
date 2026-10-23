package httpx

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// RedactedValue replaces a secret wherever one would otherwise be printed.
const RedactedValue = "REDACTED"

// secretQuery are the query parameters that carry a credential.
//
// Meta puts access_token in the query of every Graph call, S3 presigning puts
// a signature in the query of every staged URL, and an error message or a
// retry warning naming the URL would otherwise put both in the log — and logs
// get pasted into issues.
//
// Matched case-insensitively, because the platforms disagree about case.
var secretQuery = map[string]bool{
	"access_token":           true,
	"accesstoken":            true,
	"token":                  true,
	"api_key":                true,
	"apikey":                 true,
	"key":                    true,
	"client_secret":          true,
	"refresh_token":          true,
	"password":               true,
	"signature":              true,
	"sig":                    true,
	"x-amz-signature":        true,
	"x-amz-credential":       true,
	"x-amz-security-token":   true,
	"x-goog-signature":       true,
	"upload_token":           true,
	"session_token":          true,
	"code":                   true,
	"assertion":              true,
	"client_assertion":       true,
	"x-ms-encryption-key":    true,
	"authorization":          true,
	"x-amz-content-sha256":   false, // a hash of the body, not a credential
	"x-amz-signedheaders":    false,
	"x-amz-algorithm":        false,
	"x-amz-date":             false,
	"x-amz-expires":          false,
	"x-goog-signedheaders":   false,
	"x-goog-algorithm":       false,
	"x-goog-date":            false,
	"x-goog-expires":         false,
	"x-goog-credential":      true,
	"x-amz-security-token-2": true,
}

// RedactURL renders a URL with every credential in it masked.
//
// url.URL.Redacted only hides a password in the userinfo, which is the one
// place crier never puts a secret. The two places it does are a query
// parameter — Meta's access_token, an S3 signature — and credential-bearing
// paths such as Telegram bot tokens and Discord webhook tokens.
func RedactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	clean := *u
	if clean.Opaque != "" {
		return RedactedValue
	}
	if clean.Fragment != "" {
		clean.Fragment, clean.RawFragment = RedactedValue, ""
	}
	clean.Path = strings.Join(redactPath(pathSegments(u)), "/")
	clean.RawPath = ""
	if clean.RawQuery != "" {
		clean.RawQuery = redactQuery(clean.RawQuery)
	}
	return clean.Redacted()
}

// RedactURLString is RedactURL for a URL that is already a string.
//
// A string that does not parse is not passed through: a URL crier cannot read
// is also a URL it cannot promise carries no token.
func RedactURLString(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return RedactedValue
	}
	return RedactURL(u)
}

// redactQuery masks the values of the parameters that carry credentials, and
// leaves the rest alone so a log line still says what was asked for.
func redactQuery(raw string) string {
	values, err := url.ParseQuery(raw)
	if err != nil {
		// An unparseable query may hold anything, including a token.
		return RedactedValue
	}
	for key, list := range values {
		for i := range list {
			if list[i] != "" && (secretName(key) || tokenLike(list[i])) {
				list[i] = RedactedValue
			}
		}
	}
	return values.Encode()
}

// secretName also recognizes custom API credential names, with common word
// separators ignored. Explicit non-secret signing metadata stays visible.
func secretName(name string) bool {
	lower := strings.ToLower(name)
	if secret, known := secretQuery[lower]; known {
		return secret
	}
	normal := strings.NewReplacer("-", "", "_", "", ".", "").Replace(lower)
	for _, suffix := range []string{"token", "secret", "password", "signature", "apikey", "authorization", "credential"} {
		if strings.HasSuffix(normal, suffix) {
			return true
		}
	}
	return false
}

// tokenLike errs toward hiding long opaque values, including JWTs and custom
// API keys whose parameter names crier cannot know. Short ordinary IDs and
// filenames remain useful in diagnostics.
func tokenLike(value string) bool {
	if len(value) < 32 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_-.:+=", c)) {
			return false
		}
	}
	return true
}

// pathSegments decodes each escaped segment independently so an encoded slash
// inside a credential cannot turn its tail into an apparently safe path.
func pathSegments(u *url.URL) []string {
	segments := strings.Split(u.EscapedPath(), "/")
	for i, segment := range segments {
		segments[i], _ = url.PathUnescape(segment)
	}
	return segments
}

// redactPath handles known credential paths even when a token is too short to
// look opaque. Discord webhook IDs remain visible; the following token does not.
func redactPath(original []string) []string {
	segments := append([]string(nil), original...)
	for i, seg := range segments {
		switch {
		case strings.HasPrefix(seg, "bot") && strings.Contains(seg[3:], ":"):
			id, _, _ := strings.Cut(seg[3:], ":")
			segments[i] = "bot" + id + ":" + RedactedValue
		case i >= 2 && strings.EqualFold(segments[i-2], "webhooks"):
			segments[i] = RedactedValue
		case i > 0 && segments[i-1] == original[i-1] && secretName(original[i-1]), tokenLike(seg):
			segments[i] = RedactedValue
		}
	}
	return segments
}

// transportError retains the original cause for errors.Is/As while displaying
// only the scrubbed message. Timeout/Temporary preserve net.Error behavior.
type transportError struct {
	cause   error
	message string
}

func (e *transportError) Error() string { return e.message }
func (e *transportError) Unwrap() error { return e.cause }
func (e *transportError) Timeout() bool {
	var n net.Error
	return errors.As(e.cause, &n) && n.Timeout()
}
func (e *transportError) Temporary() bool {
	var n interface{ Temporary() bool }
	return errors.As(e.cause, &n) && n.Temporary()
}

// redactTransportError runs both before transport logging and after Client.Do:
// net/http adds its own *url.Error outside the transport. Never mutate one
// supplied by a caller or transport, which may be shared with another request.
func redactTransportError(err error, urls ...*url.URL) error {
	if err == nil {
		return nil
	}
	if e, ok := err.(*url.Error); ok {
		clean := *e
		u, _ := url.Parse(e.URL)
		clean.URL = RedactURLString(e.URL)
		clean.Err = redactTransportError(e.Err, append(urls, u)...)
		return &clean
	}
	message := err.Error()
	for _, u := range urls {
		if u == nil {
			continue
		}
		message = strings.ReplaceAll(message, u.String(), RedactURL(u))
		// A transport can repeat individual URL components in its cause.
		original := pathSegments(u)
		clean := redactPath(original)
		var secrets []string
		for i, value := range original {
			if value != clean[i] {
				secrets = append(secrets, value)
				if strings.HasPrefix(value, "bot") {
					_, token, _ := strings.Cut(value, ":")
					secrets = append(secrets, token)
				}
			}
		}
		for key, values := range u.Query() {
			for _, value := range values {
				if secretName(key) || tokenLike(value) {
					secrets = append(secrets, value)
				}
			}
		}
		if u.User != nil {
			password, _ := u.User.Password()
			secrets = append(secrets, password)
		}
		secrets = append(secrets, u.Fragment, u.Opaque)
		for _, secret := range secrets {
			if secret == "" {
				continue
			}
			for _, form := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
				message = strings.ReplaceAll(message, form, RedactedValue)
			}
		}
	}
	if message == err.Error() {
		return err
	}
	return &transportError{cause: err, message: message}
}
