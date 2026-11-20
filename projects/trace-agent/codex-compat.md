# 설치된 Codex CLI 호환성 시험

2026-11-20 macOS 26.5 (25F71, arm64), Go 1.26.11, codex-cli 0.169.1에서 실행했다. 대상은 R01~R04, R07~R09다. 어댑터 출력 계약은 2.1.0, SQLite는 13, 읽기 계약은 v1.3이다. [실행 가능한 시험](tests/compat/codex_test.go)

실행할 때마다 새 시험 디렉터리에 사용자 구성과 인증 정보가 없는 Codex 프로필을 만들었다. 설치된 CLI가 읽기 전용 샌드박스에서 일반 도구를 실행하고 실제 자식 스레드를 만들었다. 모델 응답만 고정된 루프백 Responses 서버가 제공했다. 첫 턴 실행 중 같은 프로필의 완결된 rollout 줄을 읽고, 부모 스레드의 실제 ID로 새 CLI 프로세스에서 두 번째 턴을 시작했다.

| 경로 | 기대 | 관찰 |
|---|---|---|
| 진행 중 수집 | 부모가 다음 모델 응답을 받기 전에도 완결된 근거가 보인다 | 로컬 서버가 두 번째 요청을 잡고 있는 동안 14개 근거를 읽었다 |
| 일반 도구 | 실제 명령이 종료 코드 0과 합성 표식을 돌려준다 | `exec_command` 호출 및 결과, CLI의 `command_execution/completed`, 표식 일치 |
| 자식 생성 | 생성 도구가 반환한 자식과 별도 rollout의 부모가 같은 작업에 연결된다 | `multi_agent_v1.spawn_agent`에서 자식 ID를 받았다. 실제 부모·자식 파일 2개, 도구 시도 2개, 배정된 하위 1개 |
| 같은 스레드의 새 턴 | 첫 부모 스레드 ID를 명시해 재개하고 새 근거를 읽는다 | 첫 부모 턴, 자식 턴, 재개한 부모 턴까지 3개의 스레드·턴 조합. 명시한 부모와 재개 결과의 스레드 ID 일치 |
| 수집기 재시작 | SQLite를 닫고 재개해 같은 원천을 모두 다시 확인해도 추가 근거 0개 | 경합 검사를 켠 최종 두 실행에서 각각 첫 턴 완료 뒤 근거 40개, 부모 재개 뒤 51개, 재처리 뒤 51개·동일 리비전. 이전 두 실행은 각각 39개·50개·50개였다. CLI 자체 진단 레코드 수는 실행마다 달라질 수 있어 시험은 재시작 전후의 동일성을 대조한다 |
| 기존 이력과 app-server 재시작 | 검증한 프로필·작업 디렉터리를 읽기 전용으로 조회한 뒤 새 app-server에서 다시 읽는다 | 매회 이력 13개. 스레드 2개, 턴 3개, 사용자·에이전트 메시지 각 3개, 명령 실행 1개, 협업 호출 1개. 재시작 후 리비전 13 유지 |

초기 검증에서 일반 도구 이름으로 시도한 `update_plan`은 이 설치본의 구성에서 지원하지 않았다. 자식 생성으로 협업 경로를 검증했으며, 계획 이벤트 수신은 1단계 합성 app-server 표본으로 확인한 범위다. Codex Desktop 창에서 직접 시작한 세션, 외부 모델 제공자, MCP 서버, 다른 버전은 이 시험에서 확인하지 않았다.

저장소 루트에서 재실행한다. 시험은 PATH에 있는 설치본을 사용하고 임시 프로필과 DB를 종료 후 정리한다.

```sh
TRACE_CODEX_COMPAT=1 go test -race -count=2 -v -run '^TestInstalledCodexCLI$' ./projects/trace-agent/tests/compat
```


## 로컬 OTel 수신

