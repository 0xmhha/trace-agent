package readmodel

import (
	"sort"
	"strings"
	"time"
)

func (m *Model) measure(count, eligible int, value any, s Sample) row {
	token := m.opts.Cohort
	if m.opts.Token != nil && s.Metric != "" {
		token = m.opts.Token(s)
	}
	return row{"count": count, "eligible": eligible, "value": value, "cohort": token}
}
func (m *Model) count(n int, s Sample) row { return m.measure(n, n, n, s) }
func (m *Model) agentState(a agent) string {
	if a.state == "RUNNING" && !fresh(a.until, m.boundary()) {
		return "UNKNOWN"
	}
	return firstNon(a.state, "UNKNOWN")
}
func (m *Model) phase(a *attempt) string {
	if a.phase == "RUNNING" && !fresh(a.until, m.boundary()) {
		return "START_OBSERVED"
	}
	return a.phase
}
func (m *Model) matchesAgent(a agent) bool {
	switch m.opts.Sample.Metric {
	case "AGENTS_RUNNING":
		return m.agentState(a) == "RUNNING"
	case "AGENTS_ENDED":
		return m.agentState(a) == "ENDED"
	case "AGENTS_UNKNOWN":
		return m.agentState(a) == "UNKNOWN"
	case "AGENTS_CREATED_HERE":
		return a.created
	}
	return true
}
func duration(a *attempt, basis string) (float64, bool) {
	if basis == "REPORTED" {
		if a.reported != nil {
			return *a.reported, true
		}
		return 0, false
	}
	if a.start == nil {
		return 0, false
	}
	var clock string
	for _, e := range a.events {
		if e.Kind == "call" && e.Occurred != nil && *e.Occurred == *a.start {
			if e.Clock == "" || clock != "" && e.Clock != clock {
				return 0, false
			}
			clock = e.Clock
		}
	}
	if clock == "" {
		return 0, false
	}
	var end *int64
	for _, e := range a.events {
		if e.Kind == "result" && e.Clock == clock && e.Occurred != nil && *e.Occurred >= *a.start {
			end = minTime(end, e.Occurred)
		}
	}
	if end == nil {
		return 0, false
	}
	return float64(*end - *a.start), true
}
func (m *Model) matches(a *attempt, s Sample) bool {
	if s.TaskID != "" && s.TaskID != a.task || s.ModuleID != "" && s.ModuleID != a.module && s.ModuleID != a.server {
		return false
	}
	if s.FromMS != 0 || s.UntilMS != 0 {
		if a.start == nil || *a.start < s.FromMS || *a.start >= s.UntilMS {
			return false
		}
	}
	switch s.Metric {
	case "ATTEMPTS":
		return m.interval(a.start)
	case "WAITING":
		return m.interval(a.start) && m.phase(a) == "RUNNING"
	case "FAILURES":
		return m.interval(a.start) && strings.HasSuffix(a.result, "_ERROR")
	case "LATENCY":
		_, ok := duration(a, s.Basis)
		return ok && m.interval(a.start)
	}
	return true
}
func (m *Model) visibleTask(t *task) bool {
	if !allowed(m.opts.Tasks, t.id) || m.opts.Sample.TaskID != "" && m.opts.Sample.TaskID != t.id {
		return false
	}
	included := m.opts.From.IsZero() && m.opts.Until.IsZero()
	if !included {
		for _, e := range t.events {
			if m.interval(e.Occurred) {
				included = true
				break
			}
		}
	}
	if !included {
		return false
	}
	s := m.opts.Sample
	if strings.HasPrefix(s.Metric, "AGENTS") {
		for _, a := range t.agents {
			if m.matchesAgent(a) {
				return true
			}
		}
		return false
	}
	if s.Metric == "ATTEMPTS" || s.Metric == "WAITING" || s.Metric == "FAILURES" || s.Metric == "LATENCY" || s.ModuleID != "" {
		for _, a := range t.attempts {
			if m.matches(a, s) {
				return true
			}
		}
		return false
	}
	return true
}
func (m *Model) agentsCount(tasks []*task) row {
	counts := map[string]int{}
	for _, t := range tasks {
		for _, a := range t.agents {
			if !m.matchesAgent(a) {
				continue
			}
			counts["total"]++
			counts[m.agentState(a)]++
			if a.created {
				counts["createdHere"]++
			}
		}
	}
	result := row{}
	for k, metric := range map[string]string{"total": "AGENTS", "createdHere": "AGENTS_CREATED_HERE", "RUNNING": "AGENTS_RUNNING", "ENDED": "AGENTS_ENDED", "UNKNOWN": "AGENTS_UNKNOWN"} {
		s := Sample{Metric: metric}
		if len(tasks) == 1 {
			s.TaskID = tasks[0].id
		}
		result[strings.ToLower(k)] = m.count(counts[k], s)
	}
	result["createdHere"] = result["createdhere"]
	delete(result, "createdhere")
	return result
}

