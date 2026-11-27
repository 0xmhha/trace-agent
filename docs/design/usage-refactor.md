# 사용 분석 리팩토링 설계

Trace Agent 를 "호출의 근거를 추적하는 도구"에서 "Claude Code 사용 현황을 분석하고 세션을 디버깅하는 도구"로 넓힌다. 기존 근거 추적 기능은 그대로 두고, 그 옆에 사용 분석 파이프라인을 새로 만든다.

이 문서는 병렬로 진행하는 구현 작업의 기준이다. 타입과 함수 시그니처는 코드가 원본이고, 이 문서는 그 코드가 무엇을 해야 하는지를 적는다.

## 1. 목표와 정한 것

| 요구 | 이 설계의 답 |
|---|---|
| 세션을 모니터링하고 수치를 대시보드로 보여 준다 | `~/.claude/projects` 전체를 한 번 동의받고 자동 감시한다. 토큰, 모델, 도구, 스킬, 플러그인, MCP, 훅을 집계한다. |
| 사용 습관을 분석하고 개선과 플러그인, MCP 를 추천한다 | 집계 수치에 로컬 규칙을 적용한다. 외부 호출은 없다. |
| 한 세션의 대화를 상세히 보고 디버깅한다 | 세션 상세에서 원본 세션 파일을 바로 읽어 타임라인으로 보여 준다. 비밀 값 패턴은 화면에 내기 전에 가린다. |
| 도구 내부에서 일어난 일도 본다 | 제3자 도구가 루프백 `/v1/events` 로 이벤트를 보낸다. 세션과 도구 호출에 연결해 타임라인에 함께 보여 준다. |
| 화면을 나누고 쓰기 쉽게 한다 | 메뉴를 사용 분석 6개와 추적 묶음으로 나눈다. 20개 화면이 들어 있던 파일을 화면별 파일로 쪼갠다. |

정한 것 일곱 가지다.

1. 새 지표는 Claude Code 세션 기록에 전부 넣는다. Codex 는 지금 되는 기능만 유지한다.
2. 첫 실행 때 한 번 묻고 기본 경로 전체를 감시한다. 처음 읽는 기간은 최근 30일이다.
3. 대화 원문은 DB 에 복사하지 않는다. 볼 때 원본에서 읽는다.
4. 인사이트와 추천은 로컬 규칙으로 만든다.
5. 제3자 입력은 범용 HTTP 이벤트 API 와 전송 명령으로 받는다. 기존 훅, MCP, OTel 수신기는 그대로 둔다.
6. 요구 전체를 한 번에 구현한다. 출처 종류 등록 표와 공통 시험도 넣는다. 기존 GraphQL 응답에 수집 공백을 싣는 일은 이번에 하지 않는다.
7. 설계와 계약 코드를 먼저 쓰고, 감사한 뒤, 작업별 워크트리에서 병렬로 구현해 통합 브랜치에 합친다.

## 2. 구조

```
~/.claude/projects/**.jsonl ──► transcript.Scan ──► db.Apply ──► usage.sqlite
        │                                                         │
        │ (세션 상세를 열 때만)                                    ├─► db 집계 질의 ─► service.Handle ─► Bridge.Usage ─► 화면
        └────────────────────► timeline.Read ─────────────────────┘                     ▲
제3자 도구 ─ POST /v1/events ─► extension 수신기 ─► db.AppendExt                          │
                                                   insight.Evaluate(집계) ───────────────┘
```

사용 분석 파이프라인은 기존 근거 저장소(`activity.sqlite`, GraphQL, 리비전과 epoch)와 코드도 파일도 공유하지 않는다. 이유는 둘이다. 기존 저장소는 계약 시험이 촘촘해서 건드리면 회귀 위험이 크다. 그리고 사용 분석 자료는 원본에서 다시 만들 수 있는 파생 자료라서 같은 수준의 보존 규칙이 필요 없다.

| 패키지 | 책임 | 감추는 것 |
|---|---|---|
| `internal/usage` | 공용 타입, JSON 계약, 마스킹, 작은 포트 | 없음. 계약 자체다. |
| `internal/usage/transcript` | 세션 기록 줄 → 수치 레코드(`usage.Batch`) | 세션 기록의 필드 배치와 버전 차이 |
| `internal/usage/timeline` | 세션 기록 → 화면용 타임라인 한 쪽 | 원문을 읽고 가리고 자르는 방법 |
| `internal/usage/db` | `usage.sqlite` 저장과 집계 질의 | 테이블 구조와 SQL |
| `internal/usage/insight` | 집계 → 인사이트와 추천 | 규칙과 임계값 |
| `internal/usage/service` | 주기적 스캔, UI 요청 분배 | 스캔 순서와 진행 상태 |
| `internal/extension` | `/v1/events` 수신, 검증, 마스킹 | 전송 형식 검증 규칙 |
| `cmd/trace-emit` | 셸과 스크립트에서 이벤트 전송 | HTTP 호출 세부 |
| `cmd/trace-usage` | GUI 없이 스캔하고 집계를 JSON 으로 출력 | 서비스 조립 |
| `desktop` (usage*.go) | 서비스 수명 관리, 네이티브 동의 창, 브리지 | Wails 와 키체인 |
| `desktop/frontend/src/usage` | 사용 분석 화면 | 화면 구성과 차트 |
| `desktop/frontend/src/trace` | 기존 추적 화면 (파일만 나눔) | 기존 GraphQL 화면 |

## 3. 계약

코드가 원본이다. 아래 파일은 구현 작업이 **고치지 않는다.** 고쳐야 한다고 판단하면 고치지 말고 결과 보고에 적는다.

