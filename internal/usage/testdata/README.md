# 사용 분석 시험 자료

모두 합성한 자료다. 실제 세션 내용, 경로, 비밀 값은 없다. 필드 배치는 Claude Code 2.1.x 세션 기록의 구조(필드 이름과 값의 종류)를 따른다. 비밀 값처럼 보이는 문자열은 넣지 않는다. 마스킹 시험은 입력을 실행 중에 만들어 쓴다.

실제 배치에서는 `session.jsonl` 이 `<프로젝트>/11111111-1111-4111-8111-111111111111.jsonl` 이고, `agent-a1b2c3.jsonl` 이 `<프로젝트>/11111111-1111-4111-8111-111111111111/subagents/agent-a1b2c3.jsonl` 이다.

## session.jsonl (루트, 28줄)

| 항목 | 기대값 |
|---|---|
| 세션 | ID `11111111-1111-4111-8111-111111111111`, 경로 `/work/sample`, 제목 `Fix failing build`, entrypoint `cli`, 버전 `2.1.329`, 브랜치 `main` |
| 시각 | 처음 2026-03-01T09:00:00Z, 마지막 2026-03-01T09:32:00Z |
| 모델 호출 | 6회 (`msg_01`~`msg_06`). `msg_01` 은 세 줄에 걸쳐 반복된다. 모델 `model-large` |
| 토큰 합계 | 입력 38, 출력 1,770, 캐시 읽기 305,100, 캐시 생성 2,680, 생각 150 |
| 귀속 | `msg_04` 는 스킬 `deploy-check`, 플러그인 `devkit` |
| 도구 호출 | 5회. `Bash`(builtin, 대상 `tail`), `mcp__issues__search`(mcp, 서버 `issues`, 도구 `search`), `Skill`(skill, 스킬 `devkit:deploy-check`, 플러그인 `devkit`), `mcp__plugin_devkit_chain__status`(mcp, 플러그인 `devkit`, 서버 `chain`, 도구 `status`), `Agent`(agent, 종류 `Explore`) |
| 도구 결과 | 5회. `toolu_01` 만 오류. 보고된 지연 없음 |
| 프롬프트 | 2회 (`u-prompt-1`, `u-prompt-2`). `isMeta` 줄과 도구 결과 줄은 세지 않는다 |
| 훅 | 4회. 성공 3 (`SessionStart:startup`, `PreToolUse:Bash`, `Stop`), 오류 1 (`hook_non_blocking_error`, 종료 코드 1, 900ms). `stop_hook_summary` 줄은 세지 않는다 |
| 턴 종료 | 1회, 70,000ms, 메시지 12 |
| 압축 | 1회, 계기 `auto`, 직전 토큰 150,000 |
| 비용 | 1.25 USD, API 42,000ms, 도구 4,300ms, 추가 12줄, 삭제 3줄. `cost-state` 줄에는 시각이 없다 |
| 건너뛴 줄 | 1 (24번째 줄이 깨진 JSON) |
| 다른 세션 줄 | 1 (마지막 줄의 `sessionId` 가 다르다) |

## agent-a1b2c3.jsonl (하위 에이전트, 5줄)

| 항목 | 기대값 |
|---|---|
| 스트림 | 세션은 위와 같고 에이전트 ID `a1b2c3`, 종류 `Explore` |
| 모델 호출 | 2회. 입력 14, 출력 200, 캐시 읽기 18,500, 캐시 생성 520 |
| 도구 | `WebFetch` 1회, 결과의 보고된 지연 850ms |
| 훅 | 성공 1 (`PreToolUse:WebFetch`) |
| 프롬프트 | 0. 하위 에이전트의 첫 user 줄은 사람이 쓴 것이 아니라서 세지 않는다 |

## 확인한 훅 종류

로컬 기록에서 본 `attachment.type` 은 `hook_success`, `hook_non_blocking_error`, `hook_cancelled` 다. 상태는 `hook_success` 가 성공, 이름에 `non_blocking_error` 나 `cancelled` 가 있으면 오류, `blocking_error` 만 있으면 차단, 그 밖의 `hook_` 종류는 불명으로 둔다. `hook_blocking_error` 는 표본에서 보지 못했다.
