package extension

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Admission limits of the endpoint.
const (
	// maxInFlight is how many requests are handled at once; one more is told
	// to come back instead of queueing behind a slow store.
	maxInFlight = 4
	// sinkTimeout bounds the store calls of one request. It stays below the
	// server write timeout so that a slow store still gets an answer out.
	sinkTimeout = 2 * time.Second
)

// Credential rules.
const (
	bearerPrefix = "Bearer "
	minBearerLen = 32
	maxBearerLen = 256
)

// Header names and values the handler reads or writes.
const (
	headerAuthorization = "Authorization"
	headerContentType   = "Content-Type"
	headerOrigin        = "Origin"
	headerRetryAfter    = "Retry-After"
	jsonMediaType       = "application/json"
	// retryAfterSeconds is the wait suggested with a 429: one rate window.
	retryAfterSeconds = "1"
)

// unattributed is the source refusals are counted under when the input could
// not be tied to a sender.
const unattributed = ""

// errPayload marks a body that is not a single event object or a batch.
var errPayload = errors.New("extension: body is not an event or a batch")

// handler is the event endpoint. It holds no state besides the two admission
// limits.
type handler struct {
	sink    usage.ExtSink
	host    string
	digest  [sha256.Size]byte
	now     func() time.Time
	random  io.Reader
	workers chan struct{}
	limiter *rateLimiter
}

// newHandler builds the handler from arguments the caller has validated.
// random feeds the IDs of events that nothing else identifies.
func newHandler(sink usage.ExtSink, host, bearer string, now func() time.Time, random io.Reader) *handler {
	return &handler{
		sink:    sink,
		host:    host,
		digest:  sha256.Sum256([]byte(bearerPrefix + bearer)),
		now:     now,
		random:  random,
		workers: make(chan struct{}, maxInFlight),
		limiter: &rateLimiter{},
	}
}

// validBearer reports whether a credential can be sent as one header value.
func validBearer(bearer string) bool {
	if len(bearer) < minBearerLen || len(bearer) > maxBearerLen {
		return false
	}
	return strings.IndexFunc(bearer, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

// ServeHTTP checks the request in a fixed order: where it was addressed,
// who sent it, what it carries, whether there is room for it, and only then
// its content. Nothing is counted for a request that did not authenticate,
// so an unauthenticated caller cannot write to the store at all.
func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if !h.addressed(req) {
		refuse(w, http.StatusForbidden)
		return
	}
	if !h.authorized(req) {
		refuse(w, http.StatusUnauthorized)
		return
	}
	received := h.now()
	if !jsonContent(req) {
		h.refuseCounted(w, req, http.StatusUnsupportedMediaType)
		return
	}
	select {
	case h.workers <- struct{}{}:
		defer func() { <-h.workers }()
	default:
		refuseBusy(w)
		return
	}
	if !h.limiter.allow(received) {
		refuseBusy(w)
		return
	}
	body, status := readBody(w, req)
	if status != 0 {
		h.refuseCounted(w, req, status)
		return
	}
	items, err := splitBody(body)
	if err != nil {
		h.refuseCounted(w, req, http.StatusBadRequest)
		return
	}
	if len(items) > MaxEvents {
		h.refuseCounted(w, req, http.StatusRequestEntityTooLarge)
		return
	}
	result, err := h.admit(items, received)
	if err != nil {
		refuse(w, http.StatusServiceUnavailable)
		return
	}
	if err := h.store(req.Context(), result); err != nil {
		refuse(w, http.StatusServiceUnavailable)
		return
	}
	respond(w, result.response())
}

// addressed reports whether the request was meant for this endpoint. The
// Host comparison stops DNS rebinding, and a browser always sends Origin on
// a cross-site POST, so refusing the header keeps web pages out.
func (h *handler) addressed(req *http.Request) bool {
	return req.Method == http.MethodPost &&
		req.Host == h.host &&
		req.URL.Path == EventsPath &&
		req.URL.RawQuery == "" &&
		len(req.Header.Values(headerOrigin)) == 0
}

// authorized compares digests rather than the header itself, so that the
// comparison takes the same time whatever the length of the presented value.
func (h *handler) authorized(req *http.Request) bool {
	values := req.Header.Values(headerAuthorization)
	if len(values) != 1 {
		return false
	}
	presented := sha256.Sum256([]byte(values[0]))
	return subtle.ConstantTimeCompare(presented[:], h.digest[:]) == 1
}

func jsonContent(req *http.Request) bool {
	media, _, err := mime.ParseMediaType(req.Header.Get(headerContentType))
	return err == nil && media == jsonMediaType
}

// readBody reads at most MaxBodyBytes. It returns a zero status on success
// and the status to refuse with otherwise.
func readBody(w http.ResponseWriter, req *http.Request) ([]byte, int) {
	if req.ContentLength > MaxBodyBytes {
		return nil, http.StatusRequestEntityTooLarge
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, MaxBodyBytes))
	if err == nil {
		return body, 0
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nil, http.StatusRequestEntityTooLarge
	}
	return nil, http.StatusBadRequest
}