| 파일 | 내용 |
|---|---|
| `internal/usage/record.go` | 파서가 만드는 레코드, `ScanInput`, `Batch`, `ExtEvent` |
| `internal/usage/api.go` | UI 로 나가는 JSON 타입, 메서드별 요청과 응답 타입, 상한값, 오류 값, `ExtSink`, `Settings` |
| `internal/usage/mask.go`, `normalize.go` | 비밀 값 가리기(`Mask`, `SecretKey`), 빈 목록을 `[]` 로 내는 `NonNil` |
| `internal/usage/testdata/**` | 파서 둘이 함께 쓰는 합성 세션 기록과 기대값 |
| `internal/usage/db/schema.sql`, `db.go`, `testutil_test.go` | 테이블, `Open`, 설정 키와 공용 도우미, 시험용 시드 |
| `internal/usage/service/ports.go`, `wiring_test.go` | 서비스가 요구하는 `Store`, `ExtensionControl`, 함수 타입과 그 컴파일 검사 |
| `internal/extension/wire.go` | 확장 이벤트 전송 형식(`WireEvent`), 상한값, 필드 패턴 |
| `desktop/frontend/src/usage/types.ts`, `api.ts`, `mock.ts`, `ui.tsx`, `usage.css`, `index.tsx`, `preview.tsx` | 화면 쪽 계약과 공용 부품 |

각 패키지의 뼈대 파일에는 공개 함수의 시그니처와 문서 주석이 이미 있다. 구현 작업은 본문을 채우고, 시그니처와 주석의 약속을 바꾸지 않는다.

### 공통 규칙

- **멱등**: 모든 레코드에 자연 키가 있다. 같은 줄을 두 번 읽어도 결과가 같아야 한다.
- **세 가지 상태**: "없다", "읽지 못했다", "아직 읽는 중"을 구별한다. 못 읽은 줄은 건너뛰고 센다. 건너뛴 줄 때문에 그 뒤를 못 읽는 일이 없어야 한다. 구간 집계 응답(`Overview`, `Tools`, `Projects`, `SessionPage`, `InsightReport`)은 `Coverage` 를 싣는다. 세션 상세와 타임라인은 `Source` 와 `Skipped` 로, 확장 응답은 `Rejected` 로 같은 일을 한다.
- **Coverage 는 서비스가 채운다**: 저장소의 집계 메서드는 `Coverage` 를 비워 둔다. 서비스가 `Store.Coverage` 로 채우고 `disabled` 와 `scanning` 을 덮는다.
- **빈 목록은 `[]`**: 응답의 목록은 비어도 `null` 이 아니다. 서비스가 인코딩 전에 `usage.NonNil` 을 적용한다.
- **원문을 저장하지 않는다**: DB 에는 메시지 본문, 도구 인자, 도구 출력을 넣지 않는다. 들어가는 글자는 이름과 라벨뿐이고 목록은 11장에 있다. 모든 라벨은 `usage.MaxLabelLen` 에서 자르고, 세션 제목은 저장 전에 `Mask` 를 거친다.
- **비용을 추정하지 않는다**: 클라이언트가 기록한 `totalCostUSD` 만 쓴다. 그 값이 숫자로 있는 줄에서만 `CostState` 를 만든다. 없으면 "미확인"이고 0 으로 바꾸지 않는다. 단가표를 두지 않는다.
- **의존성을 바꾸지 않는다**: `go mod tidy`, `go get`, `npm install <패키지>` 를 실행하지 않는다. `go.mod`, `go.sum`, `package.json`, `package-lock.json` 은 누구도 고치지 않는다.
- **시각**: 저장은 Unix 밀리초, JSON 은 RFC 3339. 구간은 `[from, until)` 이다.
- **범위와 세션**: 목록과 집계의 수치는 요청 구간 안의 행만 센다. 세션 상세는 구간과 무관하게 세션 전체를 보여 준다.

## 4. 세션 기록에서 읽는 것

Claude Code 2.1.x 의 세션 기록 한 개를 필드 이름과 값의 종류만 읽어 확인한 배치다. 값 자체는 읽지 않았다. 파일 배치는 `<프로젝트 폴더>/<세션 ID>.jsonl` 이 루트이고 `<프로젝트 폴더>/<세션 ID>/subagents/agent-<ID>.jsonl` 이 하위 에이전트다. 프로젝트 폴더 이름이 `ProjectKey` 다.

| 레코드 | 줄의 `type` | 읽는 필드 |
|---|---|---|
| `SessionPatch` | 모든 줄 | `sessionId`, `agentId`(하위 에이전트만), `cwd`, `entrypoint`, `version`, `gitBranch`, `timestamp`. 제목은 `type=ai-title` 의 `aiTitle`. 하위 에이전트 종류는 `attributionAgent`. |
| `ModelCall` | `assistant` | 키 `message.id`. `message.model`, `message.usage.input_tokens`, `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`, `output_tokens_details.thinking_tokens`, `message.stop_reason`, `attributionSkill`, `attributionPlugin`. 같은 `message.id` 가 내용 블록마다 한 줄씩 반복된다. 한 번만 센다. |
| `ToolCall` | `assistant` | `message.content[]` 중 `type=tool_use`. 키 `id`, `name`, `input`. `Skill` 은 `input.skill`, `Agent` 와 `Task` 는 `input.subagent_type`, `Bash` 는 `input.command` 의 프로그램 이름만. |
| `ToolResult` | `user` | `message.content[]` 중 `type=tool_result`. 키 `tool_use_id`, `is_error`. 줄의 `toolUseResult.interrupted`, `toolUseResult.durationMs`, `sandboxViolations` 길이. |
| `Prompt` | `user` | `message.content` 가 문자열이거나 `text` 블록이고, `isMeta` 가 아니며, `tool_result` 블록이 없는 줄. 키 `uuid`, `promptSource`, 글자 수. |
| `HookRun` | `attachment` | `attachment.type` 이 `hook_` 로 시작. 키 `uuid`. `hookEvent`, `hookName`, `toolUseID`, `exitCode`, `durationMs`. 상태는 `hook_success` 가 성공, 이름에 `non_blocking_error` 나 `cancelled` 가 있으면 오류, `blocking_error` 만 있으면 차단, 그 밖은 불명이다. `system` 의 `stop_hook_summary` 줄은 세지 않는다. |
| `TurnEnd` | `system` | `subtype=turn_duration`. 키 `uuid`, `durationMs`, `messageCount`. 루트 기록에만 있다. |
| `Compaction` | `system` | `subtype=compact_boundary`. 키 `uuid`. `compactMetadata.trigger`, `compactMetadata.preTokens`. |
| `CostState` | `cost-state` | `totalCostUSD`, `totalAPIDuration`, `totalToolDuration`, `totalLinesAdded`, `totalLinesRemoved`. 이 줄에는 시각이 없다. 같은 배치에서 앞서 본 마지막 시각을 쓴다. 루트 기록에만 있다. |

