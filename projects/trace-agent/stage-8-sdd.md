# 8단계 · macOS 패키지와 출시 검증

요구사항 R01~R11. GraphQL 1.3, SQLite user_version 13, 수집 어댑터 2.1.0. 2026-11-20에 Apple M4, 통합 메모리 24 GiB, macOS 26.5 (25F71), arm64에서 검사했다. 배포 번들의 Go 버전은 1.26.11, Wails는 v2.11.0, Node는 24.21.0이다. 브라우저 성능 측정에는 Chrome 156.0.8078.82를 사용했다.

## 설치물과 네이티브 창

`desktop/build/bin/Trace Agent.app`와 `desktop/build/bin/Trace-Agent-macOS.zip`은 로컬 빌드 결과다. Makefile이 React 번들을 Go 실행 파일에 포함시키고 macOS 앱에 ad-hoc 서명을 한다. `codesign --verify --strict`를 통과했다. 이 기기에서 확인한 범위는 Apple Silicon과 macOS 26.5다. Intel, 다른 macOS 버전, Developer ID 서명과 Apple 공증은 미검증이다.

| 요구사항 | 입력과 실행 경로 | 실제 결과 |
| --- | --- | --- |
| R01~R03, R07~R09 | 새 QA 앱 번들, 빈 DB, 새 권한 저장소. 실제 WKWebView의 React가 Wails bridge, 일회용 연결 티켓, 로컬 WebSocket을 거쳐 조회 | 첫 화면에 연결한 출처 0개. 합성 Codex 파일을 선택 권한 처리기에 넘긴 후 출처 1개, Read 시도 1회 표시 |
| R01, R02, R08 | 위 프로세스를 종료하고 같은 QA 프로필·DB로 앱 재실행 | 자동으로 출처를 다시 열고 과거 시도 1회 표시. 새 수집 입력이 없어도 이력을 조회할 수 있음 |
| R07~R09 | 허용되지 않은 Origin, 잘못된 티켓, 비밀 표식이 든 합성 입력 | 인증·Origin 거부 시험 통과. 본문 동의 전후의 민감한 표식이 DB, WAL, 프로세스 출력, 조회한 WebSocket 프레임에 남지 않음 |
| R03~R06, R08, R10, R11 | 같은 자료를 실시간·고정·서로 다른 경로에서 조회 | 실제 React에서 #120 시도 5·근거 19의 경로 일치, L7~L16 장면과 삭제 epoch 검사를 통과. [7단계 화면 시험](stage-7-sdd.md) |

네이티브 시험은 다른 번들 식별자와 임시 SQLite·권한 저장소를 사용한다. QA는 전용 번들 식별자와 시험 자료만 연다. 시험용 진입점은 `nativecheck` 빌드 태그로만 들어간다. QA 번들은 직접 OS 선택 창을 누르는 단계와 운영 키체인의 OS 접근 권한 대화상자를 시험하지 않는다. [네이티브 시험 코드](../../desktop/nativecheck_darwin.go)

Finder가 넘기는 기본 PATH에 CLI가 없으면 macOS의 표준 설치 경로와 Codex를 포함한 앱 번들을 확인한다. 프로젝트 선택 후 이력 동기화를 마치면 같은 app-server 연결을 유지해 새 알림을 받고 주기적으로 누락 이력을 확인한다. 수집 사용 여부, 본문 동의 또는 이력 기간이 바뀌면 기존 구독을 취소하고 현재 권한으로 다시 연결한다.

## 성능

### 최근 24시간 고정 조회

Codex rollout의 1,000개 세션에 각각 세션 시작, 작업 시작, Read 호출·결과 49쌍을 넣었다. 발생 시각은 24시간 창 안에서 850ms 간격이다. 원천 근거 100,000개, 작업 1,000개, 호출 시도 49,000개, 결과 49,000개다. 별도 publish한 합성 사실은 0개이며 읽기 모델이 원천에서 사실을 계산한다. 100개씩 원자적으로 추가하고 두 출처를 등록한 뒤 리비전 1002의 같은 SQLite와 필터에서 측정했다.