// envelope tells the two body shapes apart. Events is nil for a single
// event, which has no such member.
type envelope struct {
	Events json.RawMessage `json:"events"`
}

// splitBody returns the undecoded events of a body: the members of "events"
// for a batch, the body itself for a single event. Events are decoded one by
// one later, so that a malformed one does not take the others with it.
func splitBody(body []byte) ([]json.RawMessage, error) {
	if !startsWith(body, jsonObjectStart) {
		return nil, errPayload
	}
	var outer envelope
	if err := json.Unmarshal(body, &outer); err != nil {
		return nil, fmt.Errorf("%w: %w", errPayload, err)
	}
	if outer.Events == nil {
		return []json.RawMessage{body}, nil
	}
	if !startsWith(outer.Events, jsonArrayStart) {
		return nil, errPayload
	}
	var items []json.RawMessage
	if err := json.Unmarshal(outer.Events, &items); err != nil {
		return nil, fmt.Errorf("%w: %w", errPayload, err)
	}
	return items, nil
}

// admission is the outcome of one request body.
type admission struct {
	// accepted counts every valid event, including repeats within the body.
	accepted int
	// events are the valid ones to store, each ID once.
	events   []usage.ExtEvent
	rejected []WireRejection
	// refused counts the rejected events by attributable source.
	refused map[string]int
}

func (a *admission) response() WireResponse {
	return WireResponse{Accepted: a.accepted, Rejected: a.rejected}
}

// admit validates every event of a body. It fails only when an event ID
// cannot be generated.
func (h *handler) admit(items []json.RawMessage, received time.Time) (*admission, error) {
	result := &admission{
		events:   make([]usage.ExtEvent, 0, len(items)),
		rejected: []WireRejection{},
		refused:  map[string]int{},
	}
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		wire, reason := decodeEvent(item)
		var valid admitted
		if reason == "" {
			valid, reason = validate(&wire, received)
		}
		if reason != "" {
			result.rejected = append(result.rejected, WireRejection{Index: index, Reason: string(reason)})
			result.refused[attributableSource(&wire)]++
			continue
		}
		id, err := eventID(valid.senderKey, valid.timestamped, &valid.event, h.random)
		if err != nil {
			return nil, err
		}
		result.accepted++
		// A sink is promised distinct IDs: the repeat of an event inside one
		// body is accepted like a re-delivery and stored once.
		if _, repeated := seen[id]; repeated {
			continue
		}
		seen[id] = struct{}{}
		valid.event.ID = id
		result.events = append(result.events, valid.event)
	}
	return result, nil
}

// store hands the accepted events to the sink in one call and then records
// the refusals, in source order so that the calls are reproducible.
func (h *handler) store(ctx context.Context, result *admission) error {
	ctx, cancel := context.WithTimeout(ctx, sinkTimeout)
	defer cancel()
	if len(result.events) > 0 {
		if err := h.sink.AppendExt(ctx, result.events); err != nil {
			return fmt.Errorf("extension: append events: %w", err)
		}
	}
	for _, source := range slices.Sorted(maps.Keys(result.refused)) {
		if err := h.sink.CountRejected(ctx, source, result.refused[source]); err != nil {
			return fmt.Errorf("extension: count rejected: %w", err)
		}
	}
	return nil
}

// refuseCounted refuses a whole authenticated request and records it as one
// unattributed rejection, so that it stays visible. When even that cannot be
// recorded the sender is told the endpoint is unavailable instead.
func (h *handler) refuseCounted(w http.ResponseWriter, req *http.Request, status int) {
	ctx, cancel := context.WithTimeout(req.Context(), sinkTimeout)
	defer cancel()
	if err := h.sink.CountRejected(ctx, unattributed, 1); err != nil {
		status = http.StatusServiceUnavailable
	}
	refuse(w, status)
}

func refuse(w http.ResponseWriter, status int) {
	http.Error(w, strings.ToLower(http.StatusText(status)), status)
}

func refuseBusy(w http.ResponseWriter) {
	w.Header().Set(headerRetryAfter, retryAfterSeconds)
	refuse(w, http.StatusTooManyRequests)
}

// respond writes the 202 body. The response is small and already complete
// in memory, so an encoding failure cannot leave a half-written body.
func respond(w http.ResponseWriter, body WireResponse) {
	encoded, err := json.Marshal(body)
	if err != nil {
		refuse(w, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set(headerContentType, jsonMediaType)
	w.WriteHeader(http.StatusAccepted)
	// A failed write means the sender is gone; there is nobody to tell.
	_, _ = w.Write(encoded)
}