`ToolCall.MessageKey` 는 그 호출을 낸 줄의 `message.id` 다. `Prompt` 는 사람이 시작한 턴만 센다. 루트 기록에서만 만들고, `isMeta` 줄, 압축 요약 줄(`isCompactSummary`), 클라이언트가 스스로 쓴 줄(`promptSource` 가 `system`)은 세지 않는다. 하위 에이전트 기록의 첫 user 줄도 사람이 쓴 것이 아니다.

스킬 이름은 플러그인까지 포함한 전체 이름(`<플러그인>:<스킬>`)으로 묶는다. `Skill` 도구의 입력과 `attributionSkill` 은 둘 다 전체 이름을 담는다. 플러그인에 속하지 않은 스킬은 이름만 있고 플러그인이 비어 있다. 이름을 정하는 규칙은 `internal/usage/db/db.go` 한 곳에 있고 구간 집계와 세션 상세가 같이 쓴다.

**로컬 기록에서 확인한 것.** 최근 기록 256개, 91,416줄의 구조만 읽었다. 값은 읽지 않았다.

- 훅의 `attachment.type` 은 `hook_success` 12,419건, `hook_non_blocking_error` 245건, `hook_cancelled` 2건이었다. `hook_blocking_error` 는 보지 못했다.
- 가장 긴 줄은 202KB 였다. `usage.MaxLineBytes` 는 16MiB 다.
- 줄의 `sessionId` 가 파일의 세션과 다른 경우는 표본에서 0건이었다. 같은 `message.id` 가 두 파일에 나온 경우는 8,859개 중 2개였다.
- `type` 은 줄의 첫 키가 아니다. 줄 앞부분만 보고 종류를 알 수 없다.

**스트림의 주인은 파일 이름이다.** 서비스가 파일 이름에서 세션 ID 와 에이전트 ID 를 뽑아 `ScanInput` 으로 넘긴다. 줄의 `sessionId` 가 그와 다르면 다른 기록에서 복사된 줄로 보고 넘기며 `Batch.Foreign` 에 센다. 건너뛴 줄로는 세지 않는다.

**공용 시험 자료.** `internal/usage/testdata/` 에 합성한 루트 기록과 하위 에이전트 기록이 있고 `README.md` 에 기대값이 있다. scan 과 timeline 은 둘 다 이 자료로 시험한다.

**도구 분류.** 이름이 `mcp__` 로 시작하면 `mcp` 다. `mcp__<서버>__<도구>` 로 나누고, 서버가 `plugin_<플러그인>_<서버>` 꼴이면 플러그인 이름을 뽑는다. `Skill` 은 `skill`, `Agent` 와 `Task` 는 `agent`, 나머지는 `builtin` 이다. 스킬 이름이 `<플러그인>:<스킬>` 꼴이면 앞부분이 플러그인이다.

**Bash 프로그램 이름.** `command` 의 첫 단어에서 경로를 뗀 이름이다. `VAR=x cmd` 는 `cmd`, `sudo cmd` 는 `cmd` 다. 셸을 준비하기만 하는 문장(`cd`, `export`, `pushd` 등) 뒤에 다른 문장이 이어지면 그 문장을 건너뛴다. `cd /repo && git status` 는 `git` 이다. 실제 기록에서 이 규칙이 없으면 `cd` 가 1위로 나온다. `[A-Za-z0-9._+-]{1,32}` 에 맞지 않으면 비운다. 인자는 어떤 경우에도 저장하지 않는다.

**타임라인.** `timeline.Read` 는 같은 줄에서 화면용 항목을 만든다. 사용자 입력, 어시스턴트 글, 도구 호출의 입력 JSON, 도구 결과, 훅, 턴 종료, 압축을 낸다.

- 생각(thinking) 블록은 글을 내지 않는다. `Tokens.Output` 에 생각 토큰 수만 싣는다.
- 모델 호출의 첫 블록 항목에 그 호출의 `Tokens` 와 `Model` 을 싣는다.
- 글은 먼저 `usage.MaxMaskInput` 에서 자르고, `usage.Mask` 로 가린 뒤, `usage.MaxTimelineText` 에서 다시 자른다. 가리기 전에 자르는 이유는 큰 도구 결과에서 마스킹이 느려지지 않게 하려는 것이다.
- 도구 입력은 JSON 을 푼 값마다 가린 뒤 다시 직렬화한다. 이스케이프된 따옴표 안의 비밀 값을 놓치지 않기 위해서다. 키 이름이 `usage.SecretKey` 에 맞으면 값을 통째로 가린다.
- 도구 결과 항목은 짝이 되는 호출의 이름, 분류, 걸린 시간을 싣는다. 호출이 앞쪽 쪽에 있을 수 있으므로, 쪽 커서에 아직 닫히지 않은 호출(ID, 이름, 분류, 시각)을 담는다.
- 커서가 줄의 시작을 가리키지 않으면 `usage.ErrBadRequest` 다.
- 화면에 쓰지 않는 종류의 줄은 건너뛴다.

## 5. 저장과 집계

테이블은 `internal/usage/db/schema.sql` 에 있다. `testutil_test.go` 의 `seedBatch` 가 `Apply` 가 만들어야 하는 행의 기준이다.

