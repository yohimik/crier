package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"
)

// MaxBufferedBody is how large a replayable body may be before it is streamed
// instead of buffered. A streamed body can still be replayed — it is rebuilt
// from the file on disk — but it goes out chunked, without a Content-Length.
const MaxBufferedBody = 20 << 20

// Builder assembles one HTTP request.
//
// Every body it produces sets GetBody, so the retry transport can replay it.
// That is the whole reason the builder exists rather than callers reaching for
// http.NewRequest: a multipart upload built by hand is exactly the request
// that cannot be retried.
type Builder struct {
	method  string
	rawURL  string
	query   url.Values
	header  http.Header
	body    io.Reader
	getBody func() (io.ReadCloser, error)
	length  int64
	err     error
}

// NewRequest starts a request. Path segments are joined onto base with a
// single slash between them.
func NewRequest(method, base string, segments ...string) *Builder {
	return &Builder{
		method: method,
		rawURL: JoinURL(base, segments...),
		query:  url.Values{},
		header: http.Header{},
		length: -1,
	}
}

// JoinURL joins a base URL and path segments, collapsing the slashes between
// them. Segments are used as given, so a caller that needs escaping does it.
func JoinURL(base string, segments ...string) string {
	out := strings.TrimRight(base, "/")
	for _, s := range segments {
		s = strings.Trim(s, "/")
		if s == "" {
			continue
		}
		out += "/" + s
	}
	return out
}

// Query adds a query parameter.
func (b *Builder) Query(key, value string) *Builder {
	b.query.Set(key, value)
	return b
}

// QueryIf adds a query parameter only when the condition holds.
func (b *Builder) QueryIf(cond bool, key, value string) *Builder {
	if cond {
		b.query.Set(key, value)
	}
	return b
}

// Header sets a header.
func (b *Builder) Header(key, value string) *Builder {
	b.header.Set(key, value)
	return b
}

// Bearer sets the Authorization header.
func (b *Builder) Bearer(token string) *Builder {
	return b.Header("Authorization", "Bearer "+token)
}

// JSON sets a JSON body.
func (b *Builder) JSON(v any) *Builder {
	if b.err != nil {
		return b
	}
	data, err := json.Marshal(v)
	if err != nil {
		b.err = fmt.Errorf("encoding request body: %w", err)
		return b
	}
	return b.Bytes("application/json", data)
}

// Form sets an application/x-www-form-urlencoded body.
func (b *Builder) Form(values url.Values) *Builder {
	return b.Bytes("application/x-www-form-urlencoded", []byte(values.Encode()))
}

// Bytes sets a body from memory.
func (b *Builder) Bytes(contentType string, data []byte) *Builder {
	if b.err != nil {
		return b
	}
	b.header.Set("Content-Type", contentType)
	b.length = int64(len(data))
	b.body = bytes.NewReader(data)
	b.getBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return b
}

// File sets the body to the contents of a file, streamed from disk and
// replayable by re-opening it.
func (b *Builder) File(contentType, path string) *Builder {
	if b.err != nil {
		return b
	}
	st, err := os.Stat(path)
	if err != nil {
		b.err = fmt.Errorf("reading request body: %w", err)
		return b
	}
	b.header.Set("Content-Type", contentType)
	b.length = st.Size()
	open := func() (io.ReadCloser, error) { return os.Open(path) }
	f, err := open()
	if err != nil {
		b.err = fmt.Errorf("reading request body: %w", err)
		return b
	}
	b.body = f
	b.getBody = func() (io.ReadCloser, error) { return open() }
	return b
}

// Part is one piece of a multipart body: either a plain field, or a file whose
// content comes from Open.
type Part struct {
	// Name is the form field name.
	Name string
	// Value is the field's content, for a plain field.
	Value string
	// FileName, when set, makes this a file part.
	FileName string
	// ContentType is the part's own Content-Type, for a file part.
	ContentType string
	// Open produces the file part's content. It is called once per attempt, so
	// it must be repeatable.
	Open func() (io.ReadCloser, error)
	// Size is the file part's length, used to decide whether the body is
	// buffered or streamed. A negative size means unknown, which forces
	// streaming.
	Size int64
}