Codex CLI 0.169.1이 임시 프로필에서 실행한 합성 턴과 자식 작업으로 확인했다. 모델 응답은 루프백 시험 서버가 제공했으며 사용자 홈, 모델 인증 키, 외부 모델 서비스는 사용하지 않았다. 앱에 등록한 `codex-otel` 출처 하나가 전용 포트와 송신 자격 하나를 쓴다. CLI 프로세스를 새 설정으로 시작한 뒤 내보낸 이벤트부터 수집한다. 설정 이전의 기록을 요청할 API는 없다.

| CLI 설정 | 직접 관찰한 결과 |
|---|---|
| `otel.exporter.otlp-http.protocol="json"` | `POST /v1/logs`, `application/json`. 한 실행에서 근거 28건, API 요청 3건, 도구 결과 2건. 본문 수집에 동의했어도 `otel.log_user_prompt=false`이면 사용자 입력 슬롯 0개 |
| `otel.exporter.otlp-http.protocol="binary"` | 같은 경로, `application/x-protobuf`. 한 실행에서 근거 35건, API 요청 4건, 도구 결과 2건. 동의와 `otel.log_user_prompt=true`를 함께 적용했을 때 정제된 사용자 입력 2개 |
| 인증과 대상 | `otel.exporter.otlp-http.endpoint`에 해당 수신기의 전체 HTTP 로그 주소를 지정한다. `OTEL_EXPORTER_OTLP_LOGS_HEADERS` 환경변수에 `Authorization=Bearer%20`와 송신 토큰을 이어 붙였을 때 기존 인증을 통과했다. 앱에서 복사하는 설정은 키체인의 송신 자격을 읽는다 |
| 재전송과 제한 | 원래 SDK 관찰 시각이 다른 같은 밀리초의 API 관찰 2건을 보존했다. 응답 유실 후 순서와 인코딩을 바꿔 재전송해도 근거는 2건이었다. 필수 시각이 없으면 OTLP 응답의 거부 건수에 포함한다 |

최종 두 번의 반복 시험에서는 매회 두 전송 형식 모두 근거 35건을 저장했다. API 요청 4건, 도구 결과 2건을 확인했다. JSON은 프롬프트 슬롯 0개, binary는 2개였다. 실행마다 자식이 종료될 때까지 SDK가 보낸 이벤트 수는 달라질 수 있다. 확인한 종류는 `codex.conversation_starts`, `codex.startup_phase`, `codex.user_prompt`, `codex.api_request`, `codex.sse_event`, `codex.turn_ttft`, `codex.tool_decision`, `codex.tool_result`다. 이들은 `codex_otel.log_only` 계측 범위에서 전송됐다. 다른 계측 범위의 SDK 진단 메시지는 수집 대상에서 제외한다.

API 이벤트의 `attempt`는 0부터 시작하며 다른 요청에서 다시 0이 된다. 출처의 미연계 근거 상세에 HTTP 상태 코드와 CLI가 보고한 `duration_ms`를 함께 표시한다. 도구 결과에 `success`가 있으면 성공 또는 실패를 표시한다. 대화 ID, 모델, 호출 ID는 가명 처리하고 URL, 인증 속성, SDK의 호스트명, 도구 인자와 출력은 저장하지 않는다. 프롬프트는 CLI 옵션과 출처 동의가 모두 유효할 때만 기존 정제기를 거친다.

이 버전의 API 이벤트에는 개별 모델 왕복을 짝지을 요청 키가 없다. HTTP 응답 상태만으로 전체 스트림의 성공이나 모델 서버의 수신 시각을 결정할 수 없다. 근거 위치는 출처, 계측 범위, 대화 ID, 사건 이름과 시각, SDK의 원래 나노초 관찰 시각, 이벤트가 제공한 호출·구분 속성으로 구분한다. SDK가 같은 시각과 같은 식별 속성으로 두 관찰을 보고하면 서로 구분할 수 없다. 데스크톱 GUI가 보낸 이벤트, OTLP/gRPC, 메트릭·트레이스, 다른 CLI 버전은 이 시험에서 확인하지 않았다.

```sh
TRACE_CODEX_COMPAT=1 go test -race -count=1 -v -run '^TestInstalledCodexOTLP$' ./projects/trace-agent/tests/compat
go test -race -count=1 -run CodexOTLP ./internal/ingress ./internal/transport
```
