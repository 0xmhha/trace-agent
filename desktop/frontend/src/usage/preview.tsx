// Development-only harness: `npx vite` then open /usage-preview.html.
// It mounts the usage pages on mock data so a view can be checked without the
// desktop process. The production build does not include this entry.
import { useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import '../app.css'
import { I18nProvider, LanguageSelect, initLocale } from '../i18n'
import { UsagePage, parseUsageHash, usageHash, usageNav, usageTitle, type UsageRoute } from './index'
import { mockApi } from './mock'

function read(): UsageRoute {
  return parseUsageHash(location.hash) ?? { view: 'dashboard' }
}

function Preview() {
  const [route, setRoute] = useState<UsageRoute>(read)
  useEffect(() => {
    const onHash = () => setRoute(read())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])
  const navigate = (next: UsageRoute) => {
    location.hash = usageHash(next)
  }
  return (
    <div style={{ maxWidth: 1180, margin: '0 auto', padding: '1rem 1.25rem' }}>
      <nav
        style={{
          display: 'flex',
          gap: '0.4rem',
          flexWrap: 'wrap',
          alignItems: 'center',
          marginBottom: '1rem',
        }}
      >
        {usageNav.map((n) => (
          <button
            key={n.view}
            className={route.view === n.view ? 'active' : ''}
            onClick={() => navigate({ view: n.view })}
          >
            {n.icon} {n.label}
          </button>
        ))}
        <span style={{ flex: 1 }} />
        <LanguageSelect />
      </nav>
      <h1 style={{ fontSize: '1.3rem', margin: '0 0 1rem' }}>{usageTitle(route)}</h1>
      <UsagePage api={mockApi} route={route} navigate={navigate} />
    </div>
  )
}

initLocale()
createRoot(document.getElementById('root')!).render(
  <I18nProvider>
    <Preview />
  </I18nProvider>,
)
