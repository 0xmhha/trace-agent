# Claude Code 로컬 OTLP 수신 검증

R01~R03, R07~R09에 해당한다. 2026-11-20 macOS 26.5 arm64에서 실제 Claude Code CLI 2.1.329를 임시 프로필로 실행했다. 프로토콜은 OTLP/HTTP Logs이며 HTTP/protobuf와 HTTP/JSON을 지원한다. 수집기의 메타데이터 계약은 2.1.0, SQLite 버전은 13이다. [수신기와 시험](../../internal/ingress/) · [CLI 시험 입력](tests/compat/claude.mjs)

## 출처와 개인정보

출처마다 전용 루프백 포트와 인증 토큰을 사용한다. 선택한 출처가 등록되고 활성화된 이후에만 POST 요청을 처리한다. 수신기의 정확한 Host와 Authorization을 검사하고 Origin이 있는 브라우저 요청은 거부한다. 네이티브 설정은 출처의 포트와 자격을 재사용한다. 내보내는 주소는 해당 수신기의 `/v1/logs`다. CLI에서 내보내기를 켠 뒤에 생긴 이벤트를 수신한다. [Claude Code 계측](https://code.claude.com/docs/en/monitoring-usage)

선택한 출처에 현재 동의가 있을 때만 원문 속성 중 지원하는 필드를 정제기로 넘긴다. 저장기에서 사건 시각의 동의와 현재 동의를 다시 검사한다. CLI의 원문 옵션은 사용자가 선택한다. 이 옵션은 API 요청과 응답에 섞인 대화, 도구, 입력까지 SDK에서 노출할 수 있다. 시험에서는 합성 데이터만 있는 프로필에 로컬 수신 주소를 확인한 뒤 옵션을 켰다. [Claude Code 본문 이벤트](https://code.claude.com/docs/en/monitoring-usage#api-request-body-event)

## 커밋과 재시도

단일 출처는 동시에 한 내보내기를 처리한다. 대기 중인 다른 요청에는 429와 Retry-After를 보낸다. 압축 전후 4MiB와 최대 1,024개 기록을 검사하고 SQLite에는 최대 100개씩 커밋한다. 전부 끝난 뒤 성공 응답을 쓴다. 수신한 기록 위치는 출처·계측 범위·세션·원래 사건 시각·이벤트 이름·원래 순번으로 결정한다. 순번은 프로세스 재시작 때 반복될 수 있다. 요청 순서와 인코딩이 바뀌어도 재시도 결과가 같은지 시험한다. 위치가 불충분한 레코드는 OTLP partial success 응답의 거부 건수로 표시한다. [OTLP 규약](https://opentelemetry.io/docs/specs/otlp/) · [Claude Code 순번 정의](https://code.claude.com/docs/en/monitoring-usage#event-correlation-attributes)

## 결과

| 시험 | 기대값 | 실제 결과 |
|---|---|---|
| 실제 CLI의 임시 세션에서 HTTP/protobuf, HTTP/JSON 각각 사용 | 내장 SDK가 메타데이터와 동의한 본문 이벤트를 송신 | 프로토콜별 근거 38개, API 요청·응답 본문 각 6개, tool_result 3개, 사용자 입력 2개, 정제된 본문 슬롯 18개. 지원하지 않는 종류 9개는 원문 없는 표시만 남김 |
| C1 응답이 먼저 도착한 묶음을 커밋한 뒤 응답 유실, 순서와 형식을 바꿔 재전송 | 근거 2개 유지 | protobuf로 커밋한 뒤 재시도 응답을 내고, JSON으로 다시 보냈을 때 중복 0개 |
| 동의 꺼짐과 켜짐 | 동의 전 원문·토큰 저장 0건, 동의 뒤 지원하는 필드에만 정제 본문 | DB·WAL·SHM에서 합성 비밀과 토큰 0건. 동의 뒤 gzip으로 보낸 응답의 지원 슬롯 1개 |
| 등록 전, 수집 중지, 잘못된 토큰·Origin·Host·과대 입력 | 수집 거부 | 등록 전·중지·Origin·Host 403, 토큰 401, 1,025개 또는 4MiB 초과 413 |

재실행하려면 저장소 루트에서 아래 명령을 사용한다. 설치본 시험의 합성 Messages 응답과 OTLP 내보내기는 모두 루프백으로 전달한다.

```sh
go test -race -count=1 ./internal/ingress
TRACE_CLAUDE_OTLP_COMPAT=1 go test -race -count=1 -v -run TestInstalledClaudeOTLP ./internal/ingress
```

Codex OTel, Claude Code 데스크톱 GUI에서의 계측 설정, 다른 CLI 버전, 외부 모델 제공자는 이 경로의 확인 범위 밖이다.
