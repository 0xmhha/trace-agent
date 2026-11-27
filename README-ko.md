# Trace Agent

[English](README.md) | 한국어

macOS에서 Claude Code를 어떻게 쓰고 있는지 보여 주는 앱이다. 세션 기록을 읽어 토큰, 모델, 도구, 스킬, 플러그인, MCP 서버, 훅의 사용 현황을 대시보드로 보여 주고, 한 세션의 대화와 도구 호출을 따라가며 디버깅할 수 있게 한다. 제3자 도구가 보낸 실행 정보도 같은 타임라인에 함께 보여 준다. 모든 자료는 이 Mac 안에 머문다.

Codex와 Claude Code의 작업 이력을 근거 단위로 추적하는 기존 기능은 **추적** 메뉴에 그대로 있다. 작업에서 하위 에이전트, 도구 호출과 결과, 각 판정에 사용한 근거까지 따라갈 수 있고, 화면을 고정하면 같은 리비전의 작업과 통계를 다시 볼 수 있다.

## 기능

| 메뉴 | 보여 주는 것 |
|---|---|
| 대시보드 | 세션, 프롬프트, 토큰, 캐시 적중률, 비용, 일별 추이, 모델별 사용, 많이 쓴 도구와 프로젝트, 요일과 시간별 활동, 인사이트 요약 |
| 세션 | 기간 안의 세션 목록. 토큰, 실패, 비용으로 정렬하고 제목과 경로로 찾는다 |
| 세션 상세 | 대화 타임라인(프롬프트, 응답, 도구 호출과 결과, 훅, 확장 이벤트), 도구별 집계, 토큰 흐름, 하위 에이전트 |
| 도구와 확장 | 도구, MCP 서버, 스킬, 플러그인, 훅, 하위 에이전트, Bash 프로그램별 호출과 실패율, 지연 |
| 인사이트 | 사용 습관에서 찾은 문제와 추천. 판단한 규칙, 통과한 규칙, 표본이 모자라 건너뛴 규칙을 나눠 보여 준다 |
| 확장 이벤트 | 제3자 도구가 보낸 이벤트와 출처별 현황, 연결 방법 |
| 수집 설정 | 수집 켜기와 끄기, 기간, 제외할 프로젝트, 다시 읽기, 지우기 |
| 추적 | 실시간, 개요, 작업, 모듈, 출처와 설정 (기존 화면) |

화면은 기본으로 영어이고 한국어로 바꿀 수 있다.

## 수집하는 것과 하지 않는 것

처음 실행하면 `~/.claude/projects` 를 읽어도 되는지 한 번 묻는다. 허락하면 최근 30일의 세션 기록부터 읽고, 그 뒤로 새 세션을 자동으로 따라간다.

저장하는 것:

- 앱의 저장소에는 수치와 이름만 남는다. 토큰 수, 모델 ID, 도구와 스킬과 플러그인과 MCP 서버 이름, 훅 이름, 프로젝트 경로, 세션 제목, 브랜치 이름, Bash 명령의 프로그램 이름이다.

저장하지 않고 보내지 않는 것:

- 대화 원문과 도구의 입력, 출력은 저장하지 않는다. 세션 상세를 열 때 원본 세션 파일에서 바로 읽어 보여 준다. 원본이 지워진 세션은 수치만 남는다.
- 읽은 내용을 이 Mac 밖으로 보내지 않는다. 인사이트와 추천은 로컬 규칙으로 만든다.
- 비용은 Claude Code가 세션 기록에 적은 값만 쓴다. 토큰에서 추정하지 않는다.

알아 둘 한계:

- 화면에 내기 전에 비밀 값으로 보이는 문자열을 가린다. 보조 장치이며 모든 비밀 값을 잡는다고 보장하지 않는다. 이메일 주소, IP 주소, 호스트 이름은 가리지 않는다.
- 수치가 불완전할 수 있으면 화면이 그렇게 말한다. 읽지 못한 파일, 건너뛴 줄, 수집 기간 밖의 구간이 있으면 "부분 집계"로 표시한다.

설계와 결정의 배경은 [사용 분석 리팩토링 설계](docs/design/usage-refactor.md)에 있다.

## 필요한 것

- Xcode Command Line Tools가 설치된 macOS
- Go 1.25 이상
- Node.js와 npm

## 빌드와 실행

저장소 루트에서 실행한다.

```sh
make -C desktop package
open "desktop/build/bin/Trace Agent.app"
```

사용 분석은 첫 화면의 **수집 시작**으로 켠다.

추적 기능의 출처는 따로 고른다. 앱에서 **추적 → 출처와 설정 → 출처 선택**을 연다. Codex는 프로젝트 이력이나 rollout 파일을, Claude Code는 프로젝트 세션 폴더나 JSONL 파일을 선택한다. Claude의 자식 세션까지 보려면 루트와 자식 기록을 함께 담은 세션 폴더를 선택한다. 수집 범위와 보존 기간은 출처별로 조정할 수 있다. 기본 수집 범위는 메타데이터다.

Codex와 Claude Code의 OTel 기록은 같은 창에서 **로컬 OTel 수신기**를 추가해 받을 수 있다. 수신기 상세에서 터미널 설정을 복사하고 새 CLI 세션에 적용한다. 키체인이 송신 자격을 보관하고, 동의한 출처에서만 지원하는 본문 필드를 정제한다. 참여 MCP 서버와 Claude 훅은 기존 클라이언트 출처의 **추가 관찰 출처**에서 연결한다. [Claude OTel](projects/trace-agent/otlp-compat.md) · [Codex CLI와 OTel](projects/trace-agent/codex-compat.md) · [훅과 MCP 송신 형식](projects/trace-agent/observer-sdd.md)

