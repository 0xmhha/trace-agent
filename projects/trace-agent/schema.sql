-- 데이터 계약 v1.3. 빈 DB 초기화용이다. 구 DB의 이행 절차는 데이터 설계에 있다.
PRAGMA foreign_keys = ON;
PRAGMA user_version = 13;

CREATE TABLE revision (
    rev INTEGER PRIMARY KEY AUTOINCREMENT,
    committed_ms INTEGER NOT NULL
) STRICT;
-- 첫 수집 전에도 빈 화면의 point를 발급한다.
INSERT INTO revision(rev, committed_ms) VALUES(0, 0);
CREATE TABLE store_state (
    singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
    epoch INTEGER NOT NULL CHECK(epoch >= 1),
    replay_floor INTEGER NOT NULL DEFAULT 0
) STRICT;
INSERT INTO store_state(singleton, epoch) VALUES(1, 1);

CREATE TABLE source (
    source_id TEXT PRIMARY KEY,
    client TEXT NOT NULL CHECK(client IN ('codex','claude','mcp','gateway')),
    kind TEXT NOT NULL,
    profile_key TEXT NOT NULL,
    instance_key TEXT NOT NULL,
    UNIQUE(client, kind, profile_key, instance_key)
) STRICT;
-- 허용 범위와 보존 정책은 같은 개정에서 변경 통지와 함께 저장한다.
CREATE TABLE source_config (
    source_id TEXT NOT NULL REFERENCES source,
    rev INTEGER NOT NULL REFERENCES revision,
    enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
    picker_ref TEXT NOT NULL,
    history_from_ms INTEGER,
    history_until_ms INTEGER,
    retention_days INTEGER NOT NULL CHECK(retention_days > 0),
    PRIMARY KEY(source_id, rev),
    CHECK(history_from_ms IS NULL OR history_until_ms IS NULL OR history_until_ms > history_from_ms)
) STRICT;
CREATE TABLE source_status (
    source_id TEXT NOT NULL REFERENCES source,
    rev INTEGER NOT NULL REFERENCES revision,
    at_ms INTEGER NOT NULL,
    health TEXT NOT NULL CHECK(health IN ('connected','delayed','lost','unsupported')),
    client_version TEXT,
    adapter_version TEXT NOT NULL,
    permission_code TEXT NOT NULL,
    capability_code TEXT NOT NULL,
    capability_state TEXT NOT NULL CHECK(capability_state IN ('available','partial','unavailable','unverified')),
    gap_from_ms INTEGER,
    gap_until_ms INTEGER,
    reason_code TEXT,
    CHECK(gap_until_ms IS NULL OR (gap_from_ms IS NOT NULL AND gap_until_ms >= gap_from_ms)),
    PRIMARY KEY(source_id, rev, capability_code)
) STRICT;
CREATE TABLE consent (
    source_id TEXT NOT NULL REFERENCES source,
    rev INTEGER NOT NULL REFERENCES revision,
    enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
    effective_ms INTEGER NOT NULL,
    PRIMARY KEY(source_id, rev)
) STRICT;
CREATE TABLE checkpoint (
    source_id TEXT NOT NULL REFERENCES source,
    stream_key TEXT NOT NULL,
    generation TEXT NOT NULL,
    position TEXT NOT NULL,
    rev INTEGER NOT NULL REFERENCES revision,
    PRIMARY KEY(source_id, stream_key)
) STRICT;
CREATE TABLE suppression (
    source_id TEXT NOT NULL REFERENCES source,
    scope TEXT NOT NULL CHECK(scope IN ('body','all')),
    from_ms INTEGER NOT NULL,
    until_ms INTEGER NOT NULL CHECK(until_ms > from_ms),
    selector_key TEXT NOT NULL,
    rev INTEGER NOT NULL REFERENCES revision,
    PRIMARY KEY(source_id, scope, from_ms, until_ms, selector_key)
) STRICT;