// FilePart makes a file part reading from a path on disk.
func FilePart(name, path, contentType string) Part {
	p := Part{Name: name, FileName: baseName(path), ContentType: contentType, Size: -1}
	if st, err := os.Stat(path); err == nil {
		p.Size = st.Size()
	}
	p.Open = func() (io.ReadCloser, error) { return os.Open(path) }
	return p
}

// BytesPart makes a file part from memory.
func BytesPart(name, fileName, contentType string, data []byte) Part {
	return Part{
		Name: name, FileName: fileName, ContentType: contentType,
		Size: int64(len(data)),
		Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil },
	}
}

// Field makes a plain form field.
func Field(name, value string) Part { return Part{Name: name, Value: value} }

func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// Multipart sets a multipart/form-data body.
//
// A body that fits in MaxBufferedBody is built once in memory, which gives it
// a Content-Length and makes replaying it free. A larger one is streamed
// through a pipe and rebuilt from scratch for each attempt.
func (b *Builder) Multipart(parts ...Part) *Builder {
	if b.err != nil {
		return b
	}
	total := int64(0)
	streaming := false
	for _, p := range parts {
		if p.Open == nil {
			total += int64(len(p.Value))
			continue
		}
		if p.Size < 0 {
			streaming = true
			break
		}
		total += p.Size
	}
	if streaming || total > MaxBufferedBody {
		return b.multipartStreamed(parts)
	}
	return b.multipartBuffered(parts)
}

func (b *Builder) multipartBuffered(parts []Part) *Builder {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := writeParts(w, parts); err != nil {
		b.err = err
		return b
	}
	if err := w.Close(); err != nil {
		b.err = err
		return b
	}
	return b.Bytes(w.FormDataContentType(), buf.Bytes())
}

func (b *Builder) multipartStreamed(parts []Part) *Builder {
	boundary := multipart.NewWriter(io.Discard).Boundary()
	open := func() (io.ReadCloser, error) {
		pr, pw := io.Pipe()
		w := multipart.NewWriter(pw)
		if err := w.SetBoundary(boundary); err != nil {
			return nil, err
		}
		go func() {
			err := writeParts(w, parts)
			if err == nil {
				err = w.Close()
			}
			_ = pw.CloseWithError(err)
		}()
		return pr, nil
	}
	body, err := open()
	if err != nil {
		b.err = err
		return b
	}
	b.header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	b.length = -1
	b.body = body
	b.getBody = open
	return b
}

func writeParts(w *multipart.Writer, parts []Part) error {
	for _, p := range parts {
		if p.Open == nil {
			if err := w.WriteField(p.Name, p.Value); err != nil {
				return err
			}
			continue
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition",
			`form-data; name="`+escapeQuotes(p.Name)+`"; filename="`+escapeQuotes(p.FileName)+`"`)
		if p.ContentType != "" {
			h.Set("Content-Type", p.ContentType)
		}
		dst, err := w.CreatePart(h)
		if err != nil {
			return err
		}
		src, err := p.Open()
		if err != nil {
			return err
		}
		_, err = io.Copy(dst, src)
		closeErr := src.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeQuotes(s string) string { return quoteEscaper.Replace(s) }

// Build produces the request. Every error the builder swallowed surfaces here.
func (b *Builder) Build(ctx context.Context) (*http.Request, error) {
	if b.err != nil {
		return nil, b.err
	}
	raw := b.rawURL
	if len(b.query) > 0 {
		sep := "?"
		if strings.Contains(raw, "?") {
			sep = "&"
		}
		raw += sep + b.query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, b.method, raw, b.body)
	if err != nil {
		return nil, err
	}
	for k, vs := range b.header {
		req.Header[k] = append([]string(nil), vs...)
	}
	if b.getBody != nil {
		req.GetBody = b.getBody
	}
	if b.length >= 0 {
		req.ContentLength = b.length
	} else if b.body != nil {
		// An unknown length has to be spelled out, or net/http would read the
		// zero value as "no body".
		req.ContentLength = -1
	}
	return req, nil
}