type stats struct {
	total, waiting, failed, eligible int
	observed, reported               []float64
}

func (s *stats) add(m *Model, a *attempt) {
	s.total++
	if m.phase(a) == "RUNNING" {
		s.waiting++
	}
	if strings.HasSuffix(a.result, "_ERROR") {
		s.failed++
		s.eligible++
	} else if a.result == "SUCCESS" {
		s.eligible++
	}
	if ms, ok := duration(a, "REPORTED"); ok {
		s.reported = append(s.reported, ms)
	}
	if ms, ok := duration(a, "OBSERVED_INTERVAL"); ok {
		s.observed = append(s.observed, ms)
	}
}
func (m *Model) measurements(s stats, scope Sample) row {
	measure := func(metric, basis string, n, d int, v any) row {
		c := scope
		c.Metric = metric
		c.Basis = basis
		return m.measure(n, d, v, c)
	}
	var ratio any
	if s.eligible > 0 {
		ratio = float64(s.failed) / float64(s.eligible)
	}
	p95 := func(values []float64, basis string) row {
		sort.Float64s(values)
		n := len(values)
		var v any
		if n > 0 {
			v = values[n-(n/20)-1]
		}
		return measure("LATENCY", basis, n, n, v)
	}
	obs := p95(s.observed, "OBSERVED_INTERVAL")
	reported := p95(s.reported, "REPORTED")
	return row{"attempts": measure("ATTEMPTS", "", s.total, s.total, s.total), "waiting": measure("WAITING", "", s.waiting, s.waiting, s.waiting), "failureRate": measure("FAILURES", "", s.failed, s.eligible, ratio), "latencyP95Ms": obs, "latencyByBasis": []row{{"basis": "OBSERVED_INTERVAL", "p95Ms": obs}, {"basis": "REPORTED", "p95Ms": reported}}}
}
func (m *Model) overview() row {
	var all stats
	var tasks []*task
	bins := map[int64]*stats{}
	from, until := m.opts.From.UnixMilli(), m.opts.Until.UnixMilli()
	size := max(int64(time.Minute/time.Millisecond), (until-from+119)/120)
	for _, t := range m.tasks {
		if !m.visibleTask(t) {
			continue
		}
		tasks = append(tasks, t)
		for _, a := range t.attempts {
			if !m.interval(a.start) || !m.matches(a, m.opts.Sample) {
				continue
			}
			all.add(m, a)
			if a.start != nil && until > from {
				index := (*a.start - from) / size
				if bins[index] == nil {
					bins[index] = &stats{}
				}
				bins[index].add(m, a)
			}
		}
	}
	out := m.measurements(all, Sample{})
	out["tasks"] = m.count(len(tasks), Sample{Metric: "ALL"})
	out["agents"] = m.agentsCount(tasks)
	var chart []row
	for i := int64(0); from+i*size < until; i++ {
		start := from + i*size
		end := min(start+size, until)
		b := bins[i]
		if b == nil {
			b = &stats{}
		}
		v := m.measurements(*b, Sample{FromMS: start, UntilMS: end})
		for _, pair := range [][2]string{{"ATTEMPT_COUNT", "attempts"}, {"FAILURE_RATE", "failureRate"}, {"LATENCY_P95_MS", "latencyP95Ms"}} {
			chart = append(chart, row{"kind": pair[0], "from": msTime(&start), "until": msTime(&end), "measure": v[pair[1]]})
		}
	}
	out["bins"] = chart
	return out
}