- **수집 창**: 파일의 수정 시각이 `지금 - windowDays` 보다 새것이거나 이미 커서가 있는 파일만 읽는다.
- **나눠 읽기**: 서비스는 한 파일을 `usage.ScanChunkBytes` 씩 나눠 `Scan` 과 `Apply` 를 되풀이한다. 큰 파일 하나가 쓰기 잠금을 오래 잡지 않게 하려는 것이다.
- **파일이 줄거나 바뀐 경우**: 파일 크기가 커서보다 작으면 처음부터 다시 읽는다. 키가 같아 중복되지 않는다. `BaseOffset` 이 0 인 배치는 건너뛴 줄 수를 더하지 않고 바꿔 쓴다. 크기가 같거나 큰 파일로 통째로 바뀐 경우는 알아채지 못한다. 알려진 한계다.
- **낡은 배치**: `Apply` 는 같은 트랜잭션에서 동의, 제외 여부, 커서 위치를 확인한다. 동의가 없거나, 프로젝트가 제외됐거나, `BaseOffset` 이 0 도 아니고 저장된 커서와도 다르면 `usage.ErrStale` 을 내고 아무것도 쓰지 않는다. 스캔 중에 제외나 지우기가 끼어들어도 지운 행이 되살아나지 않는다.
- **제외**: 프로젝트를 제외하면 그 프로젝트의 행, 커서, 그 세션에 연결된 확장 이벤트를 같은 트랜잭션에서 지운다. 지우기 전에 그 프로젝트의 세션 ID 를 `excluded_session` 에 남긴다. 그래야 나중에 그 세션으로 들어오는 확장 이벤트를 저장하지 않을 수 있다. 스캔은 제외한 프로젝트를 건너뛴다. 다시 포함하면 수집 창 규칙대로 다시 읽는다.
- **지우기와 다시 읽기**: `clearData` 는 동의를 철회하고 모든 사용 자료, 확장 이벤트, 커서를 지운다. 사용자가 다시 동의해야 수집이 시작된다. `rescan` 은 커서만 0 으로 돌려 전부 다시 읽는다. 행은 그대로 있고 다시 읽어도 중복되지 않는다.
- **보존**: 서비스가 한 시간마다 `Prune` 을 부른다. 확장 이벤트는 `usage.ExtRetentionDays` 가 지나면 지우고 `usage.MaxExtEvents` 를 넘으면 오래된 것부터 지운다. 사용 행은 `usage.MaxWindowDays` 가 지나면 지운다.
- **비용 갱신**: `cost-state` 줄에는 시각이 없다. 그 줄이 배치의 첫 줄이면 시각이 0 이 되는데, 파일에서 가장 뒤에 온 줄이므로 저장된 값을 바꾸고 저장된 시각은 그대로 둔다.
- **Coverage**: 읽지 못한 파일이 있거나, 건너뛴 줄이 있거나, 요청 구간이 수집 창보다 하루 넘게 앞에서 시작하면 `partial` 이고 이유를 싣는다. 하루의 여유를 두는 이유는 화면이 "최근 N일"을 자기 시계로 먼저 계산하기 때문이다. 여유가 없으면 창과 같은 N일을 볼 때 항상 구간이 창보다 조금 앞서게 된다. 프로젝트 제외는 사용자의 선택이라 이유가 아니다. 서비스가 동의 전이면 `disabled`, 전체 훑기가 필요해진 때부터 그 훑기가 끝날 때까지 `scanning` 으로 덮어쓴다. 전체 훑기가 필요해지는 때는 첫 실행, 동의, 다시 읽기, 창 넓히기, 프로젝트 다시 포함이다.
- **데이터 버전**: 행이 실제로 바뀔 때만 오른다. 빈 배치와 바뀌지 않은 표시는 올리지 않는다.
- **읽기는 트랜잭션 밖에서**: DSN 의 `_txlock=immediate` 때문에 트랜잭션을 열면 쓰기 잠금을 잡는다. 집계 질의는 `s.db` 로 바로 실행한다.
- **설정 값**: 불리언은 `"1"` 과 `"0"`, 정수는 10진수 문자열이다. 키와 도우미는 `db.go` 에 있다.
- **시계열**: 통은 `loc` 의 시각 경계에 맞춘다. 구간 안의 모든 통을 0 으로 채워 낸다. 통이 400개를 넘으면 통 너비를 넓힌다.
- **백분위**: 도구 지연의 p50, p95 는 닫힌 호출만으로 구한다(nearest-rank). 닫힌 호출이 없으면 `null` 이다.
- **세션**: 구간 안에 모델 호출이나 프롬프트가 있는 루트 세션이다. 하위 에이전트의 수치는 루트 세션에 더한다.

## 6. 서비스와 브리지

`service.Service` 는 `Run` 에서 `Interval` 마다 한 번 훑는다. 동의가 없으면 아무것도 읽지 않는다.

- 루트를 `os.OpenRoot` 로 열어 그 아래만 읽는다. 프로젝트 폴더의 `*.jsonl` 과 `*/subagents/*.jsonl` 을 찾는다. 일반 파일만 읽고 심볼릭 링크, FIFO, 장치 파일은 건너뛴다.
- 파일 이름에서 세션 ID 와 에이전트 ID 를 뽑는다. 루트 기록은 파일 이름이 세션 ID 다. 하위 에이전트 기록은 `subagents` 의 부모 폴더 이름이 세션 ID 이고 `agent-<ID>.jsonl` 의 `<ID>` 가 에이전트 ID 다.
- 파일의 크기와 수정 시각이 마지막으로 본 값과 같으면 열지 않는다. 바뀐 파일만 커서부터 나눠 읽는다.
- 한 파일의 실패는 `MarkUnreadable` 로 남기고 다음 파일로 간다. `usage.ErrStale` 은 실패가 아니다. 그 파일을 이번 훑기에서 그만 읽는다.
- 조각 사이마다 `ctx` 를 확인한다. 앱 종료와 동의 철회가 큰 파일 때문에 늦어지지 않는다.

