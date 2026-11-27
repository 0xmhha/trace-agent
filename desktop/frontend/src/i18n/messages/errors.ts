// Messages of the errors namespace. Keys are written without the namespace
// prefix; components ask for "errors.<key>". Every key in en must exist in ko
// (the type below enforces it). Placeholders look like {name}; plural forms
// use the suffixes _one and _other.
//
// Keys with a dot are the codes the desktop bridge rejects with
// (bridgeError in desktop/i18n.go); errorText() translates them. A Go unit
// test checks that every code has an entry here.
export const en = {
  generic: 'The request could not be completed.',

  'bootstrap.failed': 'Could not connect to the local collector.',
  'locale.unsupported': 'This language is not supported.',

  'source.kind_unknown': 'Unknown source.',
  'source.picker_failed': 'The source could not be selected.',
  'source.path_invalid': 'The selected path could not be checked.',
  'source.too_many_picks': 'Too many source selections are open.',
  'source.history_unavailable': 'The history range of this source cannot be selected again.',
  'source.parent_missing': 'Select a connected client source first.',

  'receiver.invalid': 'Check the receiver type and server name.',
  'receiver.dialog': 'The confirmation dialog could not be opened.',
  'receiver.not_found': 'The registered receiver was not found.',
  'receiver.source_disabled': 'Turn on collection for this source first.',
  'receiver.credential': 'The receiver credential could not be saved to the Keychain.',
  'receiver.clipboard': 'Could not copy to the clipboard.',

  'usage.unavailable': 'Usage analytics could not start.',
  'bridge.missing': 'Open this page in the installed app.',
  'usage.failed': 'The usage request could not be completed.',
  'usage.bad_request': 'Check the request.',
  'usage.not_found': 'Session not found.',
  'usage.no_consent': 'Allow usage collection first.',
  'usage.dialog': 'The confirmation dialog could not be opened.',
  'usage.endpoint_off': 'Turn on the extension event receiver first.',
  'usage.credential': 'The receiver credential could not be saved to the Keychain.',
  'usage.clipboard': 'Could not copy to the clipboard.',
} as const satisfies Record<string, string>

export const ko: Record<keyof typeof en, string> = {
  generic: '요청을 처리하지 못했습니다.',

  'bootstrap.failed': '로컬 수집기에 연결하지 못했습니다.',
  'locale.unsupported': '지원하지 않는 언어입니다.',

  'source.kind_unknown': '알 수 없는 출처입니다.',
  'source.picker_failed': '출처를 선택할 수 없습니다.',
  'source.path_invalid': '선택한 경로를 확인할 수 없습니다.',
  'source.too_many_picks': '열린 출처 선택이 너무 많습니다.',
  'source.history_unavailable': '이력 범위를 다시 선택할 수 없는 출처입니다.',
  'source.parent_missing': '먼저 연결한 클라이언트 출처를 선택해 주세요.',

  'receiver.invalid': '수신기 종류와 서버 이름을 확인해 주세요.',
  'receiver.dialog': '확인 창을 열 수 없습니다.',
  'receiver.not_found': '등록된 수신기를 찾을 수 없습니다.',
  'receiver.source_disabled': '출처 수집을 먼저 켜 주세요.',
  'receiver.credential': '수신기 자격을 키체인에 저장할 수 없습니다.',
  'receiver.clipboard': '클립보드에 복사할 수 없습니다.',

  'usage.unavailable': '사용 분석을 시작하지 못했습니다.',
  'bridge.missing': '설치한 앱에서 열어 주세요.',
  'usage.failed': '사용 분석 요청을 처리하지 못했습니다.',
  'usage.bad_request': '요청을 확인해 주세요.',
  'usage.not_found': '세션을 찾을 수 없습니다.',
  'usage.no_consent': '사용 기록 수집을 먼저 허용해 주세요.',
  'usage.dialog': '확인 창을 열 수 없습니다.',
  'usage.endpoint_off': '확장 이벤트 수신기를 먼저 켜 주세요.',
  'usage.credential': '수신기 자격을 키체인에 저장할 수 없습니다.',
  'usage.clipboard': '클립보드에 복사할 수 없습니다.',
}