## 명령줄 도구

두 명령은 다음으로 빌드한다.

```sh
make -C desktop tools
```

### `trace-emit`

셸, 스크립트, 훅에서 앱으로 확장 이벤트를 보낸다.

```sh
desktop/build/bin/trace-emit --name build --status ok --duration-ms 812
desktop/build/bin/trace-emit run --name deploy -- ./deploy.sh
```

### `trace-usage`

같은 파이프라인을 터미널에서 돌려 JSON 으로 출력한다. 자기 임시 저장소만 쓰고 앱의 저장소는 건드리지 않는다.

```sh
desktop/build/bin/trace-usage --days 30 --pretty
```

## 제3자 도구의 실행 정보 받기

세션 기록에는 Claude Code가 도구를 부른 사실까지만 남는다. 도구 안에서 무슨 일이 있었는지는 그 도구가 알려 줘야 한다. 앱은 이 Mac 안에서만 열리는 수신 주소(`/v1/events`)를 제공하고, 플러그인, MCP 서버, 스크립트가 여기에 이벤트를 보내면 세션과 도구 호출에 연결해 타임라인에 보여 준다.

**확장 이벤트** 화면에서 수신기를 켜고 설정을 복사한다. 셸에서는 위의 `trace-emit` 명령으로 보낼 수 있다.

전송 형식, 훅과 연결하는 방법, 제한은 [확장 이벤트 API](docs/extension-api-ko.md)에 있다. 영어판은 [Extension Events API](docs/extension-api.md)다.

## 검증

```sh
node projects/trace-agent/tests/verify.mjs
go test -race -count=1 ./...
make -C desktop check
(cd desktop && TRACE_IA_BROWSER=1 go test -count=1 -run '^TestDesktop(IA|UsageIA)$' .)
```

마지막 명령은 Chrome에서 추적 화면과 사용 분석 화면을 실제로 열어 확인한다. 사용 분석 쪽은 합성 세션 기록을 실제 파이프라인으로 읽은 수치를 화면에서 대조한다.

| 설계 단계 | 검증 기록 |
|---|---|
| 1–2. 출처와 어댑터 | [출처별 지원표](projects/trace-agent/source-support.md) · [데이터 계약](projects/trace-agent/stage-1-sdd.md) · [수집과 재개](projects/trace-agent/stage-2-sdd.md) |
| 3–4. SQLite와 사실 조회 | [저장·삭제·장애 복구](projects/trace-agent/stage-3-sdd.md) · [사실과 통계](projects/trace-agent/stage-4-sdd.md) |
| 5–6. 전송과 시퀀스 | [GraphQL과 WebSocket](projects/trace-agent/stage-5-sdd.md) · [고정 시점의 시퀀스](projects/trace-agent/stage-6-sdd.md) |
| 7–8. 화면과 설치물 | [화면 인수 시험](projects/trace-agent/stage-7-sdd.md) · [패키지와 성능](projects/trace-agent/stage-8-sdd.md) |

## 저장소 구성

| 경로 | 내용 |
|---|---|
| `cmd/trace-emit` | 확장 이벤트를 보내는 명령 |
| `cmd/trace-usage` | 앱 없이 사용 집계를 출력하는 명령 |
| `cmd/trace-hook` | Claude Code 훅의 최소 메타데이터를 로컬 추적 수신기로 넘기는 명령 |
| `internal/usage` | 사용 분석 파이프라인. `transcript`(해석), `db`(저장과 집계), `timeline`(세션 상세), `insight`(로컬 규칙), `service`(예약과 요청 처리) |
| `internal/extension` | 확장 이벤트를 받는 루프백 수신기 |
| `internal/collect`, `internal/ingress`, `internal/observer` | 추적 출처. 로컬 파일, OTLP 수신기, 훅과 MCP 관찰기 |
| `internal/store`, `internal/readmodel`, `internal/transport` | 추적 근거 저장소, 읽기 모델, 데스크톱 화면이 쓰는 GraphQL 전송 |
| `desktop` | Wails 데스크톱 앱(Go)과 `desktop/frontend` 의 React 화면 |
| `docs` | API 안내와 설계 문서 |
| `projects/trace-agent` | 추적 기능의 단계별 명세, 시험 자료, 검증 스크립트 |

## 문서

- [확장 이벤트 API](docs/extension-api-ko.md) ([English](docs/extension-api.md))
- [사용 분석 리팩토링 설계](docs/design/usage-refactor.md)
- [다국어 지원](docs/design/i18n.md) (영어)
- [출처별 지원표](projects/trace-agent/source-support.md)

## 기여

기여를 환영한다. 개발 환경, 시험, 풀 리퀘스트 점검 목록은 [CONTRIBUTING.md](CONTRIBUTING.md)에, 커뮤니티 행동 규범은 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)에 있다. 주요 변경 내역은 [CHANGELOG.md](CHANGELOG.md)에 적는다. 세 문서는 영어로 쓴다.

## 보안

취약점은 공개 이슈로 알리지 않는다. 비공개로 알리는 방법은 [SECURITY.md](SECURITY.md)에 있다.

## 라이선스

Trace Agent 는 [Apache License 2.0](LICENSE) 으로 배포한다.