`Handle(method, params)` 는 `usage.Method*` 이름으로 분배한다. 메서드별 요청과 응답 타입은 `internal/usage/api.go` 의 표에 있다. 변경 메서드는 모두 새 `Status` 를 돌려준다. 다음은 `Handle` 만의 책임이다.

- 구간 집계 응답의 `Coverage` 를 채우고 `disabled`, `scanning` 을 덮는다.
- `session` 과 `timeline` 의 `Source` 를 정한다. 원본이 열리면 `available`, 파일이 없으면 `missing`(쪽은 비어 있고 오류가 아니다), 있지만 못 읽으면 `unreadable` 이다. 저장되지 않은 세션은 `usage.ErrNotFound`, 동의가 없으면 `usage.ErrNoConsent` 다.
- `timeline` 쪽에 그 세션의 확장 이벤트를 `ext` 항목으로 끼운다. `toolUseId` 가 있는 이벤트는 같은 쪽의 그 도구 호출 바로 뒤에 둔다. 없는 이벤트는 시각 순서에 맞는 자리에 두되, 그 쪽의 시간 범위 안에 있을 때만 둔다. 어느 쪽에도 맞지 않은 이벤트는 세션 상세의 확장 이벤트 탭(`extEvents`)에서 모두 볼 수 있다.
- `insights` 는 개요(Coverage 를 채운 것), 도구, 토큰이 많은 세션 200개, 확장 출처와 거부 총수를 모아 `Evaluate` 에 넘긴다.
- `setExtensionEnabled` 는 `ExtensionControl.SetEnabled` 를 부르고 결과를 `Store.SetExtension` 으로 저장한다.
- `clearData` 는 동의를 철회한 뒤 `Store.Clear` 를 부른다.
- 타임라인 읽기는 동시에 `usage.MaxTimelineReads` 건까지만 한다.
- 인코딩 전에 `usage.NonNil` 을 적용한다.

동의는 `Handle` 의 메서드가 아니다. WebView 가 스스로 파일 접근을 허용할 수 없어야 하므로, `Bridge.EnableUsageWatch` 가 네이티브 확인 창을 띄운 뒤 `Service.SetConsent` 를 부른다. 읽는 범위를 넓히거나 되돌릴 수 없는 요청도 브리지가 네이티브 확인 창을 먼저 띄운다. 좁히는 방향은 묻지 않는다.

| 브리지 메서드 | 하는 일 |
|---|---|
| `Usage(method, params string) (string, error)` | `Service.Handle` 로 넘긴다. `setExtensionEnabled`(켜기), `setWindowDays`(지금보다 길게), `setExcluded`(제외 풀기), `clearData` 는 네이티브 확인 창에서 허락받은 뒤에 넘긴다. 거절하면 지금 `Status` 를 그대로 돌려준다. |
| `EnableUsageWatch() (bool, error)` | 네이티브 확인 창 → `SetConsent(true)` |
| `DisableUsageWatch() error` | `SetConsent(false)` |
| `CopyExtensionSettings() error` | 자격을 키체인에 저장하고 설정을 클립보드에 복사 |

데스크톱은 `ExtensionControl` 을 구현한다. 수신기를 켜고 끄며 포트를 돌려준다. 수신기를 켜고 끄는 곳은 이 구현 하나뿐이다. 자격은 설치 키에서 HMAC 으로 만들고 WebView 에 넘기지 않는다. 설치 키는 지금 추적 출처를 처음 붙일 때만 만들어지므로, 자격을 만들기 전에 키가 없으면 새로 만들어 키체인에 저장한다. 키 길이가 32바이트가 아니면 수신기를 열지 않는다. 파생 라벨은 기존 수신기의 것과 다르게 둔다. 키체인의 등록 정보 구조체에는 필드를 더하지 않는다.

사용 분석 저장소를 열지 못해도 앱은 뜬다. 추적 기능은 그대로 동작하고 `Bridge.Usage` 만 오류를 낸다. 종료할 때는 확장 수신기를 닫고, 스캔이 끝나기를 기다린 뒤, 저장소를 닫는다.

## 7. 확장 이벤트 API

제3자 플러그인, MCP 서버, 셸 스크립트가 도구 내부에서 일어난 일을 보낸다.

```
POST http://127.0.0.1:<port>/v1/events
Authorization: Bearer <token>
Content-Type: application/json
```

본문은 이벤트 하나이거나 `{"events":[...]}` 이다. 한 요청에 100개, 256KiB 까지다.

```json
{
  "version": 1,
  "source": "deploy-script",
  "kind": "span",
  "name": "upload",
  "timestamp": "2026-01-01T01:00:00.000Z",
  "duration_ms": 812.5,
  "status": "error",
  "level": "error",
  "session_id": "<Claude Code 세션 ID>",
  "tool_use_id": "<도구 호출 ID>",
  "trace_id": "", "span_id": "", "parent_span_id": "",
  "message": "exit status 1",
  "attributes": {"step": "3", "retries": 2},
  "value": 1.0, "unit": "count"
}
```

| 필드 | 규칙 |
|---|---|
| `version` | 필수. `1`. |
| `source` | 필수. `^[A-Za-z0-9._-]{1,64}$`. |
| `kind` | 필수. `span`, `log`, `metric`. |
| `name` | 필수. 1~128자. |
| `timestamp` | 선택. RFC 3339. 없으면 받은 시각. |
| `status` | 선택. `ok`, `error`, `unknown`. 기본 `unknown`. |
| `level` | 선택. `debug`, `info`, `warn`, `error`. 기본 `info`. |
| `session_id`, `tool_use_id`, `trace_id`, `span_id`, `parent_span_id` | 선택. `^[A-Za-z0-9._:-]{1,128}$`. |
| `message` | 선택. 마스킹한 뒤 2,048자에서 자른다. |
| `attributes` | 선택. 32개까지. 키는 `^[A-Za-z0-9._-]{1,64}$`. 값은 문자열, 숫자, 불리언이고 문자열로 저장한다. 마스킹한 뒤 256자에서 자른다. |
| `duration_ms`, `value` | 선택. 0 이상의 유한한 수. |
| `unit` | 선택. 16자까지. |

