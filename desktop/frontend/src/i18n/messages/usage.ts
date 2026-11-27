// Messages of the usage namespace. Keys are written without the namespace
// prefix; components ask for "usage.<key>". Every key in en must exist in ko
// (the type below enforces it). Placeholders look like {name}; plural forms
// use the suffixes _one and _other.
export const en = {
  navDashboard: 'Dashboard',
  navSessions: 'Sessions',
  navSession: 'Session details',
  navTools: 'Tools and extensions',
  navInsights: 'Insights',
  navExtensions: 'Extension events',
  navSettings: 'Collection settings',

  period: 'Period',
  lastDays_one: 'Last day',
  lastDays_other: 'Last {count} days',
  project: 'Project',
  allProjects: 'All',
  scanProgress: 'Collecting {done}/{total}',
  statusFailed: 'Could not read the status.',

  consentTitle: 'Start collecting session history?',
  consentIntro:
    'Trace Agent reads the Claude Code session history folder on this Mac ({path}) to total your usage.',
  consentKeepsCounts:
    'Only figures and names such as tokens, models, tools, skills and hooks are kept in the app’s store.',
  consentNoText:
    'Conversation text is not stored; the session details read it straight from the original file.',
  consentLocal: 'Nothing that is read leaves this Mac.',
  consentSettings: 'You can exclude projects or stop collecting in the collection settings.',
  consentStart: 'Start collecting',
  consentFailed: 'Could not turn on collection.',

  durationMs: '{value} ms',
  durationSeconds: '{value} s',
  durationMinutes: '{value} min',
  durationHours: '{value} h',
  costUnknown: 'Unknown',
  timeUnknown: 'Unknown time',
  listSeparator: ', ',

  coverageUnreadableFiles: 'some files could not be read',
  coverageSkippedLines: 'some lines could not be parsed and were skipped',
  coverageOutsideWindow: 'part of the range is older than the collection window',
  coverageDisabled: 'Collection is off. You can turn it on in the collection settings.',
  coverageScanning: 'Reading session history. The numbers may still grow.',
  coveragePartial: 'Partial totals:',
  coverageReasons: '{reasons}.',
  coverageSomeUnread: 'some records could not be read',
  coverageFiles_one: '{value} unreadable file.',
  coverageFiles_other: '{value} unreadable files.',
  coverageLines_one: '{value} skipped line.',
  coverageLines_other: '{value} skipped lines.',

  emptyScanning: 'Still reading. This fills in shortly.',
  emptyDisabled: 'Collection is off, so there are no records to show.',
  emptyPartial: 'No records were read in this range. Some parts could not be read, so there may be some.',
  emptyComplete: 'There are no records in this range.',
  retry: 'Retry',
} as const satisfies Record<string, string>

export const ko: Record<keyof typeof en, string> = {
  navDashboard: '대시보드',
  navSessions: '세션',
  navSession: '세션 상세',
  navTools: '도구와 확장',
  navInsights: '인사이트',
  navExtensions: '확장 이벤트',
  navSettings: '수집 설정',

  period: '기간',
  lastDays_one: '최근 {count}일',
  lastDays_other: '최근 {count}일',
  project: '프로젝트',
  allProjects: '전체',
  scanProgress: '수집 중 {done}/{total}',
  statusFailed: '상태를 읽지 못했습니다.',

  consentTitle: '세션 기록 수집을 시작할까요?',
  consentIntro: '이 Mac의 Claude Code 세션 기록 폴더({path})를 읽어 사용 현황을 집계합니다.',
  consentKeepsCounts: '토큰, 모델, 도구, 스킬, 훅 같은 수치와 이름만 앱의 저장소에 남깁니다.',
  consentNoText: '대화 원문은 저장하지 않고, 세션 상세를 열 때 원본 파일에서 바로 읽습니다.',
  consentLocal: '읽은 내용을 이 Mac 밖으로 보내지 않습니다.',
  consentSettings: '프로젝트별 제외와 수집 중지는 수집 설정에서 할 수 있습니다.',
  consentStart: '수집 시작',
  consentFailed: '수집을 켜지 못했습니다.',

  durationMs: '{value}ms',
  durationSeconds: '{value}초',
  durationMinutes: '{value}분',
  durationHours: '{value}시간',
  costUnknown: '미확인',
  timeUnknown: '시각 미상',
  listSeparator: ', ',

  coverageUnreadableFiles: '읽지 못한 파일이 있습니다',
  coverageSkippedLines: '해석하지 못해 건너뛴 줄이 있습니다',
  coverageOutsideWindow: '수집 기간보다 앞선 구간이 포함됐습니다',
  coverageDisabled: '수집이 꺼져 있습니다. 수집 설정에서 켤 수 있습니다.',
  coverageScanning: '세션 기록을 읽는 중입니다. 수치가 계속 늘어날 수 있습니다.',
  coveragePartial: '부분 집계',
  coverageReasons: '{reasons}.',
  coverageSomeUnread: '일부 기록을 읽지 못했습니다',
  coverageFiles_one: '파일 {value}개',
  coverageFiles_other: '파일 {value}개',
  coverageLines_one: '줄 {value}개',
  coverageLines_other: '줄 {value}개',

  emptyScanning: '아직 읽는 중입니다. 잠시 뒤 채워집니다.',
  emptyDisabled: '수집이 꺼져 있어 보여 줄 기록이 없습니다.',
  emptyPartial: '이 범위에서 읽은 기록이 없습니다. 읽지 못한 부분이 있어 실제로는 있을 수 있습니다.',
  emptyComplete: '이 범위에는 기록이 없습니다.',
  retry: '다시 읽기',
}
