package collect

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// AppOptions limits thread discovery to the selected working directory.
// Follow keeps reading notifications on this same connection after catch-up.
type AppOptions struct {
	Directory string
	Follow    bool
	// PollInterval sets the delay between serial history refreshes in Follow mode.
	PollInterval time.Duration
}

type appConnection struct {
	adapter           *Adapter
	sink              AppSink
	conn              io.ReadWriteCloser
	inbox             <-chan frame
	heads             map[string]string
	request, sequence int64
	generation        string
	touched           []map[string]bool
	selected          map[string]bool
	directory         string
}

// CollectApp takes ownership of a dedicated app-server JSONL connection. It sends
// only initialization and read methods. Closing the transport must interrupt I/O.
// The caller chooses the local profile/instance when opening the connection.
func (a *Adapter) CollectApp(ctx context.Context, conn io.ReadWriteCloser, opts AppOptions, sink AppSink) error {
	if conn == nil {
		return errInput
	}
	closeConn := sync.OnceFunc(func() { _ = conn.Close() })
	defer closeConn()
	if a.kind != AppServer || opts.Directory == "" || sink == nil || opts.PollInterval < 0 {
		return errInput
	}
	stop := context.AfterFunc(ctx, closeConn)
	defer stop()
	pumpCtx, cancel := context.WithCancel(ctx)
	inbox := make(chan frame)
	done := make(chan struct{})
	go func() { defer close(done); readFrames(pumpCtx, conn, inbox) }()
	defer func() { cancel(); closeConn(); <-done }()
	c := &appConnection{adapter: a, sink: sink, conn: conn, inbox: inbox, heads: make(map[string]string), generation: rand.Text(), selected: make(map[string]bool), directory: opts.Directory}
	_, err := c.call(ctx, "initialize", object{"clientInfo": object{"name": "trace_agent", "version": Version}, "capabilities": object{"experimentalApi": true}})
	if err != nil {
		return err
	}
	if err = c.send(object{"method": "initialized"}); err != nil {
		return err
	}
	if err := c.scan(ctx); err != nil || !opts.Follow {
		return err
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = 10 * time.Second
	}
	timer := time.NewTimer(opts.PollInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case reply := <-c.inbox:
			if reply.err != nil {
				return reply.err
			}
			if err := c.notice(ctx, reply.msg.notification); err != nil {
				return err
			}
		case <-timer.C:
			if err := c.scan(ctx); err != nil {
				return err
			}
			timer.Reset(opts.PollInterval)
		}
	}
}