전송 형식의 Go 타입과 상한값은 `internal/extension/wire.go` 에 있다. 수신기와 `trace-emit` 이 같은 타입을 쓴다.

응답은 `202` 와 `{"accepted":n,"rejected":[{"index":i,"reason":"..."}]}` 이다. 일부만 틀려도 맞는 이벤트는 받는다. 거부한 입력은 `CountRejected` 로 세어 화면에 보인다. 조용히 버리지 않는다. 거부는 인증을 통과한 요청에서만 센다. 인증에 실패한 요청은 세지 않는다.

**이벤트 ID.** 저장하는 ID 는 수신기가 만든다.

- 보내는 쪽이 `id` 를 넣었으면 `source` 와 `id` 의 해시다. 같은 이벤트를 다시 보내도 한 번만 저장된다.
- `id` 가 없고 `timestamp` 가 있으면 가린 뒤의 모든 필드를 정해진 순서로 이은 해시다. 속성은 키 순서로 정렬한다. 같은 시각의 같은 내용은 중복으로 본다.
- 둘 다 없으면 새 이벤트로 저장한다. 받은 시각은 해시에 넣지 않는다.

**가리기.** `message` 와 `name` 은 `usage.Mask` 를 거친다. 속성은 키 이름이 `usage.SecretKey` 에 맞으면 값을 통째로 가리고, 아니면 값을 `usage.Mask` 에 넣는다.

**요청 검사.** `Host` 가 수신 주소와 같아야 하고, `Origin` 헤더가 없어야 하며, 메서드는 `POST`, 경로는 `/v1/events`, 쿼리는 없어야 한다. `Content-Type` 은 `application/json`, `Authorization` 헤더는 하나여야 한다. 자격 비교는 상수 시간으로 한다. 동시에 4건까지 처리하고 초당 50건을 넘으면 `429` 다. 헤더 읽기, 본문 읽기, 응답 쓰기에 제한 시간을 둔다. 기존 `internal/observer/receiver.go` 의 검사와 같은 수준이다.

`session_id` 와 `tool_use_id` 는 Claude Code 훅의 표준 입력 JSON 에 들어 있다. `trace-emit` 은 `--from-hook` 으로 그 값을 읽는다. 두 값이 없는 이벤트는 "미연계"로 출처 화면에만 나온다.

`trace-emit` 은 두 가지로 쓴다. `trace-emit --name build --status ok --duration-ms 812 --attr step=3` 은 이벤트 하나를 보낸다. `trace-emit run --name build -- make all` 은 명령을 실행하고 걸린 시간과 종료 코드를 span 으로 보낸 뒤 그 종료 코드로 끝난다. 주소와 자격은 `TRACE_EVENTS_URL`, `TRACE_EVENTS_TOKEN` 에서 읽는다. 주소가 `http://127.0.0.1:<포트>/v1/events` 꼴이 아니면 보내지 않는다. 자격과 이벤트가 이 Mac 밖으로 나가지 않게 하려는 것이다. 전송 실패는 원래 작업을 막지 않는다. 전송 제한 시간은 500ms 다. 보낼 때마다 `id` 와 `timestamp` 를 넣는다.

**알려진 한계.** 도구들이 자격 하나를 같이 쓴다. `source`, `session_id`, `tool_use_id` 는 보내는 쪽이 정하므로 한 도구가 다른 출처 이름을 쓸 수 있다. 앱이 꺼져 있을 때 다른 프로세스가 같은 포트를 열면 자격을 받을 수 있다. 자격을 새로 만드는 기능은 이번에 넣지 않는다.

## 8. 인사이트 규칙

규칙은 `usage.InsightInput` 만 읽는 순수 함수다. 각 규칙은 표본이 모자라면 판단하지 않고 건너뛴 것으로 센다. 임계값은 규칙 옆에 이름 붙은 상수로 둔다. 새 규칙은 등록 표 한 곳에 더하면 된다.

| 규칙 | 조건 | 추천 |
|---|---|---|
| 도구 실패율 | 한 도구의 호출이 20회 이상이고 실패율이 15% 이상 | 도구별로 다르다. Bash 는 허용 목록과 샌드박스 설정, Edit 는 먼저 읽기. |
| MCP 서버 실패율 | 서버 호출 10회 이상, 실패율 20% 이상 | 서버 연결과 인증 점검 |
| 느린 훅 | 실행 20회 이상, 평균 1초 이상 | 훅 명령을 가볍게 |
| 실패하는 훅 | 실행 10회 이상, 실패율 10% 이상 | 훅 스크립트 점검 |
| 낮은 캐시 적중률 | 모델 호출 50회 이상, 적중률 50% 미만 | 세션을 주제별로 나누기 |
| 잦은 맥락 압축 | 압축이 2회 이상인 세션이 3개 이상 | 긴 작업을 하위 에이전트나 새 세션으로 나누기 |
| 전용 도구 대신 Bash | `cat`, `grep`, `find`, `ls`, `head`, `tail`, `sed` 합계가 Bash 호출의 30% 이상이고 50회 이상 | Read, Grep, Glob 사용 |
| 반복되는 외부 명령 | 한 프로그램(`gh`, `curl`, `kubectl`, `docker`, `aws`, `psql` 등)이 50회 이상 | 해당 MCP 서버나 스킬 검토 |
| 쓰지 않는 확장 | 세션 20개 이상인데 스킬, MCP, 하위 에이전트 호출이 각각 0 | 종류별로 도입 제안 |
| 토큰이 몰린 세션 | 한 세션이 구간 토큰의 25% 이상이고 세션이 5개 이상 | 해당 세션을 열어 원인 확인 |
| 단일 모델 사용 | 모델 호출 200회 이상이 모두 한 모델 | 단순 작업에 가벼운 모델 검토 |
| 확장 이벤트 오류 | 한 출처의 이벤트 20개 이상, 오류 비율 20% 이상 | 해당 도구 점검 |
| 거부된 확장 입력 | 거부가 1건 이상 | 전송 형식 점검 |

