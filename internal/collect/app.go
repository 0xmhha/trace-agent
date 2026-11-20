package collect

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
)

// ItemVersion tracks the latest snapshot and the most recent lifecycle reports.
type ItemVersion struct {
	Sequence                    uint64
	Current, Started, Completed string
}

// AppSink supplies durable version lookup and commits before acknowledging.
// Serialize one app-server collection connection per source.
type AppSink interface {
	Latest(ctx context.Context, recordKey string) (ItemVersion, error)
	Commit(ctx context.Context, e Evidence, v ItemVersion) error
}

type itemEvent struct {
	e     Evidence
	phase string
}

func (a *Adapter) item(thread, turn string, raw object, method string) (itemEvent, error) {
	id := str(raw, "id")
	if thread == "" || turn == "" || id == "" {
		return itemEvent{}, errInput
	}
	typ := choice(raw, "type", "userMessage", "agentMessage", "mcpToolCall", "commandExecution", "collabAgentToolCall", "dynamicToolCall", "plan", "reasoning", "fileChange", "webSearch")
	if typ == "" {
		typ = "unsupported"
	}
	m := object{"type": typ}
	if typ == "mcpToolCall" {
		copyBool(m, obj(raw["result"]), "isError")
	}
	if typ == "commandExecution" {
		if code, ok := raw["exitCode"].(float64); ok {
			m["exitCode"] = code
		}
	}
	a.ids(m, raw, "id", "senderThreadId", "model")
	a.ids(m, object{"threadId": thread, "turnId": turn}, "threadId", "turnId")
	status := choice(raw, "status", "inProgress", "completed", "failed", "declined", "cancelled")
	if status != "" {
		m["status"] = status
	}
	if typ == "mcpToolCall" || typ == "dynamicToolCall" || typ == "collabAgentToolCall" {
		m["tool"] = a.tool(str(raw, "tool"))
		a.ids(m, raw, "server")
	}
	if typ == "collabAgentToolCall" {
		receivers, _ := raw["receiverThreadIds"].([]any)
		ids := make([]string, 0, len(receivers))
		for _, v := range receivers {
			if s, ok := v.(string); ok && s != "" {
				ids = append(ids, a.id(s))
			}
		}
		m["receiverThreadIds"] = ids
		states := object{}
		for child, v := range obj(raw["agentsStates"]) {
			if state := choice(obj(v), "status", "pendingInit", "running", "completed", "errored", "shutdown", "notFound"); state != "" {
				states[a.id(child)] = state
			}
		}
		m["agentsStates"] = states
	}
	if n, ok := raw["durationMs"].(float64); ok && n >= 0 {
		m["durationMs"] = n
	}
	phase := "observed"
	switch {
	case method == "item/started":
		phase = "started"
	case method == "item/completed":
		phase = "completed"
	case status == "inProgress":
		phase = "started"
	case status != "":
		phase = "completed"
	}
	pos := Position{Stream: thread, Generation: "app-v2"}
	e := a.evidence(pos, m, nil)
	e.RecordKey = a.token("item", e.Source, thread, turn, id)
	return itemEvent{e: e, phase: phase}, nil
}

// A new snapshot can legitimately return to an older value. A repeated lifecycle
// notification, however, has its own phase identity and can arrive late.
func (event itemEvent) accept(ctx context.Context, sink AppSink, notification bool) error {
	prev, err := sink.Latest(ctx, event.e.RecordKey)
	if err != nil {
		return err
	}
	tag := event.phase + "/" + event.e.SemanticTag
	if prev.Current == tag || notification && ((event.phase == "started" && prev.Started == tag) || (event.phase == "completed" && prev.Completed == tag)) {
		return nil
	}
	v := prev
	lateStart := notification && event.phase == "started" && prev.Completed != ""
	if !lateStart {
		v.Current = tag
	}
	switch event.phase {
	case "started":
		v.Started = tag
	case "completed":
		v.Completed = tag
	}
	v.Sequence++
	event.e.VersionKey = event.phase + "/" + strconv.FormatUint(v.Sequence, 10)
	return sink.Commit(ctx, event.e, v)
}