func (c *appConnection) scan(ctx context.Context) error {
	for _, archived := range []bool{false, true} {
		params := object{"cwd": c.directory, "archived": archived, "sourceKinds": []string{"cli", "vscode", "exec", "appServer", "subAgent", "subAgentReview", "subAgentCompact", "subAgentThreadSpawn", "subAgentOther", "unknown"}, "modelProviders": []string{}}
		err := c.pages(ctx, "thread/list", params, func(thread object) error {
			id := str(thread, "id")
			if id == "" {
				return errInput
			}
			if str(thread, "cwd") != c.directory {
				delete(c.selected, id)
				return nil
			}
			c.selected[id] = true
			if err := c.thread(ctx, thread, true); err != nil {
				return err
			}
			return c.turns(ctx, id)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *appConnection) send(msg object) error {
	if err := json.NewEncoder(c.conn).Encode(msg); err != nil {
		return errors.New("app-server write failed")
	}
	return nil
}

type rpcMessage struct {
	notification
	ID     json.RawMessage
	Result json.RawMessage
	Error  *struct{ Code int }
}

type frame struct {
	msg rpcMessage
	err error
}

// One unbuffered handoff keeps source reads bounded while allowing an idle
// connection to wake for the next poll. Closing the transport releases a blocked read.
func readFrames(ctx context.Context, conn io.Reader, out chan<- frame) {
	reader := bufio.NewReader(conn)
	for {
		line, err := completeLine(reader)
		var msg rpcMessage
		if err == nil && (!utf8.Valid(line) || json.Unmarshal(line, &msg) != nil) {
			err = errInput
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		select {
		case <-ctx.Done():
			return
		case out <- frame{msg, err}:
		}
		if err != nil {
			return
		}
	}
}
func (c *appConnection) read(ctx context.Context) (rpcMessage, error) {
	select {
	case <-ctx.Done():
		return rpcMessage{}, ctx.Err()
	case reply := <-c.inbox:
		return reply.msg, reply.err
	}
}

func (c *appConnection) call(ctx context.Context, method string, params object) (json.RawMessage, error) {
	c.request++
	if err := c.send(object{"id": c.request, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		msg, err := c.read(ctx)
		if err != nil {
			return nil, err
		}
		if len(msg.ID) == 0 {
			if err = c.notice(ctx, msg.notification); err != nil {
				return nil, err
			}
			continue
		}
		if string(msg.ID) != strconv.FormatInt(c.request, 10) || msg.Method != "" {
			return nil, errors.New("unexpected app-server response")
		}
		if msg.Error != nil {
			if msg.Error.Code == -32601 {
				return nil, errMethodUnavailable
			}
			return nil, errors.New("app-server read request failed")
		}
		return msg.Result, nil
	}
}

// Each nested page request owns its cursor and the set of items updated while it was in flight.
func (c *appConnection) pages(ctx context.Context, method string, params object, visit func(object) error) error {
	outer := len(c.touched)
	defer func() { c.touched = c.touched[:outer] }()
	seen := make(map[string]bool)
	for {
		c.touched = append(c.touched[:outer], make(map[string]bool))
		raw, err := c.call(ctx, method, params)
		if err != nil {
			return err
		}
		var page struct {
			Data       []object
			NextCursor string
		}
		if json.Unmarshal(raw, &page) != nil || page.Data == nil {
			return errInput
		}
		for _, r := range page.Data {
			if err = visit(r); err != nil {
				return err
			}
		}
		c.touched = c.touched[:outer]
		if page.NextCursor == "" {
			return nil
		}
		if seen[page.NextCursor] {
			return errors.New("repeated history cursor")
		}
		seen[page.NextCursor] = true
		params["cursor"] = page.NextCursor
	}
}
func (c *appConnection) turns(ctx context.Context, thread string) error {
	previousHead := c.heads[thread]
	var nextHead string
	fallback := false
	// Request full items only if the separate items endpoint is unavailable.
	scan := func(view string) error {
		return c.pages(ctx, "thread/turns/list", object{"threadId": thread, "itemsView": view, "sortDirection": "desc"}, func(turn object) error {
			id := str(turn, "id")
			if id == "" {
				return errInput
			}
			if nextHead == "" {
				nextHead = id
			}
			if err := c.turn(ctx, thread, turn, "", true); err != nil {
				return err
			}
			if fallback {
				items, ok := turn["items"].([]any)
				if !ok {
					return errInput
				}
				for _, raw := range items {
					if err := c.history(ctx, thread, id, object{"item": raw}); err != nil {
						return err
					}
				}
				if id == previousHead {
					return errCaughtUp
				}
				return nil
			}
			err := c.pages(ctx, "thread/items/list", object{"threadId": thread, "turnId": id}, func(row object) error {
				if str(row, "turnId") != id {
					return errInput
				}
				return c.history(ctx, thread, id, row)
			})
			if err == nil && id == previousHead {
				return errCaughtUp
			}
			return err
		})
	}
	err := scan("notLoaded")
	if errors.Is(err, errMethodUnavailable) {
		fallback = true
		err = scan("full")
	}
	if err == nil || errors.Is(err, errCaughtUp) {
		if nextHead != "" {
			c.heads[thread] = nextHead
		}
		return nil
	}
	return err
}

var errCaughtUp = errors.New("previous turn reached")

func (c *appConnection) history(ctx context.Context, thread, turn string, row object) error {
	e, err := c.adapter.item(thread, turn, obj(row["item"]), historyMethod(row))
	if err != nil {
		return err
	}
	if c.reported(e.e.RecordKey + e.phase) {
		return nil
	}
	e.e.OccurredMS = itemTime(row, e.phase)
	return e.accept(ctx, c.sink, c.reported(e.e.RecordKey))
}

// A turn list may still have unvisited rows while its nested item request is
// waiting. A live report supersedes stale rows in every enclosing open page.
func (c *appConnection) mark(record, phase string) {
	for _, page := range c.touched {
		page[record] = true
		page[record+phase] = true
	}
}
func (c *appConnection) reported(key string) bool {
	return len(c.touched) > 0 && c.touched[len(c.touched)-1][key]
}
