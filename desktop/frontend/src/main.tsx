import { createRoot } from 'react-dom/client'
import { App } from './App'
import { I18nProvider, initLocale } from './i18n'

initLocale()
createRoot(document.getElementById('root')!).render(
  <I18nProvider>
    <App />
  </I18nProvider>,
)
