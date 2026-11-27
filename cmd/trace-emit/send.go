package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/0xmhha/trace-agent/internal/extension"
)

// sendTimeout bounds one send from the first byte written to the last byte
// of the response read, so that tracing cannot stall the caller.
const sendTimeout = 500 * time.Millisecond

// endpointPrefix is everything of an accepted endpoint address before the
// port. Only the port may vary: the event and the credential must not leave
// this machine, and a host name such as localhost can be made to resolve
// elsewhere.
const endpointPrefix = "http://127.0.0.1:"

// eventIDBytes is the amount of randomness in an event identifier.
const eventIDBytes = 16

// maxResponseBytes bounds the response read. A full response lists at most
// extension.MaxEvents rejections; one event needs far less.
const maxResponseBytes = 64 << 10

const (
	headerAuthorization = "Authorization"
	headerContentType   = "Content-Type"
	bearerPrefix        = "Bearer "
	contentTypeJSON     = "application/json"
)

var (
	errNoEndpoint      = errors.New(envURL + " is not set")
	errEndpointRefused = errors.New(envURL + " is not " + endpointPrefix + "<port>" + extension.EventsPath)
	errNoToken         = errors.New(envToken + " is not set")
	errTokenRefused    = errors.New(envToken + " is not usable in a request header")
	errBodyTooLarge    = errors.New("event is larger than the request limit")
	errNotAccepted     = errors.New("endpoint did not accept the request")
	errRejected        = errors.New("endpoint rejected the event")
)

// destination is a checked endpoint address and its credential.
type destination struct {
	url   string
	token string
}

// resolveDestination reads the endpoint and the credential from the
// environment. Its errors never contain either value: a mistyped address can
// carry a credential.
func resolveDestination(getenv func(string) string) (destination, error) {
	raw := getenv(envURL)
	if raw == "" {
		return destination{}, errNoEndpoint
	}
	target, err := loopbackEndpoint(raw)
	if err != nil {
		return destination{}, err
	}
	token := getenv(envToken)
	switch {
	case token == "":
		return destination{}, errNoToken
	case !headerSafe(token):
		return destination{}, errTokenRefused
	}
	return destination{url: target, token: token}, nil
}

// loopbackEndpoint accepts raw only when it is exactly
// http://127.0.0.1:<port>/v1/events with a canonical port, and returns it.
// Matching the text, rather than parsing a URL, leaves no room for user
// info, a query, a fragment or an alternative spelling of the host.
func loopbackEndpoint(raw string) (string, error) {
	rest, found := strings.CutPrefix(raw, endpointPrefix)
	if !found {
		return "", errEndpointRefused
	}
	digits, found := strings.CutSuffix(rest, extension.EventsPath)
	if !found {
		return "", errEndpointRefused
	}
	port, err := strconv.ParseUint(digits, 10, 16)
	if err != nil || port == 0 || strconv.FormatUint(port, 10) != digits {
		return "", errEndpointRefused
	}
	return endpointPrefix + digits + extension.EventsPath, nil
}

// headerSafe reports whether token is visible ASCII, which is all a bearer
// credential may contain. A line break would split the header.
func headerSafe(token string) bool {
	for i := range len(token) {
		if token[i] <= ' ' || token[i] > '~' {
			return false
		}
	}
	return true
}

// newEventID returns the idempotency key of one event.
func newEventID(random io.Reader) (string, error) {
	var buf [eventIDBytes]byte
	if _, err := io.ReadFull(random, buf[:]); err != nil {
		return "", fmt.Errorf("generate event id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// deliver completes ev with its source and identifier and posts it to dest.
func (e environment) deliver(ctx context.Context, dest destination, ev extension.WireEvent) error {
	var err error
	if ev.Source, err = resolveSource(ev.Source, e.getenv); err != nil {
		return err
	}
	if ev.ID, err = newEventID(e.random); err != nil {
		return err
	}
	return e.post(ctx, dest, ev)
}

// post sends ev as one request and reads the verdict of the endpoint.
func (e environment) post(ctx context.Context, dest destination, ev extension.WireEvent) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	if len(body) > extension.MaxBodyBytes {
		return errBodyTooLarge
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, dest.url, bytes.NewReader(body)) // #nosec G107 G704 -- loopbackEndpoint fixed the host and the path; only a 16-bit port varies.
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set(headerAuthorization, bearerPrefix+dest.token)
	req.Header.Set(headerContentType, contentTypeJSON)
	client := http.Client{
		Transport: e.transport,
		// A redirect could carry the credential to another address.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req) // #nosec G107 G704 -- see the request above.
	if err != nil {
		return fmt.Errorf("send event: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("%w: status %d", errNotAccepted, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	var verdict extension.WireResponse
	if err := json.Unmarshal(raw, &verdict); err != nil {
		return errors.New("response is not the expected JSON")
	}
	if len(verdict.Rejected) > 0 {
		// Quoted because the reason comes from another process and is
		// printed to a terminal.
		return fmt.Errorf("%w: %q", errRejected, verdict.Rejected[0].Reason)
	}
	return nil
}