// Native row timestamps can be null. An observed history item alone is not proof
// of completion; prefer a reported endpoint to an inferred status.
func historyMethod(row object) string {
	if _, ok := row["completedAtMs"].(float64); ok {
		return "item/completed"
	}
	if str(obj(row["item"]), "status") == "" {
		if _, ok := row["startedAtMs"].(float64); ok {
			return "item/started"
		}
	}
	return ""
}

type notification struct {
	Method string
	Params json.RawMessage
}

func (c *appConnection) notice(ctx context.Context, n notification) error {
	if !slices.Contains([]string{"thread/started", "item/started", "item/completed", "turn/started", "turn/completed", "turn/plan/updated"}, n.Method) {
		return nil
	}
	var p object
	if json.Unmarshal(n.Params, &p) != nil {
		return errInput
	}
	if n.Method == "thread/started" {
		thread := obj(p["thread"])
		id := str(thread, "id")
		if id != "" && str(thread, "cwd") == c.directory {
			c.selected[id] = true
			return c.thread(ctx, thread, false)
		}
		return nil
	}
	if !c.selected[str(p, "threadId")] {
		return nil
	}
	switch n.Method {
	case "item/started", "item/completed":

		event, err := c.adapter.item(str(p, "threadId"), str(p, "turnId"), obj(p["item"]), n.Method)
		if err != nil {
			return err
		}
		c.mark(event.e.RecordKey, event.phase)
		event.e.OccurredMS = itemTime(p, event.phase)
		return event.accept(ctx, c.sink, true)
	case "turn/started", "turn/completed":
		return c.turn(ctx, str(p, "threadId"), obj(p["turn"]), n.Method, false)
	case "turn/plan/updated":
		m := c.adapter.plan(object{"method": n.Method, "params": p})
		if str(p, "turnId") == "" {
			return errInput
		}
		c.sequence++
		e := c.adapter.evidence(Position{Stream: "notifications", Generation: c.generation, Offset: c.sequence}, m, nil)
		return c.sink.Commit(ctx, e, ItemVersion{Sequence: 1, Current: e.SemanticTag})
	}
	return nil
}

var errMethodUnavailable = errors.New("read method unavailable")

func itemTime(row object, phase string) *int64 {
	field := "startedAtMs"
	if phase == "completed" {
		field = "completedAtMs"
	}
	if n, ok := row[field].(float64); ok && n >= 0 && n < 1<<53 {
		ms := int64(n)
		return &ms
	}
	return nil
}

func (c *appConnection) thread(ctx context.Context, thread object, history bool) error {
	id := str(thread, "id")
	m := object{"type": "thread"}
	c.adapter.ids(m, thread, "id", "sessionId", "parentThreadId", "projectId", "model")
	e := c.adapter.evidence(Position{Stream: id, Generation: "app-v2"}, m, nil)
	if version := str(thread, "cliVersion"); version != "" {
		e.ClientVersion = numericVersion(version)
	}
	e.RecordKey = c.adapter.token("thread", e.Source, id)
	if history && c.reported(e.RecordKey) {
		return nil
	}
	if !history {
		c.mark(e.RecordKey, "observed")
	}
	return (itemEvent{e, "observed"}).accept(ctx, c.sink, !history)
}
func (c *appConnection) turn(ctx context.Context, thread string, turn object, method string, history bool) error {
	id := str(turn, "id")
	if id == "" {
		return errInput
	}
	m := object{"type": "turn"}
	c.adapter.ids(m, object{"threadId": thread, "turnId": id}, "threadId", "turnId")
	c.adapter.ids(m, turn, "rootTurnId")
	status := choice(turn, "status", "completed", "interrupted", "failed", "inProgress")
	if status != "" {
		m["status"] = status
	}
	phase := "observed"
	switch {
	case method == "turn/started" || status == "inProgress":
		phase = "started"
	case method == "turn/completed" || status != "":
		phase = "completed"
	}
	e := c.adapter.evidence(Position{Stream: thread, Generation: "app-v2"}, m, nil)
	e.RecordKey = c.adapter.token("turn", e.Source, thread, id)
	if history && c.reported(e.RecordKey+phase) {
		return nil
	}
	if !history {
		c.mark(e.RecordKey, phase)
	}
	return (itemEvent{e, phase}).accept(ctx, c.sink, !history || c.reported(e.RecordKey))
}
