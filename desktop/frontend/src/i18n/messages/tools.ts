// Messages of the tools namespace. Keys are written without the namespace
// prefix; components ask for "tools.<key>". Every key in en must exist in ko
// (the type below enforces it). Placeholders look like {name}; plural forms
// use the suffixes _one and _other.
export const en = {
  loading: 'Reading tool usage.',

  'tab.tools': 'Tools',
  'tab.tools.note':
    'Calls per tool name. The failure rate is failed calls divided by all calls; p50 and p95 are durations of the calls whose result came back.',
  'tab.mcpServers': 'MCP servers',
  'tab.mcpServers.name': 'Server',
  'tab.mcpServers.note':
    'Calls of the tools each MCP server provides, added up per server. The average time counts only calls whose result came back.',
  'tab.skills': 'Skills',
  'tab.skills.name': 'Skill',
  'tab.skills.note':
    'Calls per skill loaded with the Skill tool. Tokens are the model calls the transcript attributes to the skill; empty when nothing is attributed.',
  'tab.plugins': 'Plugins',
  'tab.plugins.name': 'Plugin',
  'tab.plugins.note':
    'Calls of the skills and MCP tools a plugin provides, added up per plugin. Tokens are the model calls the transcript attributes to the plugin.',
  'tab.hooks': 'Hooks',
  'tab.hooks.note':
    'Runs per hook event and name. Hooks run on every tool call or turn, so their run time adds directly to waiting time.',
  'tab.subagents': 'Sub-agents',
  'tab.subagents.name': 'Type',
  'tab.subagents.note':
    'Sub-agents started with the Agent tool, counted per type. The failure rate is the share of runs that ended in failure.',
  'tab.programs': 'Bash programs',
  'tab.programs.name': 'Program',
  'tab.programs.note':
    'Program names taken from Bash commands and counted. Command arguments are not stored, so they do not appear here either.',

  'category.builtin': 'Built-in tools',
  'category.mcp': 'MCP',
  'category.skill': 'Skills',
  'category.agent': 'Sub-agents',

  'legend.failure':
    'A failure rate of {warn} or more is shown in the caution colour, and {bad} or more in the warning colour with a "High" label.',
  'legend.hook':
    'A hook is marked "Slow" when its average time is {avg} s or more or its maximum time is {max} s or more.',

  'col.name': 'Name',
  'col.category': 'Category',
  'col.calls': 'Calls',
  'col.errors': 'Failures',
  'col.rate': 'Failure rate',
  'col.pending': 'Pending',
  'col.sessions': 'Sessions',
  'col.avg': 'Avg time',
  'col.max': 'Max time',
  'col.tokens': 'Attributed tokens',
  'col.event': 'Event',
  'col.runs': 'Runs',

  'caption.group': '{group}: calls, failure rate and average time',
  'caption.hooks': 'Runs, failure rate and run time per hook',
  'caption.tools': 'Calls, failure rate and duration per tool',

  'flag.high': 'High',
  'flag.slow': 'Slow',
  unnamed: 'Unnamed',
  tokenDetail:
    'Input {input} · output {output} · cache read {cacheRead} · cache write {cacheCreate}',

  'sort.asc': 'ascending',
  'sort.desc': 'descending',
  'sort.active': '{column}: sorted {dir}. Click to reverse the order.',
  'sort.by': 'Sort by {column}',

  'summary.calls': 'Tool calls',
  'summary.callsValue_one': '{n} call',
  'summary.callsValue_other': '{n} calls',
  'summary.tools_one': '{n} tool',
  'summary.tools_other': '{n} tools',
  'summary.rate': 'Overall failure rate',
  'summary.rateHint': '{failed} failed · {pending} pending',
  'summary.share': 'Share of calls by category',
  'summary.shareAria': 'Share of calls by category: {parts}',
  'summary.shareTitle_one': '{label} {n} call ({share})',
  'summary.shareTitle_other': '{label} {n} calls ({share})',
  'summary.legendCalls_one': '{n} call',
  'summary.legendCalls_other': '{n} calls',

  'origin.server': 'server {name}',
  'origin.plugin': 'plugin {name}',

  'filter.category': 'Category',
  'filter.all': 'All',
  'filter.name': 'Name',
  'filter.placeholder': 'Name, server, plugin',
  'filter.clear': 'Clear filters',
  'filter.shown': '{shown} of {total} tools',
  'filter.total_one': '{total} tool',
  'filter.total_other': '{total} tools',
  noMatch: 'No tools match the filters.',
} as const satisfies Record<string, string>