CREATE TABLE session (
    session_id TEXT PRIMARY KEY,
    owner_source TEXT NOT NULL REFERENCES source,
    external_key TEXT NOT NULL,
    born_rev INTEGER NOT NULL REFERENCES revision,
    UNIQUE(owner_source, external_key)
) STRICT;
CREATE TABLE task (
    task_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES session,
    external_key TEXT NOT NULL,
    born_rev INTEGER NOT NULL REFERENCES revision,
    UNIQUE(session_id, external_key)
) STRICT;
CREATE TABLE agent (
    task_id TEXT NOT NULL REFERENCES task ON DELETE CASCADE,
    agent_id TEXT NOT NULL,
    born_rev INTEGER NOT NULL REFERENCES revision,
    PRIMARY KEY(task_id, agent_id)
) STRICT;
CREATE TABLE module (
    task_id TEXT NOT NULL REFERENCES task ON DELETE CASCADE,
    module_id TEXT NOT NULL,
    born_rev INTEGER NOT NULL REFERENCES revision,
    PRIMARY KEY(task_id, module_id)
) STRICT;
CREATE TABLE attempt (
    task_id TEXT NOT NULL REFERENCES task ON DELETE CASCADE,
    attempt_id TEXT NOT NULL,
    born_rev INTEGER NOT NULL REFERENCES revision,
    PRIMARY KEY(task_id, attempt_id)
) STRICT;
CREATE TABLE evidence (
    evidence_id INTEGER PRIMARY KEY,
    source_id TEXT NOT NULL REFERENCES source,
    stream_key TEXT NOT NULL,
    generation TEXT NOT NULL,
    record_key TEXT NOT NULL,
    version_key TEXT NOT NULL,
    semantic_tag TEXT NOT NULL,
    occurred_ms INTEGER,
    observed_ms INTEGER NOT NULL,
    clock_key TEXT,
    rev INTEGER NOT NULL REFERENCES revision,
    record_kind TEXT NOT NULL,
    observer_code TEXT CHECK(observer_code IN ('agent','model','tool','gateway','client','collector','unknown')),
    adapter_version TEXT,
    UNIQUE(source_id, stream_key, generation, record_key, version_key),
    UNIQUE(evidence_id, rev)
) STRICT;
CREATE TABLE evidence_scope (
    evidence_id INTEGER NOT NULL REFERENCES evidence ON DELETE CASCADE,
    selector_key TEXT NOT NULL,
    PRIMARY KEY(evidence_id, selector_key)
) STRICT;
CREATE INDEX scope_by_selector ON evidence_scope(selector_key, evidence_id);
CREATE TABLE evidence_key (
    evidence_id INTEGER NOT NULL REFERENCES evidence ON DELETE CASCADE,
    namespace TEXT NOT NULL,
    scope_key TEXT NOT NULL,
    value_key TEXT NOT NULL,
    PRIMARY KEY(evidence_id, namespace, scope_key, value_key)
) STRICT;
CREATE INDEX lookup_evidence_key ON evidence_key(namespace, scope_key, value_key);

-- 같은 프로필의 두 어댑터도 같은 thread를 볼 수 있다. 내부 토큰만 저장한다.
CREATE TABLE agent_identity (
    identity_id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL CHECK(namespace IN ('codex_thread','claude_agent')),
    scope_key TEXT NOT NULL,
    value_key TEXT NOT NULL,
    born_rev INTEGER NOT NULL REFERENCES revision,
    UNIQUE(namespace, scope_key, value_key)
) STRICT;
-- 출처별 관찰이다. 여러 근거가 같은 부모 관계를 지지할 수 있다.
CREATE TABLE identity_observation (
    evidence_id INTEGER NOT NULL,
    evidence_rev INTEGER NOT NULL,
    rev INTEGER NOT NULL REFERENCES revision CHECK(rev >= evidence_rev),
    identity_id TEXT NOT NULL REFERENCES agent_identity,
    kind TEXT NOT NULL CHECK(kind IN ('self','spawn_parent')),
    parent_identity_id TEXT REFERENCES agent_identity,
    withdrawn INTEGER NOT NULL DEFAULT 0 CHECK(withdrawn IN (0,1)),
    PRIMARY KEY(evidence_id, identity_id, kind, rev),
    FOREIGN KEY(evidence_id, evidence_rev) REFERENCES evidence(evidence_id, rev) ON DELETE CASCADE,
    CHECK((kind = 'spawn_parent') = (parent_identity_id IS NOT NULL)),
    CHECK(parent_identity_id IS NULL OR identity_id <> parent_identity_id)
) STRICT;
CREATE INDEX identity_evidence ON identity_observation(identity_id, evidence_id);

