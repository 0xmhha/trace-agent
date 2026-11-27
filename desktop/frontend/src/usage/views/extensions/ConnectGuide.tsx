import { Card } from '../../ui'
import { TOKEN_ENV, URL_ENV } from './model'
import { withCode } from './rich'
import { t, type MessageKey } from '../../../i18n'

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

// Message keys, not text, so the wording follows the current locale.
const examples: readonly { id: string; title: MessageKey; note: MessageKey; code: string }[] = [
  {
    id: 'curl',
    title: 'extensions.guide.curl.title',
    note: 'extensions.guide.curl.note',
    code: curlExample,
  },
  {
    id: 'hook',
    title: 'extensions.guide.hook.title',
    note: 'extensions.guide.hook.note',
    code: hookExample,
  },
  {
    id: 'run',
    title: 'extensions.guide.run.title',
    note: 'extensions.guide.run.note',
    code: runExample,
  },
]

/** Copy-ready ways to send events to the endpoint. */
export function ConnectGuide() {
  return (
    <Card title={t('extensions.guide.title')}>
      <p className="u-ext-lead">
        {withCode(t('extensions.guide.lead'), { url: URL_ENV, token: TOKEN_ENV })}
      </p>
      <ul className="u-ext-examples">
        {examples.map((example) => (
          <li key={example.id} className="u-ext-example">
            <div>
              <h3>{t(example.title)}</h3>
              <p className="u-hint">{t(example.note)}</p>
            </div>
            <pre className="u-ext-code">{example.code}</pre>
          </li>
        ))}
      </ul>
      <p className="u-hint">{t('extensions.guide.foot')}</p>
    </Card>
  )
}