추천 문구에 특정 제품 이름을 지어내지 않는다. "GitHub 용 MCP 서버를 검토하세요"처럼 종류로 말한다.

## 9. 화면

메뉴는 두 묶음이다. 사용 분석이 위에 있고 기본 화면이다.

| 메뉴 | 주소 | 내용 |
|---|---|---|
| 대시보드 | `#dashboard` | 핵심 수치 타일, 일별 토큰 누적 막대, 모델별 사용, 캐시 적중률, 상위 도구와 프로젝트, 요일과 시간 히트맵, 인사이트 3건 |
| 세션 | `#sessions` | 정렬과 검색이 되는 세션 목록 |
| 세션 상세 | `#session/<id>` | 요약, 대화 타임라인, 도구, 토큰 흐름, 하위 에이전트, 확장 이벤트 |
| 도구와 확장 | `#tools` | 도구, MCP 서버, 스킬, 플러그인, 훅, 하위 에이전트, Bash 프로그램 |
| 인사이트 | `#insights` | 인사이트와 추천, 판단한 규칙과 건너뛴 규칙의 수 |
| 확장 이벤트 | `#extensions` | 출처별 현황, 이벤트 목록, 연결 방법 |
| 수집 설정 | `#settings` | 수집 켜기와 끄기, 기간, 제외 프로젝트, 다시 읽기, 지우기, 확장 수신기 |
| 추적 | `#live`, `#overview`, `#tasks`, `#module_groups`, `#sources` | 기존 화면. 동작은 바꾸지 않는다. |

- 셸(`App.tsx`)은 사이드바와 주소 해석만 맡는다. 사용 분석 주소면 `UsagePage` 를, 아니면 기존 추적 작업 공간을 그린다.
- 기존 `App.tsx` 의 연결과 구독 로직은 `src/trace/` 로 옮긴다. `Screen.tsx` 는 화면 종류별 파일로 나눈다. 화면의 글자와 DOM 구조는 바꾸지 않는다. 브라우저 시험(`test/ia.mjs`)이 그것에 기대고 있다.
- 추적 작업 공간은 추적 주소에서만 그린다. 사용 분석 주소에서 함께 떠 있으면 기존 코드가 주소를 추적 주소로 고쳐 쓴다. 반대로 추적 주소에서는 사용 분석 API 를 부르지 않는다. 브라우저 시험의 가짜 브리지에 그 메서드가 없다.
- 각 화면은 `ViewProps` 만 받는다. `api` 로 읽고, `refresh` 가 바뀌면 다시 읽는다. 구간 자료를 보여 주는 화면은 자기 응답의 `coverage` 로 `CoverageNote` 를 그린다. 변경 요청이 돌려준 `Status` 는 `onStatus` 로 넘긴다.
- 원문은 글자로만 그린다. HTML 로 바꾸지 않고, 마크다운을 해석하지 않고, 링크를 자동으로 만들지 않는다. 이미지는 인라인 SVG 만 쓴다. CSP 가 `data:` 이미지를 막는다.
- 화면마다 CSS 클래스에 자기 접두사를 쓴다(`u-dash-`, `u-sess-` 등).
- 계열 색 여섯 개는 색각 이상 시뮬레이션에서 이웃한 색끼리 구분되는지 검사한 값이다. 주황, 청록, 노랑, 분홍은 흰 바탕과의 대비가 3:1 에 못 미치므로, 이 색을 쓰는 차트는 값 표기나 표 보기를 함께 둔다.
- 차트는 SVG 로 직접 그린다. 새 npm 패키지를 넣지 않는다. 색은 `usage.css` 의 `--u-s1`~`--u-s6` 을 순서대로 쓴다. 색만으로 구분하지 않고 범례와 값 표기를 함께 둔다.
- 빈 화면은 `Empty` 로 그린다. "없음", "읽는 중", "읽지 못함"이 다르게 보여야 한다.
- 개발 중에는 `npx vite` 로 `usage-preview.html` 을 열어 모의 데이터로 화면을 본다.

## 10. 출처 종류 등록 표

기존 수집 코드의 출처 종류 7개가 여러 파일에 흩어져 있다. `internal/collect` 에 종류별 속성 표를 두고 나머지 Go 코드가 그 표에서 읽게 한다. 속성은 클라이언트, OTLP 수신 여부, 관찰자 여부, 본문 지원, 부모가 될 수 있는지다. 표를 도는 시험을 두어 새 종류를 빠뜨리면 실패하게 한다. 메타데이터만 저장하는지 보는 시험에 `codex-otel` 을 더한다. 동작은 바꾸지 않는다.

## 11. 보안과 개인정보

