package transport

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/0xmhha/trace-agent/internal/store"
	"github.com/gorilla/websocket"
)

const protocol = "graphql-transport-ws"
const maxFrame = 1 << 20
const tick = 100 * time.Millisecond

// Backend supplies point-in-time reads and the privacy barrier shared with writes.
type Backend interface {
	Point(context.Context) (store.Point, error)
	Records(context.Context, store.Point) ([]store.Record, error)
	CommittedAt(context.Context, int64) (time.Time, error)
	WithEpoch(context.Context, int64, func() error) error
}

// PickerAction is a request to consume a single-use OS picker capability.
type PickerAction struct {
	Operation, PickerToken, SourceID, Client string
	RetentionDays                            int
	From, Until                              time.Time
}

// Picker applies the requested source operation after consuming the OS capability.
type Picker func(context.Context, PickerAction) (store.Point, error)
type ticket struct {
	manage bool
	until  time.Time
}
type receipt struct {
	f       store.Filter
	preview store.Preview
	until   time.Time
}

// Server owns the loopback listener, native ticket issuer and active sessions.
type Server struct {
	db        Backend
	cat       *catalog
	key       []byte
	origin    string
	slots     chan struct{}
	mu        sync.Mutex
	tickets   map[string]ticket
	peers     map[*peer]bool
	picker    Picker
	http      *http.Server
	done      bool
	statusSeq atomic.Uint64
	cache     modelCache
	sessions  sync.WaitGroup
}

// New prepares the schema-validated desktop operation catalog.
func New(db Backend, origin string) (*Server, error) {
	u, err := url.Parse(origin)
	if db == nil || err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid desktop origin or backend")
	}
	c, err := loadCatalog()
	if err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	return &Server{db: db, cat: c, key: key, origin: origin, slots: make(chan struct{}, 8), tickets: map[string]ticket{}, peers: map[*peer]bool{}}, nil
}

// SetPicker installs the native source-registration callback.
func (s *Server) SetPicker(fn Picker) { s.mu.Lock(); s.picker = fn; s.mu.Unlock() }

// Ticket issues a short-lived connection capability. The caller is the native bridge.
func (s *Server) Ticket(manage bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.tickets {
		if !v.until.After(time.Now()) {
			delete(s.tickets, k)
		}
	}
	if s.done || len(s.tickets) >= 64 {
		return "", errors.New("ticket issuer unavailable")
	}
	t := rand.Text()
	s.tickets[t] = ticket{manage, time.Now().Add(30 * time.Second)}
	return t, nil
}
func (s *Server) consume(t string) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.tickets[t]
	delete(s.tickets, t)
	return v.manage, ok && v.until.After(time.Now()) && !s.done
}

// Listen binds a fresh loopback port and returns its desktop WebSocket URL.
func (s *Server) Listen() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.http != nil || s.done {
		return "", errors.New("listener already started or closed")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	host := ln.Addr().String()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host || r.URL.Path != "/graphql" || r.URL.RawQuery != "" || r.Method != http.MethodGet || r.Header.Get("Origin") != s.origin || !slices.Contains(websocket.Subprotocols(r), protocol) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		default:
			http.Error(w, "too many connections", http.StatusTooManyRequests)
			return
		}
		up := websocket.Upgrader{Subprotocols: []string{protocol}, HandshakeTimeout: 3 * time.Second, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == s.origin }}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		p := &peer{s: s, ws: ws, ctx: ctx, cancel: cancel, active: map[string]*job{}, receipts: map[string]receipt{}}
		s.mu.Lock()
		if s.done {
			s.mu.Unlock()
			cancel()
			_ = ws.Close()
			return
		}
		s.sessions.Add(1)
		s.peers[p] = true
		s.mu.Unlock()
		defer s.sessions.Done()
		defer func() { p.close(); p.wg.Wait(); s.mu.Lock(); delete(s.peers, p); s.mu.Unlock() }()
		p.serve()
	})
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	s.http = srv
	go func() { _ = srv.Serve(ln) }()
	return "ws://" + host + "/graphql", nil
}

// Close stops admitting sessions and cancels pending operations.
func (s *Server) Close(ctx context.Context) error {
	s.mu.Lock()
	s.done = true
	clear(s.tickets)
	srv := s.http
	for p := range s.peers {
		p.close()
	}
	s.mu.Unlock()
	if srv != nil {
		if err := srv.Shutdown(ctx); err != nil {
			return err
		}
		done := make(chan struct{})
		go func() { s.sessions.Wait(); close(done) }()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
		}
	}
	return nil
}

type envelope struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}
type inputFrame struct {
	ID, Type string
	Payload  json.RawMessage
}
type job struct{ cancel context.CancelFunc }
type peer struct {
	s            *Server
	ws           *websocket.Conn
	ctx          context.Context
	cancel       context.CancelFunc
	write        sync.Mutex
	mu           sync.Mutex
	wg           sync.WaitGroup
	active       map[string]*job
	manage       bool
	acknowledged int64
	displayed    store.Point
	receipts     map[string]receipt
}

