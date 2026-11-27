// Messages of the capabilities namespace. Keys are written without the namespace
// prefix; components ask for "capabilities.<key>". Every key in en must exist in ko
// (the type below enforces it). Placeholders look like {name}; plural forms
// use the suffixes _one and _other.
//
// The trace API reports each capability as a code, a state and an English
// reason (internal/transport/capabilities.go). Keys are "<code>.<state>",
// narrowed where the server words the same code and state differently:
// "<code>.<state>.<source kind>" (for example codex-otel), "<code>.<state>.otel"
// or ".observer" for a group of kinds, and ".missing" / ".different" for the
// client version. The page tries the most specific key first and shows the
// server's reason when no key matches.
export const en = {
  'desktop_live.UNVERIFIED':
    'Notifications and restarts of work started in a separate desktop window have not been verified.',
  'history.UNAVAILABLE.otel':
    'Collects the events the SDK sends after the OTLP receiver is connected. Earlier records cannot be requested.',
  'history.UNAVAILABLE.observer':
    'Records only the events delivered to the selected observation receiver. Earlier history of the origin cannot be queried.',
  'history.UNVERIFIED': 'The existing evidence this source delivered has not been verified yet.',
  'history.AVAILABLE': 'Evidence was collected up to that point in the selected range.',
  'telemetry.AVAILABLE.claude-otel':
    'OTLP/HTTP protobuf and JSON export from Claude Code CLI 2.1.329 was verified.',
  'telemetry.AVAILABLE.codex-otel':
    'API attempt numbers, HTTP status and durations sent over OTLP/HTTP protobuf and JSON by Codex CLI 0.169.1 were verified.',
  'telemetry.UNVERIFIED':
    'Export was verified with Codex CLI 0.169.1. The version of this source is outside the verified range.',
  'telemetry.UNAVAILABLE': 'Codex OTel events are collected once a dedicated source is connected.',
  'restart.PARTIAL':
    'The same receiver address and source credentials are restored. Events the sender retries are treated as the same record.',
  'restart.AVAILABLE.codex-app-server':
    'The app-server of the same profile was restarted and the same history items were compared.',
  'restart.AVAILABLE.codex-rollout':
    'Resumes from the stored file position and holds the last incomplete line until the next read.',
  'restart.AVAILABLE.claude-transcript':
    'Resumes the same file from the SQLite checkpoint and records file replacement or truncation as a gap.',
  'hook.PARTIAL':
    'Receives the pre-run and end events of registered hooks. Pre-run events are not used as evidence of completion.',
  'mcp_observer.PARTIAL':
    'Verifies only the receipts and returns that explicitly connected MCP servers report. Matching them with the client needs shared connection evidence.',
  'cli_history.AVAILABLE': 'The thread, turn and item history of Codex CLI 0.169.1 was read.',
  'live.PARTIAL':
    'Rereads the history of the selected working directory periodically. Immediate delivery of notifications for other client connections is unverified.',
  'live.AVAILABLE.codex-rollout': 'Complete lines were collected while a real CLI turn was in progress.',
  'live.AVAILABLE.claude-transcript':
    'Collects complete lines from the selected session file. Reading child files as well needs the project folder.',
  'plan.PARTIAL':
    'Projecting plan-change notifications into steps was verified on synthetic samples. Real CLI plan updates are unverified.',
  'cli.AVAILABLE.codex-rollout': 'Plain commands and child spawning were verified with Codex CLI 0.169.1.',
  'cli.AVAILABLE.claude-transcript':
    'Tool round trips and child files were verified with Claude Code CLI 2.1.329.',
  'client_version.UNVERIFIED.missing': 'The evidence of this source has no client version.',
  'client_version.UNVERIFIED.different':
    'The observed client version differs from the version of the installed-build compatibility test.',
  'body.UNAVAILABLE.observer': 'This observation path collects identifiers and states only.',
  'body.UNAVAILABLE': 'Body consent is off for this source.',
  'body.PARTIAL':
    'Shows only supported fields, sanitized, for which both the event time and the current consent are valid.',
  'body.PARTIAL.claude-otel':
    'Sanitizes only the supported fields the sender sent after turning on the body option for this loopback address.',
  'body.PARTIAL.codex-otel':
    'Sanitizes user input only when the CLI log_user_prompt option is on and body consent is given for the source.',
  'collection.UNAVAILABLE': 'Collection is paused in the source settings.',
} as const satisfies Record<string, string>