-- 발생 단위는 블록 또는 항목이다. actor는 호출을 수행한 주체를 가리킨다.
CREATE TABLE call_observation (
    evidence_id INTEGER NOT NULL,
    evidence_rev INTEGER NOT NULL,
    rev INTEGER NOT NULL REFERENCES revision CHECK(rev >= evidence_rev),
    namespace TEXT NOT NULL,
    family_key TEXT NOT NULL,
    stream_scope_key TEXT NOT NULL,
    actor_scope_key TEXT,
    call_key TEXT NOT NULL,
    side TEXT NOT NULL CHECK(side IN ('call','result')),
    withdrawn INTEGER NOT NULL DEFAULT 0 CHECK(withdrawn IN (0,1)),
    PRIMARY KEY(evidence_id, rev),
    FOREIGN KEY(evidence_id, evidence_rev) REFERENCES evidence(evidence_id, rev) ON DELETE CASCADE
) STRICT;
CREATE INDEX match_call_in_stream ON call_observation
    (namespace, family_key, stream_scope_key, actor_scope_key, call_key, side);

-- 작업 귀속 전에도 하나의 출처 범위에서 확인한 모델 시도를 조회한다.
CREATE TABLE source_model_observation (
    evidence_id INTEGER NOT NULL,
    evidence_rev INTEGER NOT NULL,
    rev INTEGER NOT NULL REFERENCES revision CHECK(rev >= evidence_rev),
    scope_key TEXT NOT NULL,
    attempt_key TEXT NOT NULL,
    model_key TEXT,
    role TEXT NOT NULL CHECK(role IN ('request','response','conversation','state')),
    phase TEXT NOT NULL CHECK(phase IN ('proposed','start_observed','running','ended','unknown')),
    result TEXT NOT NULL CHECK(result IN ('success','model_error','cancelled','unknown')),
    withdrawn INTEGER NOT NULL DEFAULT 0 CHECK(withdrawn IN (0,1)),
    PRIMARY KEY(evidence_id, role, rev),
    FOREIGN KEY(evidence_id, evidence_rev) REFERENCES evidence(evidence_id, rev) ON DELETE CASCADE
) STRICT;
CREATE INDEX model_observations_by_key ON source_model_observation(scope_key, attempt_key, rev);

CREATE TABLE body (
    evidence_id INTEGER NOT NULL REFERENCES evidence ON DELETE CASCADE,
    field_code TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('sanitized','truncated','redaction_failed','withheld','deleted')),
    clean_text TEXT,
    observer_code TEXT NOT NULL,
    conversation_role TEXT CHECK(conversation_role IN ('user','assistant','system','developer','tool','unknown')),
    actor_code TEXT,
    edge_code TEXT,
    truncation_code TEXT,
    PRIMARY KEY(evidence_id, field_code),
    CHECK((status IN ('sanitized','truncated')) = (clean_text IS NOT NULL))
) STRICT;

