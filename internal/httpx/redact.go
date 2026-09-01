package httpx

import (
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
// parameter — Meta's access_token, an S3 signature — and Telegram's path,
// where the bot token is a path segment rather than a header.
func RedactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	clean := *u
	clean.Path = redactPath(clean.Path)
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
		if !secretQuery[strings.ToLower(key)] {
			continue
		}
		for i := range list {
			if list[i] != "" {
				list[i] = RedactedValue
			}
		}
	}
	return values.Encode()
}

// redactPath masks a credential that lives in the path rather than in a header.
//
// Telegram is the reason: its Bot API is /bot<token>/sendPhoto, so the whole
// credential is in the path of every call and in every error about one.
func redactPath(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if rest, ok := strings.CutPrefix(seg, "bot"); ok && strings.Contains(rest, ":") {
			// A bot token is <id>:<secret>; the colon is what tells it apart
			// from a path segment that merely starts with "bot".
			id, _, _ := strings.Cut(rest, ":")
			segments[i] = "bot" + id + ":" + RedactedValue
		}
	}
	return strings.Join(segments, "/")
}