- 사용 분석이 읽는 것은 루트 아래의 `.jsonl` 뿐이다. `os.OpenRoot` 로 루트 밖을 열 수 없게 한다. 심볼릭 링크를 따라가지 않는다.
- `usage.sqlite` 는 `0600` 이고 심볼릭 링크면 열지 않는다. 지운 자리를 덮어쓰도록 `secure_delete` 를 켠다. 들어가는 것은 수치, 모델 ID, 도구와 스킬과 플러그인과 MCP 서버 이름, 훅 이름, 프로젝트 경로, 세션 제목, 브랜치 이름, Bash 프로그램 이름이다. 제목은 가린 뒤에 넣고 모든 라벨은 200자에서 자른다.
- 세션 제목은 대화를 요약한 글이고, 경로와 브랜치 이름에는 사람이나 작업의 이름이 들어갈 수 있다. 첫 훑기 전에는 프로젝트를 제외할 수 없으므로 민감한 프로젝트도 제목과 경로가 한 번은 들어간다. 제외하면 지워진다. 알려진 한계다.
- 타임라인 원문은 메모리에서만 다루고 가린 뒤 화면으로 보낸다. 마스킹은 보조 장치이고 모든 비밀 값을 잡는다고 보장하지 않는다. 이메일, IP, 호스트 이름, 접두사 없는 16진수 문자열은 가리지 않는다. 화면에 그 한계를 적는다.
- 확장 이벤트의 `message` 와 속성 값은 저장 전에 가리고 자른다.
- 모든 수신기는 `127.0.0.1` 에만 묶인다. 외부로 나가는 호출은 없다.
- 시험 자료에는 비밀 값처럼 보이는 문자열을 그대로 적지 않는다. 실행 중에 이어 붙여 만든다.
- 저장소의 코드, 주석, 시험 자료에 실제 세션 내용이나 개인 경로를 넣지 않는다. 시험 자료는 모두 합성한다.

## 12. 작업 분할

작업마다 고칠 수 있는 파일이 정해져 있다. 다른 작업의 파일과 3장의 계약 파일은 고치지 않는다. 같은 패키지를 나눠 맡은 작업은 비공개 식별자 앞에 자기 영역 이름을 붙여 이름이 겹치지 않게 한다.

| 작업 | 고칠 수 있는 파일 |
|---|---|
| scan | `internal/usage/transcript/**` |
| timeline | `internal/usage/timeline/**` |
| db-ingest | `internal/usage/db/ingest.go`, `settings.go`, `ext_write.go` 와 그 시험 |
| db-aggregate | `internal/usage/db/coverage.go`, `overview.go`, `tools.go`, `projects.go` 와 그 시험 |
| db-sessions | `internal/usage/db/sessions.go`, `ext_read.go` 와 그 시험 |
| insight | `internal/usage/insight/**` |
| extension | `internal/extension/**` 중 `wire.go` 를 뺀 것 |
| emit | `cmd/trace-emit/**`, `docs/extension-api.md` |
| service | `internal/usage/service/service.go` 와 새 파일, 그 시험 (`ports.go`, `wiring_test.go` 제외) |
| desktop | `desktop/usage*.go`, `desktop/main.go` |
| cli | `cmd/trace-usage/**` |
| registry | `internal/collect/**`, `internal/observer/**`, `internal/ingress/**`, `internal/readmodel/decode.go`, `observer.go`, `query.go`, `internal/transport/capabilities.go`, `internal/store/write.go`, `internal/scenario/seed.go`, `desktop/receiver.go` |
| shell | `desktop/frontend/src/App.tsx`, `nav.ts`, `Screen.tsx`, `src/trace/**`, `app.css`, `test/ia.mjs`, `test/release.mjs`, `desktop/ia_test.go`, `desktop/nativecheck_darwin.go` |
| view-* (7개) | `desktop/frontend/src/usage/views/<화면>.tsx`, `views/<화면>/**`, `views/<화면>.css` |

같은 패키지를 나눠 맡은 `db-*` 작업은 시험 파일 이름을 맡은 소스 파일에 맞춘다(`overview.go` 의 시험은 `overview_test.go`). `README.md`, `desktop/Makefile`, 통합 시험은 병렬 작업이 끝난 뒤 통합 단계에서 고친다.

`registry` 는 `desktop/receiver.go` 의 `receiverToken`, `receiverService`, `localReceiver`, `closeReceiver` 이름을 바꾸지 않는다. `desktop` 작업이 그 이름에 기댄다. `desktop/main.go` 와 `desktop/sources.go` 에 있는 종류 참조는 그대로 둔다.

**워크트리 준비.** `frontend/dist` 와 `node_modules` 는 git 이 추적하지 않아 새 워크트리에 없다. `desktop/` 의 Go 코드를 빌드하려면 주 체크아웃의 `desktop/frontend/dist` 를 복사한다(`cp -cR`). 프런트엔드를 빌드하려면 주 체크아웃의 `desktop/frontend/node_modules` 를 복사한다(`cp -cR`). 심볼릭 링크는 쓰지 않는다. 커밋할 때 `git add -A` 를 쓰지 않고 경로를 직접 적는다.

## 13. 완료 기준

1. 루트 모듈과 `desktop` 모듈에서 `go build ./...`, `go vet ./...`, `go test -race -count=1 ./...` 가 통과한다.
2. `desktop/frontend` 에서 `npm run build` 가 통과한다.
3. 기존 시험을 지우거나 약하게 만들지 않았다.
4. 새 패키지마다 정상 경로와 실패 경로 시험이 있다.
5. `trace-usage` 로 실제 `~/.claude/projects` 를 읽어 세션, 토큰, 도구 수치가 나오고 Coverage 가 함께 나온다.
6. 미리보기에서 일곱 화면이 모의 데이터로 그려진다.
7. 통합 시험이 통과한다. 합성 루트를 실제 `Scan`, `db`, `service` 로 끝까지 돌리는 시험, `Apply` 와 `seedBatch` 의 행을 비교하는 시험, 모든 `Handle` 메서드가 빈 저장소에서 `null` 목록을 내지 않는지 보는 시험, 실제 수신기와 저장소를 잇는 확장 이벤트 시험이다.

## 14. 이번에 하지 않는 것

- 기존 GraphQL 응답 도장에 수집 공백을 싣는 일
- Codex 세션의 토큰과 스킬 집계
- Claude Desktop 일반 채팅 수집
- LLM 을 쓰는 분석
- 대화 원문 검색과 장기 보관
- 프런트엔드에 흩어진 출처 종류 목록 정리
- 확장 수신기의 출처별 자격과 자격 재발급
- 통째로 바뀐 세션 파일을 알아채는 일 (크기가 줄어든 경우만 알아챈다)
- 첫 훑기 전에 프로젝트를 제외하는 일
