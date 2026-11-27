-- Usage store v1. Numbers and metadata only: no message text, no tool
-- arguments, no tool output. Every row except ext_event can be rebuilt from
-- the original transcripts, so the file may be deleted to reset the pipeline.
-- Open applies this file and sets user_version in one transaction.

-- Keys: consent, window_days, ext_enabled, ext_port.
CREATE TABLE setting (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

-- Bumped in the same transaction as any change the UI should refetch for.
CREATE TABLE data_version (
    singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
    n INTEGER NOT NULL
) STRICT;
INSERT INTO data_version(singleton, n) VALUES(1, 0);

CREATE TABLE project (
    project_key TEXT PRIMARY KEY,
    path TEXT NOT NULL DEFAULT '',
    excluded INTEGER NOT NULL DEFAULT 0 CHECK(excluded IN (0,1))
) STRICT;

-- Read position of one transcript file. path is relative to the watched root.
-- skipped counts lines passed over as malformed or oversized; unreadable marks
-- a file the last scan could not open or read.
CREATE TABLE file (
    path TEXT PRIMARY KEY,
    project_key TEXT NOT NULL,
    offset INTEGER NOT NULL CHECK(offset >= 0),
    size INTEGER NOT NULL,
    mod_ms INTEGER NOT NULL,
    skipped INTEGER NOT NULL DEFAULT 0,
    unreadable INTEGER NOT NULL DEFAULT 0 CHECK(unreadable IN (0,1)),
    scanned_ms INTEGER NOT NULL
) STRICT;
CREATE INDEX file_by_project ON file(project_key);

-- One transcript stream: the root agent (agent_id = '') or one sub-agent.
-- first_ms and last_ms are 0 until a timestamped line has been read.
CREATE TABLE stream (
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL DEFAULT '',
    project_key TEXT NOT NULL,
    file_path TEXT NOT NULL,
    agent_type TEXT NOT NULL DEFAULT '',
    project_path TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    entrypoint TEXT NOT NULL DEFAULT '',
    client_version TEXT NOT NULL DEFAULT '',
    git_branch TEXT NOT NULL DEFAULT '',
    first_ms INTEGER NOT NULL,
    last_ms INTEGER NOT NULL,
    PRIMARY KEY(session_id, agent_id)
) STRICT;
CREATE INDEX stream_by_project ON stream(project_key, last_ms);
CREATE INDEX stream_by_file ON stream(file_path);

CREATE TABLE model_call (
    session_id TEXT NOT NULL,
    agent_id TEXT NOT NULL DEFAULT '',
    key TEXT NOT NULL,
    project_key TEXT NOT NULL,
    at_ms INTEGER NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    input INTEGER NOT NULL DEFAULT 0,
    output INTEGER NOT NULL DEFAULT 0,
    cache_read INTEGER NOT NULL DEFAULT 0,
    cache_create INTEGER NOT NULL DEFAULT 0,
    thinking INTEGER NOT NULL DEFAULT 0,
    stop_reason TEXT NOT NULL DEFAULT '',
    skill TEXT NOT NULL DEFAULT '',
    plugin TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(session_id, agent_id, key)
) STRICT;
CREATE INDEX model_call_by_time ON model_call(at_ms);
CREATE INDEX model_call_by_project ON model_call(project_key, at_ms);

-- category is one of builtin, mcp, skill, agent. target holds the program
-- name of a Bash call and is empty otherwise.
CREATE TABLE tool_call (
    session_id TEXT NOT NULL,
    key TEXT NOT NULL,
    agent_id TEXT NOT NULL DEFAULT '',
    project_key TEXT NOT NULL,
    at_ms INTEGER NOT NULL,
    name TEXT NOT NULL,
    category TEXT NOT NULL CHECK(category IN ('builtin','mcp','skill','agent')),
    mcp_server TEXT NOT NULL DEFAULT '',
    mcp_tool TEXT NOT NULL DEFAULT '',
    plugin TEXT NOT NULL DEFAULT '',
    skill TEXT NOT NULL DEFAULT '',
    subagent_type TEXT NOT NULL DEFAULT '',
    target TEXT NOT NULL DEFAULT '',
    message_key TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(session_id, key)
) STRICT;
CREATE INDEX tool_call_by_time ON tool_call(at_ms);
CREATE INDEX tool_call_by_project ON tool_call(project_key, at_ms);
CREATE INDEX tool_call_by_name ON tool_call(name, at_ms);

-- Joined to tool_call by (session_id, key). A call without a row here is
-- pending. duration_ms = 0 means the transcript reported none; readers then
-- use the difference of the two timestamps.
CREATE TABLE tool_result (
    session_id TEXT NOT NULL,
    key TEXT NOT NULL,
    project_key TEXT NOT NULL,
    at_ms INTEGER NOT NULL,
    is_error INTEGER NOT NULL CHECK(is_error IN (0,1)),
    interrupted INTEGER NOT NULL DEFAULT 0 CHECK(interrupted IN (0,1)),
    duration_ms INTEGER NOT NULL DEFAULT 0,
    sandbox_violations INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(session_id, key)
) STRICT;

CREATE TABLE prompt (
    session_id TEXT NOT NULL,
    key TEXT NOT NULL,
    agent_id TEXT NOT NULL DEFAULT '',
    project_key TEXT NOT NULL,
    at_ms INTEGER NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    chars INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(session_id, key)
) STRICT;
CREATE INDEX prompt_by_time ON prompt(at_ms);
CREATE INDEX prompt_by_project ON prompt(project_key, at_ms);

CREATE TABLE hook_run (
    session_id TEXT NOT NULL,
    key TEXT NOT NULL,
    agent_id TEXT NOT NULL DEFAULT '',
    project_key TEXT NOT NULL,
    at_ms INTEGER NOT NULL,
    event TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    tool_use_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK(status IN ('success','error','blocked','unknown')),
    exit_code INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(session_id, key)
) STRICT;
CREATE INDEX hook_run_by_time ON hook_run(at_ms);

CREATE TABLE turn_end (
    session_id TEXT NOT NULL,
    key TEXT NOT NULL,
    project_key TEXT NOT NULL,
    at_ms INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    messages INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(session_id, key)
) STRICT;

CREATE TABLE compaction (
    session_id TEXT NOT NULL,
    key TEXT NOT NULL,
    agent_id TEXT NOT NULL DEFAULT '',
    project_key TEXT NOT NULL,
    at_ms INTEGER NOT NULL,
    trigger_code TEXT NOT NULL DEFAULT '',
    pre_tokens INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(session_id, key)
) STRICT;
CREATE INDEX compaction_by_time ON compaction(at_ms);

-- Latest client-reported cost of a session. Written from the root transcript
-- only, and only when the transcript reported a numeric total.
CREATE TABLE cost_state (
    session_id TEXT PRIMARY KEY,
    project_key TEXT NOT NULL,
    at_ms INTEGER NOT NULL,
    total_cost_usd REAL NOT NULL,
    api_duration_ms INTEGER NOT NULL DEFAULT 0,
    tool_duration_ms INTEGER NOT NULL DEFAULT 0,
    lines_added INTEGER NOT NULL DEFAULT 0,
    lines_removed INTEGER NOT NULL DEFAULT 0
) STRICT;

-- Events pushed by third-party tools. message and attribute values are masked
-- and length-capped by the endpoint before they reach this table. attributes
-- is a JSON object of string values. duration_ms and value are NULL when the
-- sender did not report them.
CREATE TABLE ext_event (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('span','log','metric')),
    name TEXT NOT NULL,
    at_ms INTEGER NOT NULL,
    received_ms INTEGER NOT NULL,
    duration_ms REAL,
    status TEXT NOT NULL CHECK(status IN ('ok','error','unknown')),
    level TEXT NOT NULL CHECK(level IN ('debug','info','warn','error')),
    session_id TEXT NOT NULL DEFAULT '',
    tool_use_id TEXT NOT NULL DEFAULT '',
    trace_id TEXT NOT NULL DEFAULT '',
    span_id TEXT NOT NULL DEFAULT '',
    parent_span_id TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    attributes TEXT NOT NULL DEFAULT '{}',
    value REAL,
    unit TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX ext_event_by_time ON ext_event(at_ms);
CREATE INDEX ext_event_by_source ON ext_event(source, at_ms);
CREATE INDEX ext_event_by_session ON ext_event(session_id, at_ms);

-- Inputs refused at the endpoint, by sender. source = '' collects inputs that
-- could not be attributed.
CREATE TABLE ext_reject (
    source TEXT PRIMARY KEY,
    n INTEGER NOT NULL
) STRICT;
