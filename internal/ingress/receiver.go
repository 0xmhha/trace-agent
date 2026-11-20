// Package ingress receives authenticated OTLP/HTTP logs on loopback.
package ingress

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	maxPayload = 4 << 20
	maxRecords = 1024
	protoType  = "application/x-protobuf"
	jsonType   = "application/json"
)

// Sink reads consent and makes at most 100 projected records durable per commit.
type Sink interface {
	Registered(context.Context, string) (bool, error)
	SourceConfig(context.Context, string) (store.SourceConfig, error)
	AppendBatch(context.Context, []collect.Evidence) error
}

// Receiver owns a dedicated source credential and a single loopback listener.
type Receiver struct {
	db         Sink
	a          *collect.Adapter
	credential [32]byte
	host       string
	port       int
	http       *http.Server
	slot       chan struct{}
}

// Token issues a URL-safe capability for an installed OTLP exporter.
func Token() (string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(secret[:]), nil
}

// Listen starts a source-specific receiver. Pass zero to allocate a port, then
// save Port and the token in the native credential store for exporter restarts.
func Listen(db Sink, a *collect.Adapter, port int, token string) (*Receiver, error) {
	if db == nil || a == nil || (a.Kind() != collect.Telemetry && a.Kind() != collect.CodexTelemetry) || port < 0 || port > 65535 || len(token) < 32 || len(token) > 256 || strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return nil, errors.New("invalid OTLP receiver configuration")
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	r := &Receiver{db: db, a: a, host: ln.Addr().String(), port: ln.Addr().(*net.TCPAddr).Port, credential: sha256.Sum256([]byte("Bearer " + token)), slot: make(chan struct{}, 1)}
	r.http = &http.Server{Handler: r, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 8 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 20 * time.Second, MaxHeaderBytes: 8192}
	go func() { _ = r.http.Serve(ln) }()
	return r, nil
}

// URL returns the complete OTLP logs endpoint.
func (r *Receiver) URL() string { return "http://" + r.host + "/v1/logs" }

// Port returns the allocated loopback port.
func (r *Receiver) Port() int { return r.port }

// Close drains an active exporter request.
func (r *Receiver) Close(ctx context.Context) error { return r.http.Shutdown(ctx) }

var errSize = errors.New("export exceeds receiver limits")

func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost || req.Host != r.host || req.URL.Path != "/v1/logs" || req.URL.RawQuery != "" || len(req.Header.Values("Origin")) != 0 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	credential := sha256.Sum256([]byte(req.Header.Get("Authorization")))
	if len(req.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare(credential[:], r.credential[:]) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	media, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || (media != jsonType && media != protoType) {
		http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
		return
	}
	select {
	case r.slot <- struct{}{}:
		defer func() { <-r.slot }()
	default:
		w.Header().Set("Retry-After", "1")
		fail(w, media, 429, 8, "receiver busy")
		return
	}
	exists, err := r.db.Registered(req.Context(), r.a.Source())
	if err != nil {
		fail(w, media, 503, 14, "source unavailable")
		return
	}
	if !exists {
		fail(w, media, 403, 7, "source not attached")
		return
	}
	cfg, err := r.db.SourceConfig(req.Context(), r.a.Source())
	if err != nil {
		fail(w, media, 503, 14, "source unavailable")
		return
	}
	if !cfg.Enabled {
		fail(w, media, 403, 7, "source disabled")
		return
	}
	payload, err := readBody(w, req)
	if err != nil {
		if errors.Is(err, errSize) {
			fail(w, media, 413, 8, "export exceeds receiver limits")
		} else {
			fail(w, media, 400, 3, "invalid export")
		}
		return
	}
	records, rejected, err := project(payload, media, cfg.Consent, r.a)
	if err != nil {
		if errors.Is(err, errSize) {
			fail(w, media, 413, 8, "export exceeds receiver limits")
		} else {
			fail(w, media, 400, 3, "invalid export")
		}
		return
	}
	for start := 0; start < len(records); start += 100 {
		if err = r.db.AppendBatch(req.Context(), records[start:min(start+100, len(records))]); err != nil {
			if errors.Is(err, store.ErrDisabled) {
				fail(w, media, 403, 7, "source disabled")
				return
			}
			w.Header().Set("Retry-After", "1")
			fail(w, media, 503, 14, "commit unavailable")
			return
		}
	}
	if len(records) > 0 {
		if h, ok := r.db.(interface {
			ReportHealth(context.Context, *collect.Adapter, string) error
		}); ok {
			if err = h.ReportHealth(req.Context(), r.a, "connected"); err != nil {
				fail(w, media, 503, 14, "health unavailable")
				return
			}
		}
	}
	writeSuccess(w, media, rejected)
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.ContentLength > maxPayload {
		return nil, errSize
	}
	raw := http.MaxBytesReader(w, r.Body, maxPayload)
	defer func() { _ = raw.Close() }()
	var reader io.Reader = raw
	switch r.Header.Get("Content-Encoding") {
	case "", "identity":
	case "gzip":
		z, err := gzip.NewReader(raw)
		if err != nil {
			return nil, err
		}
		defer func() { _ = z.Close() }()
		reader = z
	default:
		return nil, errors.New("unsupported encoding")
	}
	b, err := io.ReadAll(io.LimitReader(reader, maxPayload+1))
	var maxErr *http.MaxBytesError
	if len(b) > maxPayload || errors.As(err, &maxErr) {
		return nil, errSize
	}
	return b, err
}

func pbInt(field protowire.Number, value int) []byte {
	if value < 0 {
		return nil
	}
	return protowire.AppendVarint(protowire.AppendTag(nil, field, protowire.VarintType), uint64(value))
}
func pbString(field protowire.Number, s string) []byte {
	return protowire.AppendString(protowire.AppendTag(nil, field, protowire.BytesType), s)
}

func fail(w http.ResponseWriter, media string, status, code int, message string) {
	w.Header().Set("Content-Type", media)
	w.WriteHeader(status)
	if media == jsonType {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": message})
		return
	}
	_, _ = w.Write(append(pbInt(1, code), pbString(2, message)...))
}
func writeSuccess(w http.ResponseWriter, media string, rejected int) {
	w.Header().Set("Content-Type", media)
	if rejected == 0 {
		if media == jsonType {
			_, _ = io.WriteString(w, "{}")
		}
		return
	}
	const msg = "missing stable event identity"
	if media == jsonType {
		_ = json.NewEncoder(w).Encode(map[string]any{"partialSuccess": map[string]any{"rejectedLogRecords": strconv.Itoa(rejected), "errorMessage": msg}})
		return
	}
	partial := append(pbInt(1, rejected), pbString(2, msg)...)
	_, _ = w.Write(protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), partial))
}