export const ko: Record<keyof typeof en, string> = {
  'desktop_live.UNVERIFIED': '별도 데스크톱 창에서 시작한 작업의 알림과 재시작을 확인하지 못했습니다.',
  'history.UNAVAILABLE.otel':
    'OTLP 수신기를 연결한 뒤 SDK가 전송한 이벤트를 수집합니다. 이전 기록을 요청하는 기능은 없습니다.',
  'history.UNAVAILABLE.observer':
    '선택한 관찰 수신기에 전달한 이벤트만 기록합니다. 원천의 이전 이력 조회는 지원하지 않습니다.',
  'history.UNVERIFIED': '이 출처가 전달한 기존 근거를 아직 확인하지 못했습니다.',
  'history.AVAILABLE': '선택한 범위에서 해당 시점까지 근거가 수집됐습니다.',
  'telemetry.AVAILABLE.claude-otel': 'Claude Code CLI 2.1.329의 OTLP/HTTP protobuf와 JSON 송신을 확인했습니다.',
  'telemetry.AVAILABLE.codex-otel':
    'Codex CLI 0.169.1에서 OTLP/HTTP protobuf와 JSON으로 보낸 API 시도 번호, HTTP 상태, 소요 시간을 확인했습니다.',
  'telemetry.UNVERIFIED':
    'Codex CLI 0.169.1에서 송신을 확인했습니다. 현재 출처의 버전은 확인 범위 밖입니다.',
  'telemetry.UNAVAILABLE': 'Codex OTel 이벤트는 전용 출처를 연결하면 수집합니다.',
  'restart.PARTIAL':
    '동일한 수신 주소와 출처 자격을 복원합니다. 송신자가 재시도한 이벤트는 같은 기록으로 처리합니다.',
  'restart.AVAILABLE.codex-app-server':
    '같은 프로필의 app-server를 다시 시작해 이력의 동일 항목을 대조했습니다.',
  'restart.AVAILABLE.codex-rollout':
    '저장된 파일 위치에서 재개하고 마지막 미완결 줄은 다음 읽기까지 보류합니다.',
  'restart.AVAILABLE.claude-transcript':
    'SQLite 체크포인트에서 동일 파일을 재개하고 파일 교체·축소는 공백으로 기록합니다.',
  'hook.PARTIAL':
    '등록한 훅의 실행 전·종료 이벤트를 받습니다. 실행 전 이벤트는 완료 근거로 쓰지 않습니다.',
  'mcp_observer.PARTIAL':
    '명시적으로 연결한 MCP 서버가 보고한 수신·반환만 확인합니다. 클라이언트와의 대응에는 공통 연결 근거가 필요합니다.',
  'cli_history.AVAILABLE': 'Codex CLI 0.169.1의 스레드·턴·항목 이력을 읽었습니다.',
  'live.PARTIAL':
    '선택한 작업 디렉터리의 이력을 주기적으로 다시 읽습니다. 다른 클라이언트 연결의 즉시 알림 전달은 미확인입니다.',
  'live.AVAILABLE.codex-rollout': '실제 CLI 턴이 진행 중인 동안 완결된 줄을 수집했습니다.',
  'live.AVAILABLE.claude-transcript':
    '선택한 세션 파일에서 완결된 줄을 수집합니다. 자식 파일을 함께 읽으려면 프로젝트 폴더가 필요합니다.',
  'plan.PARTIAL':
    '계획 변경 알림의 단계 투영은 합성 표본에서 확인했습니다. 실제 CLI 계획 갱신은 미확인입니다.',
  'cli.AVAILABLE.codex-rollout': 'Codex CLI 0.169.1에서 일반 명령과 자식 생성을 확인했습니다.',
  'cli.AVAILABLE.claude-transcript': 'Claude Code CLI 2.1.329에서 도구 왕복과 자식 파일을 확인했습니다.',
  'client_version.UNVERIFIED.missing': '이 출처의 근거에 클라이언트 버전이 없습니다.',
  'client_version.UNVERIFIED.different': '관찰한 클라이언트 버전은 설치본 호환 시험의 버전과 다릅니다.',
  'body.UNAVAILABLE.observer': '이 관찰 경로는 식별자와 상태만 수집합니다.',
  'body.UNAVAILABLE': '이 출처의 본문 동의가 꺼져 있습니다.',
  'body.PARTIAL': '사건 시각과 현재 동의가 모두 유효한 지원 필드만 정제해 표시합니다.',
  'body.PARTIAL.claude-otel': '송신자가 이 루프백 주소로 본문 옵션을 켠 뒤 보낸 지원 필드만 정제합니다.',
  'body.PARTIAL.codex-otel':
    'CLI의 log_user_prompt 옵션을 켜고 출처 본문에 동의한 경우에만 사용자 입력을 정제합니다.',
  'collection.UNAVAILABLE': '출처 설정에서 수집을 중지했습니다.',
}
