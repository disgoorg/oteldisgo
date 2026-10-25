package oteldisgo

import (
	"net/http"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// NewTransport wraps the provided http.RoundTripper (defaulting to http.DefaultTransport if nil)
// and redacts Discord interaction and webhook tokens from the "url.full" attribute on the active OpenTelemetry span.
func NewTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{base: base}
}

type transport struct {
	base http.RoundTripper
}

func (t *transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL != nil {
		if span := oteltrace.SpanFromContext(r.Context()); span.IsRecording() {
			if redacted, ok := redactDiscordURL(r.URL); ok {
				span.SetAttributes(attribute.String("url.full", redacted))
			}
		}
	}
	return t.base.RoundTrip(r)
}

func (t *transport) CloseIdleConnections() {
	type closeIdler interface {
		CloseIdleConnections()
	}
	if tr, ok := t.base.(closeIdler); ok {
		tr.CloseIdleConnections()
	}
}

func redactDiscordURL(u *url.URL) (string, bool) {
	redactedPath, ok := redactDiscordPath(u.Path)
	if !ok {
		return "", false
	}
	uCopy := *u
	uCopy.User = nil
	uCopy.Path = redactedPath
	uCopy.RawPath = ""
	return uCopy.String(), true
}

func redactDiscordPath(path string) (string, bool) {
	parts := strings.Split(path, "/")
	for i := range parts {
		if (parts[i] == "interactions" || parts[i] == "webhooks") && i+2 < len(parts) && parts[i+1] != "" && parts[i+2] != "" {
			parts[i+2] = "REDACTED"
			return strings.Join(parts, "/"), true
		}
	}
	return path, false
}