func (p *peer) close() { p.cancel(); _ = p.ws.Close() }
func (p *peer) reject(code int) {
	_ = p.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, "connection rejected"), time.Now().Add(time.Second))
	p.close()
}
func (p *peer) send(ctx context.Context, msg envelope, epoch int64) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(raw) > maxFrame {
		return errOverflow
	}
	p.write.Lock()
	defer p.write.Unlock()
	write := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_ = p.ws.SetWriteDeadline(time.Now().Add(time.Second))
		return p.ws.WriteMessage(websocket.TextMessage, raw)
	}
	if epoch != 0 {
		return p.s.db.WithEpoch(ctx, epoch, write)
	}
	return write()
}

var errOverflow = errors.New("frame exceeds output limit")

func (p *peer) serve() {
	p.ws.SetReadLimit(65536)
	_ = p.ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var first inputFrame
	if err := p.ws.ReadJSON(&first); err != nil {
		return
	}
	var creds struct{ Ticket string }
	if first.Type != "connection_init" || json.Unmarshal(first.Payload, &creds) != nil {
		p.reject(4401)
		return
	}
	manage, ok := p.s.consume(creds.Ticket)
	if !ok {
		p.reject(4403)
		return
	}
	p.manage = manage
	point, err := p.s.db.Point(p.ctx)
	if err != nil {
		return
	}
	p.acknowledged = point.Epoch
	if err = p.send(p.ctx, envelope{Type: "connection_ack"}, 0); err != nil {
		return
	}
	_ = p.ws.SetReadDeadline(time.Time{})
	p.wg.Add(1)
	go func() { defer p.wg.Done(); p.monitorEpoch() }()
	for {
		var in inputFrame
		if err := p.ws.ReadJSON(&in); err != nil {
			return
		}
		switch in.Type {
		case "ping":
			if p.send(p.ctx, envelope{Type: "pong"}, 0) != nil {
				return
			}
		case "pong":
		case "connection_init":
			p.reject(4429)
			return
		case "complete":
			p.mu.Lock()
			if j := p.active[in.ID]; j != nil {
				j.cancel()
				delete(p.active, in.ID)
			}
			p.mu.Unlock()
		case "subscribe":
			if in.ID == "" || len(in.ID) > 80 {
				p.reject(4400)
				return
			}
			p.mu.Lock()
			if _, exists := p.active[in.ID]; exists {
				p.mu.Unlock()
				p.reject(4409)
				return
			}
			if len(p.active) >= 16 {
				p.mu.Unlock()
				p.reject(4429)
				return
			}
			ctx, cancel := context.WithCancel(p.ctx)
			j := &job{cancel}
			p.active[in.ID] = j
			p.mu.Unlock()
			p.wg.Add(1)
			go func() { defer p.wg.Done(); defer cancel(); p.run(ctx, in, j) }()
		default:
			p.reject(4400)
			return
		}
	}
}
func (p *peer) monitorEpoch() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var stale time.Time
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-t.C:
			point, err := p.s.db.Point(p.ctx)
			if err != nil {
				p.close()
				return
			}
			if p.isAck(point.Epoch) {
				stale = time.Time{}
				continue
			}
			p.s.discardBefore(point.Epoch)
			if stale.IsZero() {
				stale = time.Now()
			} else if time.Since(stale) > 5*time.Second {
				p.reject(4403)
				return
			}
		}
	}
}
func (p *peer) isAck(epoch int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.acknowledged == epoch
}
func (p *peer) run(ctx context.Context, in inputFrame, j *job) {
	var request struct {
		Query, OperationName string
		Variables            object
	}
	var err error
	if json.Unmarshal(in.Payload, &request) != nil {
		err = errVariables
	} else {
		var op operation
		var vars object
		op, vars, err = p.s.cat.resolve(request.Query, request.OperationName, request.Variables)
		if err == nil {
			err = p.execute(ctx, in.ID, op, vars)
		}
	}
	// Serializing completion with the operation map makes immediate ID reuse safe.
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active[in.ID] != j {
		return
	}
	delete(p.active, in.ID)
	msg := envelope{ID: in.ID, Type: "complete"}
	if err != nil {
		msg.Type = "error"
		msg.Payload = []object{{"message": "operation failed", "extensions": object{"code": code(err)}}}
	}
	_ = p.send(ctx, msg, 0)
}
func code(err error) string {
	switch {
	case errors.Is(err, errOverflow):
		return "OVERFLOW"
	case errors.Is(err, errScope):
		return "INVALID_SCOPE"
	case errors.Is(err, errAuth):
		return "FORBIDDEN"
	case errors.Is(err, errVariables):
		return "BAD_INPUT"
	case errors.Is(err, errMissing):
		return "NOT_FOUND"
	default:
		return "REQUEST_FAILED"
	}
}

var errAuth = errors.New("permission denied")
var errMissing = errors.New("requested data outside scope")
