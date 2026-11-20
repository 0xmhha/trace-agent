# Claude Code 수집 호환성

2026-11-20, macOS 26.5 arm64에서 설치된 Claude Code CLI 2.1.329를 시험했다. 임시 프로젝트와 전용 프로필을 만들고 Messages API만 루프백 모형으로 교체했다. CLI가 직접 Read, Agent, TaskCreate를 실행하고 루트·자식 JSONL을 기록했다. API 서버나 실제 사용자 계정에 요청을 보내는 시험은 포함하지 않았다.

| 수집 시점 | 작업 | 도구 시도 | 식별자가 연결된 자식 |
|---|---:|---:|---:|
| 첫 턴 진행 중 | 1 | 1 | 0 |
| 첫 턴 종료 | 1 | 3 | 1 |
| CLI 프로세스를 다시 띄워 같은 세션의 두 번째 턴 종료 | 2 | 3 | 1 |

진행 중 수치는 응답 전후 파일 플러시 시점에 따라 달라질 수 있다. 첫 턴과 두 번째 턴이 끝난 시점에는 세 도구 시도 모두 같은 ID의 결과가 도착했다. 각 시점에 SQLite를 닫고 다시 열어 파일의 마지막 완결 경계부터 재개했으며 리비전은 그대로였다.

이 버전에서 루트 입력은 외부 사용자, promptId, 일반 텍스트와 isSidechain=false를 기록하고 isMeta를 생략했다. 사용자와 도구 사이에는 여러 attachment가 있었다. 어댑터는 이 봉투에서 세션·레코드·부모 식별자만 남긴다. 이 두 형식을 반영한 뒤 첫 턴의 집계가 작업 0건에서 1건, 시도 0건에서 3건으로 바로잡혔다. 실제 도구 결과의 is_error 생략은 성패 불명으로 둔다. 자식은 Agent 호출과 결과의 자식 ID, 실제 자식 기록까지 맞아야 센다.

저장소 루트에서 재실행한다. 설치된 Claude CLI, Node.js와 Go가 필요하다.

```sh
TRACE_CLAUDE_COMPAT=1 go test -race -count=1 -run '^TestInstalledClaude$' -v ./internal/readmodel
```

[루프백 모델 시나리오](tests/compat/claude.mjs) · [수집과 재시작 시험](../../internal/readmodel/claude_compat_test.go) · [출처별 지원표](source-support.md)
