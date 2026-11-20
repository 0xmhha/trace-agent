# 선택적 Claude Code 훅과 참여 MCP 서버

데이터 계약 v1.3의 R01·R03·R06·R07·R09를 다룬다. 이미 고른 클라이언트 자료원에 관찰 출처를 추가하면 별도 출처 ID, 루프백 주소, 전용 Bearer 자격을 얻는다. 훅은 Claude 기록에, MCP는 Codex app-server·rollout 또는 Claude 기록에 연결할 수 있다. MCP 관측에는 등록할 때 정한 서버 이름을 사용한다. 관찰 출처에는 개별 동의·보존·삭제 정책이 적용된다. 이 수신기는 메타데이터만 저장한다.

## Claude Code

설정에서 제공한 주소를 각 이벤트의 HTTP 훅에 사용한다. 다음은 PreToolUse용 예시다. PostToolUse와 PostToolUseFailure에도 같은 핸들러를 두고 환경 변수에는 이 훅의 전용 자격을 설정한다. 포트 43190은 실행 중인 앱이 알려 준 포트로 교체한다. onFailure가 continue이므로 수신기가 중단돼도 원래 도구는 실행을 계속한다. [Claude Code 훅 규격](https://code.claude.com/docs/en/hooks)

```json
{
  "hooks": {
    "PreToolUse": [{
      "hooks": [{
        "type": "http",
        "url": "http://127.0.0.1:43190/v1/hooks",
        "timeout": 1,
        "onFailure": "continue",
        "headers": {"Authorization": "Bearer $TRACE_OBSERVER_TOKEN"},
        "allowedEnvVars": ["TRACE_OBSERVER_TOKEN"]
      }]
    }]
  }
}
```

도구 입력·결과를 프로세스 밖으로 보내기 전에 제거하려면 저장소 루트에서 `go build -o trace-hook ./cmd/trace-hook`으로 전달기를 만들고 세 이벤트의 command 훅에서 이 파일의 절대 경로를 호출한다. TRACE_OBSERVER_URL과 TRACE_OBSERVER_TOKEN을 전달한다. 실행 파일은 표준 입력의 이벤트 이름·세션 ID·호출 ID·선택 시각만 골라 전송한다. 최대 500ms 안에 승인·거부 판단이나 메시지 없이 종료한다. 수신기·자격·입력이 없을 때도 종료 코드는 0이다.

PreToolUse는 클라이언트가 관찰한 호출 제안이다. PostToolUse와 PostToolUseFailure는 클라이언트가 관찰한 결과다. 원천 출처·프로필·세션·tool_use_id의 조합이 유일할 때 기존 시도에 근거가 추가된다.

## 참여 MCP 서버의 HTTP 입력

서버 전용 /v1/mcp 주소에 application/json과 전용 Authorization 헤더를 사용한다. 아래 세 행은 각각 독립된 POST 본문이다. server에는 등록한 이름, connection_id에는 RPC 연결 구분자, rpc_id에는 그 연결의 요청 식별자를 넣는다. 숫자 ID와 문자열 ID는 구분한다. timestamp는 UTC 오프셋이 있는 RFC 3339 시각, clock_id는 해당 시계의 식별자다.

```jsonl
{"version":1,"server":"sample","connection_id":"connection-1","rpc_id":4,"clock_id":"sample-clock","timestamp":"2025-01-01T01:00:00.000Z","action":"bridge","session_id":"session-A","client_call_id":"client-call-B"}
{"version":1,"server":"sample","connection_id":"connection-1","rpc_id":4,"clock_id":"sample-clock","timestamp":"2025-01-01T01:00:00.001Z","action":"receive"}
{"version":1,"server":"sample","connection_id":"connection-1","rpc_id":4,"clock_id":"sample-clock","timestamp":"2025-01-01T01:00:00.080Z","action":"return","is_error":false}
```

브리지에는 클라이언트와 서버 양쪽에서 관찰한 호출 키가 필요하다. turn_id도 알고 있으면 함께 전송한다. 수신기와 원천에 같은 서버·연결·RPC·클라이언트 키를 확인할 수 있을 때 도구의 수신과 반환이 각각 DIRECT가 된다. 이름·시각만 맞거나 후보가 2개면 미연계 근거로 남는다. 참인 is_error는 도구 오류, 정수 rpc_error_code는 JSON-RPC 오류다. 해당 상태가 없으면 성공 여부는 UNKNOWN이다. 시계가 다른 서버 반환과 클라이언트 시작의 차이를 지연 표본에 포함하지 않는다. 참여 수집 코드는 관찰 전송 오류를 작업 결과와 독립적으로 처리해야 한다.

## 검증 결과

| 경로 | 직접 확인한 결과 |
|---|---|
| 설치된 Claude Code 2.1.329 | 임시 프로필과 로컬 모델 응답기를 사용했다. Claude의 HTTP 훅이 세 도구 시도에 6건을 보냈고 PreToolUse 3건이 해당 원천 시도와 연결됐다. |
| 합성 MCP → HTTP → SQLite → 시도 상세 | 호출당 1회로 유지됐다. 반환이 먼저 도착해도 브리지 전에는 수신·반환 구간이 UNKNOWN이고, 같은 연결의 브리지가 생기면 DIRECT 두 건과 RPC_ERROR가 됐다. 다른 연결의 브리지와 중복 훅은 집계를 늘리지 않았다. 같은 키의 원천 후보가 2개가 되자 관찰 4건을 미연계로 돌렸다. 서로 다른 시계를 섞은 지연 표본은 0개였다. |
| 접근·정제 | 출처 등록 전 요청은 403, 다른 출처 자격은 401이었다. 도구 입력·오류 자유문·서버 이름을 SQLite 메타데이터에서 찾을 수 없었다. 전달기도 입력을 전송 전에 정제했다. |

참여 MCP의 결과는 명시적 브리지를 내보내는 합성 계측 입력으로 확인했다. 별도 계측을 하지 않은 외부 MCP 서버의 내부 수신·반환은 미확인이다.

```sh
go test -race -count=1 ./cmd/trace-hook ./internal/observer
TRACE_HOOK_COMPAT=1 go test -race -count=1 -v -run TestInstalledClaudeHooks ./internal/observer
```
