import { Card } from '../../ui'
import { TOKEN_ENV, URL_ENV } from './model'

// The examples read the address and the token from the environment, so a
// copied example never carries a credential.
const curlExample = [
  `curl -sS -X POST "$${URL_ENV}" \\`,
  `  -H "Authorization: Bearer $${TOKEN_ENV}" \\`,
  `  -H "Content-Type: application/json" \\`,
  `  -d '{"version":1,"source":"my-tool",`,
  `       "kind":"log","name":"hello"}'`,
].join('\n')

const hookExample = [
  `"hooks": {`,
  `  "PostToolUse": [{`,
  `    "matcher": "Bash",`,
  `    "hooks": [{`,
  `      "type": "command",`,
  `      "command": "trace-emit --name post-bash --from-hook"`,
  `    }]`,
  `  }]`,
  `}`,
].join('\n')

const runExample = 'trace-emit run --name build -- make all'

const examples: readonly { id: string; title: string; note: string; code: string }[] = [
  {
    id: 'curl',
    title: 'curl 로 보내기',
    note: '필수 필드는 version, source, kind, name 입니다. kind 는 span, log, metric 중 하나입니다.',
    code: curlExample,
  },
  {
    id: 'hook',
    title: 'Claude Code 훅에서 보내기',
    note: 'Claude Code 설정의 hooks 에 넣습니다. --from-hook 은 훅 입력에서 세션 ID 와 도구 호출 ID 를 읽어 이벤트를 그 세션에 연결합니다.',
    code: hookExample,
  },
  {
    id: 'run',
    title: '명령을 감싸서 실행하기',
    note: '명령을 실행하고 걸린 시간과 종료 코드를 보낸 뒤, 그 종료 코드로 끝납니다.',
    code: runExample,
  },
]

/** Copy-ready ways to send events to the endpoint. */
export function ConnectGuide() {
  return (
    <Card title="연결 방법">
      <p className="u-ext-lead">
        설정 복사로 받은 주소와 토큰을 환경 변수 <code>{URL_ENV}</code>, <code>{TOKEN_ENV}</code>{' '}
        에 넣어 둡니다. 아래 예시는 두 변수를 읽으므로 토큰을 명령에 직접 적지 않습니다.
      </p>
      <ul className="u-ext-examples">
        {examples.map((example) => (
          <li key={example.id} className="u-ext-example">
            <div>
              <h3>{example.title}</h3>
              <p className="u-hint">{example.note}</p>
            </div>
            <pre className="u-ext-code">{example.code}</pre>
          </li>
        ))}
      </ul>
      <p className="u-hint">
        trace-emit 은 주소가 이 Mac 의 수신기(http://127.0.0.1:&lt;포트&gt;/v1/events)가 아니면 보내지
        않습니다. 전송에 실패해도 원래 명령은 그대로 실행됩니다.
      </p>
    </Card>
  )
}