export const ko: Record<keyof typeof en, string> = {
  loading: '도구 사용 기록을 읽는 중입니다.',

  'tab.tools': '도구',
  'tab.tools.note':
    '도구 이름별 호출 수입니다. 실패율은 실패한 호출을 전체 호출로 나눈 값이고, p50과 p95는 결과가 돌아온 호출만으로 구한 소요 시간입니다.',
  'tab.mcpServers': 'MCP 서버',
  'tab.mcpServers.name': '서버',
  'tab.mcpServers.note':
    'MCP 서버별로 그 서버가 제공한 도구의 호출을 합쳤습니다. 평균 시간은 결과가 돌아온 호출만으로 구합니다.',
  'tab.skills': '스킬',
  'tab.skills.name': '스킬',
  'tab.skills.note':
    'Skill 도구로 불러온 스킬별 호출 수입니다. 토큰은 세션 기록이 그 스킬에 귀속한 모델 호출의 합이고, 귀속 기록이 없으면 비어 있습니다.',
  'tab.plugins': '플러그인',
  'tab.plugins.name': '플러그인',
  'tab.plugins.note':
    '플러그인이 제공한 스킬과 MCP 도구의 호출을 플러그인별로 합쳤습니다. 토큰은 세션 기록이 그 플러그인에 귀속한 모델 호출의 합입니다.',
  'tab.hooks': '훅',
  'tab.hooks.note':
    '훅 이벤트와 이름별 실행 횟수입니다. 훅은 도구 호출이나 턴마다 실행되므로 실행 시간이 그대로 대기 시간이 됩니다.',
  'tab.subagents': '하위 에이전트',
  'tab.subagents.name': '종류',
  'tab.subagents.note':
    'Agent 도구로 시작한 하위 에이전트를 종류별로 센 수치입니다. 실패율은 실패로 끝난 실행의 비율입니다.',
  'tab.programs': 'Bash 프로그램',
  'tab.programs.name': '프로그램',
  'tab.programs.note':
    'Bash 명령에서 프로그램 이름만 뽑아 센 수치입니다. 명령의 인자는 저장하지 않으므로 여기에도 나오지 않습니다.',

  'category.builtin': '기본 도구',
  'category.mcp': 'MCP',
  'category.skill': '스킬',
  'category.agent': '하위 에이전트',

  'legend.failure':
    "실패율이 {warn} 이상이면 주의 색으로, {bad} 이상이면 경고 색과 '높음' 표시로 나타냅니다.",
  'legend.hook':
    "평균 시간이 {avg}초 이상이거나 최대 시간이 {max}초 이상이면 '느림'을 붙입니다.",

  'col.name': '이름',
  'col.category': '분류',
  'col.calls': '호출(회)',
  'col.errors': '실패(회)',
  'col.rate': '실패율',
  'col.pending': '미완료(회)',
  'col.sessions': '세션(개)',
  'col.avg': '평균 시간',
  'col.max': '최대 시간',
  'col.tokens': '귀속 토큰',
  'col.event': '이벤트',
  'col.runs': '실행(회)',

  'caption.group': '{group}별 호출 수와 실패율, 평균 시간',
  'caption.hooks': '훅별 실행 횟수와 실패율, 실행 시간',
  'caption.tools': '도구별 호출 수와 실패율, 소요 시간',

  'flag.high': '높음',
  'flag.slow': '느림',
  unnamed: '이름 없음',
  tokenDetail: '입력 {input} · 출력 {output} · 캐시 읽기 {cacheRead} · 캐시 쓰기 {cacheCreate}',

  'sort.asc': '오름차순',
  'sort.desc': '내림차순',
  'sort.active': '{column}: {dir}으로 정렬됨. 누르면 순서를 뒤집습니다.',
  'sort.by': '{column} 기준으로 정렬',

  'summary.calls': '도구 호출',
  'summary.callsValue_one': '{n}회',
  'summary.callsValue_other': '{n}회',
  'summary.tools_one': '도구 {n}개',
  'summary.tools_other': '도구 {n}개',
  'summary.rate': '전체 실패율',
  'summary.rateHint': '실패 {failed}회 · 미완료 {pending}회',
  'summary.share': '분류별 호출 비중',
  'summary.shareAria': '분류별 호출 비중: {parts}',
  'summary.shareTitle_one': '{label} {n}회 ({share})',
  'summary.shareTitle_other': '{label} {n}회 ({share})',
  'summary.legendCalls_one': '{n}회',
  'summary.legendCalls_other': '{n}회',

  'origin.server': '서버 {name}',
  'origin.plugin': '플러그인 {name}',

  'filter.category': '분류',
  'filter.all': '전체',
  'filter.name': '이름',
  'filter.placeholder': '이름, 서버, 플러그인',
  'filter.clear': '조건 지우기',
  'filter.shown': '도구 {total}개 중 {shown}개',
  'filter.total_one': '도구 {total}개',
  'filter.total_other': '도구 {total}개',
  noMatch: '조건에 맞는 도구가 없습니다.',
}
