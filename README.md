# Trace Agent

macOS에서 Codex와 Claude Code의 로컬 작업 이력을 조회하는 앱이다. 작업에서 하위 에이전트, 도구 호출과 결과, 각 판정에 사용한 근거까지 따라갈 수 있다. 화면을 고정하면 다른 기록이 도착해도 같은 리비전의 작업과 통계를 다시 볼 수 있다.

## 실행

macOS에서 Xcode Command Line Tools, Go 1.25 이상, Node.js와 npm이 필요하다. 저장소 루트에서 실행한다.

```sh
make -C desktop package
open "desktop/build/bin/Trace Agent.app"
```

앱에서 **출처와 설정 → 출처 선택**을 연다. Codex는 프로젝트 이력이나 rollout 파일을, Claude Code는 프로젝트 세션 폴더나 JSONL 파일을 선택한다. Claude의 자식 세션까지 보려면 루트와 자식 기록을 함께 담은 세션 폴더를 선택한다. 수집 범위와 보존 기간은 출처별로 조정할 수 있다. 기본 수집 범위는 메타데이터다.

Codex와 Claude Code의 OTel 기록은 같은 창에서 **로컬 OTel 수신기**를 추가해 받을 수 있다. 수신기 상세에서 터미널 설정을 복사하고 새 CLI 세션에 적용한다. 키체인이 송신 자격을 보관하고, 동의한 출처에서만 지원하는 본문 필드를 정제한다. 참여 MCP 서버와 Claude 훅은 기존 클라이언트 출처의 **추가 관찰 출처**에서 연결한다. [Claude OTel](projects/trace-agent/otlp-compat.md) · [Codex CLI와 OTel](projects/trace-agent/codex-compat.md) · [훅과 MCP 송신 형식](projects/trace-agent/observer-sdd.md)

## 검증

```sh
node projects/trace-agent/tests/verify.mjs
go test -race -count=1 ./...
make -C desktop check
```

| 설계 단계 | 검증 기록 |
|---|---|
| 1–2. 출처와 어댑터 | [출처별 지원표](projects/trace-agent/source-support.md) · [데이터 계약](projects/trace-agent/stage-1-sdd.md) · [수집과 재개](projects/trace-agent/stage-2-sdd.md) |
| 3–4. SQLite와 사실 조회 | [저장·삭제·장애 복구](projects/trace-agent/stage-3-sdd.md) · [사실과 통계](projects/trace-agent/stage-4-sdd.md) |
| 5–6. 전송과 시퀀스 | [GraphQL과 WebSocket](projects/trace-agent/stage-5-sdd.md) · [고정 시점의 시퀀스](projects/trace-agent/stage-6-sdd.md) |
| 7–8. 화면과 설치물 | [화면 인수 시험](projects/trace-agent/stage-7-sdd.md) · [패키지와 성능](projects/trace-agent/stage-8-sdd.md) |
