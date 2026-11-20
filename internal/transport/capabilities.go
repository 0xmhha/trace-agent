package transport

import (
	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
)

func capability(code, state, reason string) object {
	return object{"code": code, "state": state, "reason": reason}
}
func capabilities(i store.SourceInfo) []any {
	items := []any{capability("desktop_live", "UNVERIFIED", "별도 데스크톱 창에서 시작한 작업의 알림과 재시작을 확인하지 못했습니다.")}
	observer := i.Kind == string(collect.ClaudeHook) || i.Kind == string(collect.MCPObserver)
	codexOTel := i.Kind == string(collect.CodexTelemetry)
	telemetry := i.Kind == string(collect.Telemetry) || codexOTel
	if telemetry {
		reason := "Claude Code CLI 2.1.329의 OTLP/HTTP protobuf와 JSON 송신을 확인했습니다."
		state := "AVAILABLE"
		if codexOTel {
			reason = "Codex CLI 0.169.1에서 OTLP/HTTP protobuf와 JSON으로 보낸 API 시도 번호, HTTP 상태, 소요 시간을 확인했습니다."
			if i.ClientVersion != "0.169.1" {
				state = "UNVERIFIED"
				reason = "Codex CLI 0.169.1에서 송신을 확인했습니다. 현재 출처의 버전은 확인 범위 밖입니다."
			}
		}
		items = append(items, capability("history", "UNAVAILABLE", "OTLP 수신기를 연결한 뒤 SDK가 전송한 이벤트를 수집합니다. 이전 기록을 요청하는 기능은 없습니다."),
			capability("telemetry", state, reason),
			capability("restart", "PARTIAL", "동일한 수신 주소와 출처 자격을 복원합니다. 송신자가 재시도한 이벤트는 같은 기록으로 처리합니다."))
	} else if observer {
		items = append(items, capability("history", "UNAVAILABLE", "선택한 관찰 수신기에 전달한 이벤트만 기록합니다. 원천의 이전 이력 조회는 지원하지 않습니다."))
	} else {
		history := capability("history", "UNVERIFIED", "이 출처가 전달한 기존 근거를 아직 확인하지 못했습니다.")
		if i.HasMetadata {
			history = capability("history", "AVAILABLE", "선택한 범위에서 해당 시점까지 근거가 수집됐습니다.")
		}
		items = append(items, history)
	}
	switch collect.Kind(i.Kind) {
	case collect.ClaudeHook:
		items = append(items, capability("hook", "PARTIAL", "등록한 훅의 실행 전·종료 이벤트를 받습니다. 실행 전 이벤트는 완료 근거로 쓰지 않습니다."))
	case collect.MCPObserver:
		items = append(items, capability("mcp_observer", "PARTIAL", "명시적으로 연결한 MCP 서버가 보고한 수신·반환만 확인합니다. 클라이언트와의 대응에는 공통 연결 근거가 필요합니다."))

	case collect.AppServer:
		items = append(items, capability("cli_history", "AVAILABLE", "Codex CLI 0.169.1의 스레드·턴·항목 이력을 읽었습니다."),
			capability("live", "PARTIAL", "선택한 작업 디렉터리의 이력을 주기적으로 다시 읽습니다. 다른 클라이언트 연결의 즉시 알림 전달은 미확인입니다."),
			capability("restart", "AVAILABLE", "같은 프로필의 app-server를 다시 시작해 이력의 동일 항목을 대조했습니다."),
			capability("plan", "PARTIAL", "계획 변경 알림의 단계 투영은 합성 표본에서 확인했습니다. 실제 CLI 계획 갱신은 미확인입니다."))
	case collect.Rollout:
		items = append(items, capability("cli", "AVAILABLE", "Codex CLI 0.169.1에서 일반 명령과 자식 생성을 확인했습니다."),
			capability("live", "AVAILABLE", "실제 CLI 턴이 진행 중인 동안 완결된 줄을 수집했습니다."),
			capability("restart", "AVAILABLE", "저장된 파일 위치에서 재개하고 마지막 미완결 줄은 다음 읽기까지 보류합니다."))
	case collect.Transcript:
		items = append(items, capability("cli", "AVAILABLE", "Claude Code CLI 2.1.329에서 도구 왕복과 자식 파일을 확인했습니다."),
			capability("live", "AVAILABLE", "선택한 세션 파일에서 완결된 줄을 수집합니다. 자식 파일을 함께 읽으려면 프로젝트 폴더가 필요합니다."),
			capability("restart", "AVAILABLE", "SQLite 체크포인트에서 동일 파일을 재개하고 파일 교체·축소는 공백으로 기록합니다."))
	}
	verifiedVersion := "2.1.329"
	if i.Client == "codex" {
		verifiedVersion = "0.169.1"
	}
	if i.ClientVersion == "" {
		items = append(items, capability("client_version", "UNVERIFIED", "이 출처의 근거에 클라이언트 버전이 없습니다."))
	}
	if i.ClientVersion != "" && i.ClientVersion != verifiedVersion {
		items = append(items, capability("client_version", "UNVERIFIED", "관찰한 클라이언트 버전은 설치본 호환 시험의 버전과 다릅니다."))
	}

	if i.Client == "codex" && !codexOTel {
		items = append(items, capability("telemetry", "UNAVAILABLE", "Codex OTel 이벤트는 전용 출처를 연결하면 수집합니다."))
	}
	if observer {
		items = append(items, capability("body", "UNAVAILABLE", "이 관찰 경로는 식별자와 상태만 수집합니다."))
	} else if i.Consent {
		reason := "사건 시각과 현재 동의가 모두 유효한 지원 필드만 정제해 표시합니다."
		if telemetry {
			reason = "송신자가 이 루프백 주소로 본문 옵션을 켠 뒤 보낸 지원 필드만 정제합니다."
		}
		if codexOTel {
			reason = "CLI의 log_user_prompt 옵션을 켜고 출처 본문에 동의한 경우에만 사용자 입력을 정제합니다."
		}
		items = append(items, capability("body", "PARTIAL", reason))
	} else {
		items = append(items, capability("body", "UNAVAILABLE", "이 출처의 본문 동의가 꺼져 있습니다."))
	}
	if !i.Enabled {
		items = append(items, capability("collection", "UNAVAILABLE", "출처 설정에서 수집을 중지했습니다."))
	}
	return items
}

func gapReason(code string) string {
	switch code {
	case "replaced":
		return "파일이 교체되어 새 파일의 시작부터 읽었습니다."
	case "truncated":
		return "파일이 축소되어 저장된 위치에서 이어 읽을 수 없었습니다."
	case "rewritten":
		return "파일의 기존 내용이 바뀌어 새 세대로 읽었습니다."
	case "lost":
		return "선택한 출처에 연결할 수 없었습니다."
	case "delayed":
		return "수집이 지연됐습니다."
	case "erased_range":
		return "삭제 또는 보존 만료로 이 기간의 대상 근거를 제거하고 재수집을 차단했습니다."
	default:
		return "출처에서 수집 공백을 보고했습니다."
	}
}