-- 같은 evidence에서 여러 사실이 나올 수 있다. 한 사실은 여러 출처에서 관찰할 수 있다.
CREATE TABLE fact (
    task_id TEXT NOT NULL REFERENCES task ON DELETE CASCADE,
    fact_id TEXT NOT NULL,
    stable_key TEXT NOT NULL,
    PRIMARY KEY(task_id, fact_id),
    UNIQUE(task_id, stable_key)
) STRICT;
CREATE TABLE fact_version (
    task_id TEXT NOT NULL,
    fact_id TEXT NOT NULL,
    rev INTEGER NOT NULL REFERENCES revision,
    occurred_ms INTEGER,
    clock_key TEXT,
    kind TEXT NOT NULL,
    grade TEXT NOT NULL CHECK(grade IN ('direct','inferred','unknown')),
    subject_role TEXT,
    edge_code TEXT,
    value_code TEXT,
    numeric_value REAL,
    identity_id TEXT REFERENCES agent_identity,
    agent_id TEXT,
    related_agent_id TEXT,
    attempt_id TEXT,
    related_attempt_id TEXT,
    plan_key TEXT,
    step_key TEXT,
    step_ordinal INTEGER CHECK(step_ordinal >= 0),
    related_module_id TEXT,
    module_id TEXT,
    reason_code TEXT,
    withdrawn INTEGER NOT NULL DEFAULT 0 CHECK(withdrawn IN (0,1)),
    PRIMARY KEY(task_id, fact_id, rev),
    FOREIGN KEY(task_id, fact_id) REFERENCES fact(task_id, fact_id) ON DELETE CASCADE,
    FOREIGN KEY(task_id, agent_id) REFERENCES agent(task_id, agent_id),
    FOREIGN KEY(task_id, related_agent_id) REFERENCES agent(task_id, agent_id),
    FOREIGN KEY(task_id, attempt_id) REFERENCES attempt(task_id, attempt_id),
    FOREIGN KEY(task_id, related_attempt_id) REFERENCES attempt(task_id, attempt_id),
    FOREIGN KEY(task_id, module_id) REFERENCES module(task_id, module_id),
    FOREIGN KEY(task_id, related_module_id) REFERENCES module(task_id, module_id),
    CHECK(step_ordinal IS NULL OR step_key IS NOT NULL),
    CHECK(grade = 'direct' OR reason_code IS NOT NULL),
    CHECK(kind <> 'assignment' OR (identity_id IS NOT NULL AND agent_id IS NOT NULL)),
    CHECK(kind <> 'attempt_duration' OR
          (attempt_id IS NOT NULL AND value_code IS NOT NULL AND
           value_code IN ('reported','observed_interval') AND numeric_value IS NOT NULL AND numeric_value >= 0)),
    CHECK(step_key IS NULL OR plan_key IS NOT NULL)
) STRICT;
CREATE TABLE support (
    task_id TEXT NOT NULL,
    fact_id TEXT NOT NULL,
    fact_rev INTEGER NOT NULL,
    evidence_id INTEGER NOT NULL REFERENCES evidence ON DELETE CASCADE,
    evidence_rev INTEGER NOT NULL CHECK(evidence_rev <= fact_rev),
    PRIMARY KEY(task_id, fact_id, fact_rev, evidence_id),
    FOREIGN KEY(evidence_id, evidence_rev) REFERENCES evidence(evidence_id, rev) ON DELETE CASCADE,
    FOREIGN KEY(task_id, fact_id, fact_rev)
        REFERENCES fact_version(task_id, fact_id, rev) ON DELETE CASCADE
) STRICT;
CREATE TABLE fact_body (
    task_id TEXT NOT NULL,
    fact_id TEXT NOT NULL,
    fact_rev INTEGER NOT NULL,
    purpose TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK(ordinal >= 0),
    evidence_id INTEGER NOT NULL,
    field_code TEXT NOT NULL,
    PRIMARY KEY(task_id, fact_id, fact_rev, purpose, ordinal),
    FOREIGN KEY(task_id, fact_id, fact_rev, evidence_id)
        REFERENCES support(task_id, fact_id, fact_rev, evidence_id) ON DELETE CASCADE,
    FOREIGN KEY(evidence_id, field_code) REFERENCES body(evidence_id, field_code) ON DELETE CASCADE
) STRICT;
CREATE TABLE fact_dependency (
    task_id TEXT NOT NULL,
    fact_id TEXT NOT NULL,
    fact_rev INTEGER NOT NULL,
    input_id TEXT NOT NULL,
    input_rev INTEGER NOT NULL CHECK(input_rev <= fact_rev),
    PRIMARY KEY(task_id, fact_id, fact_rev, input_id, input_rev),
    FOREIGN KEY(task_id, fact_id, fact_rev)
        REFERENCES fact_version(task_id, fact_id, rev) ON DELETE CASCADE,
    FOREIGN KEY(task_id, input_id, input_rev)
        REFERENCES fact_version(task_id, fact_id, rev) ON DELETE CASCADE,
    CHECK(fact_id <> input_id)
) STRICT;
CREATE INDEX dependents_by_input ON fact_dependency(task_id, input_id, input_rev);
CREATE INDEX evidence_by_source_time ON evidence(source_id, occurred_ms, rev);
CREATE INDEX support_by_evidence ON support(evidence_id);
CREATE INDEX fact_by_task_time ON fact_version(task_id, occurred_ms, fact_id, rev);
CREATE INDEX fact_by_attempt ON fact_version(task_id, attempt_id, rev);
CREATE INDEX fact_by_agent ON fact_version(task_id, agent_id, rev);

-- 같은 리비전에 영향을 받은 화면 범위. 본문, 자유문, 외부 ID를 넣지 않는다.
CREATE TABLE change_scope (
    rev INTEGER NOT NULL REFERENCES revision,
    scope_kind TEXT NOT NULL CHECK(scope_kind IN ('source','task','store')),
    scope_key TEXT NOT NULL,
    PRIMARY KEY(rev, scope_kind, scope_key)
) STRICT;