Chrome의 WebSocket 요청 전송 직전부터 GraphQL 결과를 받아 파싱한 시점까지 쟀다. 개요와 한 Read 인스턴스의 첫 페이지(요청 크기 50, 실제 49건)를 각각 한 번 예열한 후 두 종류를 번갈아 30회씩 조회했다. 매번 동일 point와 기대 건수를 대조했다. p95는 오름차순 29번째 값이다.

| 30회 조회 | 첫 측정 | 통합 후 재측정 | 목표 |
| --- | ---: | ---: | ---: |
| 최근 24시간 개요 | 26.5ms | 19.8ms | ≤ 1,000ms |
| Read 인스턴스의 호출 첫 페이지 | 2.4ms | 1.9ms | ≤ 1,000ms |

### 두 출처 실시간 수집

완결된 JSONL을 Codex와 Claude 파일에 번갈아 20ms 간격으로 썼다. 총 10분, 출처당 초당 25건, 합계 30,000건이다. 실제 데스크톱 수집기의 200ms 파일 탐색, SQLite 커밋, GraphQL 구독, React 출처 화면을 사용했다. 기록 준비 시각은 한 줄 쓰기 직전이다. 화면 시각은 해당 리비전 이상을 표시하는 DOM 변경 후 두 번째 requestAnimationFrame에서 기록했다. DB의 근거별 리비전을 화면 묶음과 연결하고 모든 근거가 저장됐는지 센다.

| 실행 | 저장된 근거 | 화면 표본 | p95 | 최대 | 목표 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 첫 측정 | 30,000 / 30,000 | 3,006 | 314.7ms | 703.9ms | p95 ≤ 2,000ms |
| 통합 후 재측정 (602.09초) | 30,000 / 30,000 | 3,077 | 374.2ms | 1,344.7ms | p95 ≤ 2,000ms |

초기 시험은 macOS 임시 폴더의 심볼릭 링크가 파일 선택 권한을 통과하지 않아 0건을 수집했다. 시험 폴더도 실제 선택 창과 같이 정규화한 뒤 40줄 smoke와 10분 시험을 재실행했다. 측정 중 생성한 입력과 DB는 임시 디렉터리에만 저장한다. [합성 자료 생성과 수집 시험](../../desktop/release_test.go) · [브라우저 측정](../../desktop/frontend/test/release.mjs)

## 재실행

저장소 루트에서 실행한다. 브라우저 시험에는 Chrome, 네이티브 시험에는 macOS GUI 세션과 Xcode Command Line Tools가 필요하다.

```sh
go test -count=1 -run 'TestReleaseRedaction|TestAdmissionAndOperationLimits' ./internal/transport
cd desktop
make package
make native-smoke
golangci-lint run --no-config --enable gosec --enable revive ./...
TRACE_VERIFY=smoke go test -count=1 -v -run '^TestDesktopRelease$' .
TRACE_VERIFY=read go test -count=1 -v -timeout 15m -run '^TestDesktopRelease$' .
TRACE_VERIFY=live go test -count=1 -v -timeout 15m -run '^TestDesktopRelease$' .
TRACE_IA_BROWSER=1 go test -race -count=1 -v -run '^TestDesktopIA$' .
```

두 CLI에서 설치한 바이너리와 격리 프로젝트로 새 턴, 도구, 재시작 범위를 확인했다. 클라이언트 버전, CLI와 데스크톱 차이, 조건부 자료원과 지원 한계는 [출처별 지원표](source-support.md)에 있다. 이후 데스크톱 세션에서 확보한 원천 기록과 재시작 검사는 아래에 기록했다.

## 데스크톱 클라이언트의 신규 세션

2026-11-20 KST, R01·R03·R08. 기준 main은 `ea6afa4`, 수집 어댑터는 2.1.0, SQLite는 13이다. 이 커밋에서 `make -C desktop package`로 만든 배포 앱의 서명과 실행 중인 프로세스를 확인했다. 기존 화면 인수 시험과 별개로 이번 검증에서는 원천 기록과 읽기 모델을 대조했다. 개인 세션 경로, 원문, 식별자는 커밋에서 제외했다.

**Codex 데스크톱 26.1111.71704.** 데스크톱의 새 로컬 채팅에서 합성 문구를 출력하는 명령을 한 번 실행했다. 앱은 턴과 commandExecution을 완료 처리했고 출력 문구도 일치했다. 해당 rollout의 session_meta에 originator가 `Codex Desktop`, cli_version이 `0.169.1`로 기록됐다. custom_tool_call과 custom_tool_call_output의 call_id가 일치했다. 앱에 포함된 app-server의 새 프로세스에서 `thread/turns/list`로 이 턴을 조회하고 프로세스를 종료한 뒤 다시 조회했다. 두 조회의 턴·항목 ID와 완료 상태가 일치했다.

| Codex 단일 세션 | 원천 근거 | 작업 | 종료된 시도 | revision / epoch | 마지막 완결 바이트 |
|---|---:|---:|---:|---|---:|
| 첫 수집 | 30 | 1 | 1 | 30 / 1 | 228984 |
| 수집기 프로세스 재시작 후 같은 파일 조회 | 30 | 1 | 1 | 30 / 1 | 228984 |

독립된 두 프로세스에서 배포 앱의 파일 수집 함수, SQLite와 읽기 모델을 사용했다. 두 번째 프로세스가 같은 작업·시도 ID와 파일 체크포인트를 읽었는지 대조했다. 검증 대상은 이 세션의 완료 이력이다. Codex 앱 자체의 종료·재실행, 진행 중 알림, 계획과 하위 에이전트는 이번 결과에 포함하지 않았다.

단일 도구 호출을 끝낸 전용 시험 세션 파일 하나를 절대 경로로 지정한다. DB와 수집용 키는 시험마다 새로 만든다. 같은 시험 안의 두 프로세스만 DB와 키를 공유한다. 본문 수집은 꺼져 있다. [재시작 시험](../../desktop/client_history_test.go)

```sh
cd desktop
TRACE_DESKTOP_SESSION_KIND=codex-rollout \
TRACE_DESKTOP_SESSION_FILE='/absolute/path/to/selected-rollout.jsonl' \
go test -race -count=1 -run '^TestDesktopClientHistory$' -v .
```

**Claude 데스크톱 6.6395.1.** 새 임시 Git 프로젝트에 한 줄짜리 `probe.txt`를 준비했다. 설치 CLI 2.1.329에서 `claude --desktop`은 프로젝트를 Claude Desktop으로 열었다고 보고하고 정상 종료했다. 같은 옵션에 첫 메시지를 함께 주면 앱에서 입력하라는 안내와 종료 코드 1이 반환됐다. 이 단계에서 시험 프로젝트의 Read 호출·결과를 담은 JSONL은 확보하지 못했다. 데스크톱 도구 실행과 재시작 이력은 미확인으로 둔다. 완료된 시험 세션을 확보하면 위 명령에서 종류를 `claude-transcript`로 바꾸고 그 파일 하나를 지정한다.

11월 20일 후속 확인에서 Claude 앱의 `main.log`에 시험 폴더의 신뢰 설정 저장(13:45 KST)과 같은 경로에 대한 신뢰 확인(15:34 KST)이 남았다. 사용자가 첫 메시지를 보냈다고 알려온 뒤, 해당 프로젝트 이름과 시험 입력·결과 표식으로 로컬 프로젝트 JSONL을 대조했다. 시험에 해당하는 기록은 찾지 못했다. 신뢰 로그로 확인한 항목은 앱이 받은 프로젝트 경로다. Read 호출·결과 ID 연결, 수집기 재시작 후 작업·시도 수와 체크포인트는 미검증이다. 후속 재생에는 해당 Code 세션의 원천 JSONL이 필요하다.
