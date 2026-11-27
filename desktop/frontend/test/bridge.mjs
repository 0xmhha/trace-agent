// The fake desktop bridge of the usage suites. Behind it the Go harness
// (desktop/usage_ia_test.go) answers through the real usage pipeline.

// Runs inside the page, so it can only use its argument.
function install({ headers }) {
  // calls counts Bridge.Usage, last keeps the newest response of each
  // method, native lists the calls that would open a dialog in the app and
  // locales the languages the page reported with Bridge.SetLocale.
  const usage = { calls: 0, last: {}, native: [], locales: [] }
  window.__usage = usage
  const native = (name, result) => async () => {
    usage.native.push(name)
    return result
  }
  window.go = {
    main: {
      Bridge: {
        Bootstrap: () => fetch('/_ticket', { headers }).then((r) => r.json()),
        Pick: async () => '',
        HasReceiver: async () => false,
        SetLocale: async (code) => {
          usage.locales.push(code)
        },
        Usage: async (method, params) => {
          usage.calls++
          const r = await fetch('/__usage', {
            method: 'POST',
            headers: { ...headers, 'Content-Type': 'application/json' },
            body: JSON.stringify({ method, params: JSON.parse(params) }),
          })
          const text = await r.text()
          if (!r.ok) throw new Error(text.trim())
          usage.last[method] = text
          return text
        },
        EnableUsageWatch: native('EnableUsageWatch', false),
        DisableUsageWatch: native('DisableUsageWatch'),
        CopyExtensionSettings: native('CopyExtensionSettings'),
      },
    },
  }
}

/** Installs the fake bridge in every document the page loads. */
export function installUsageBridge(page, headers) {
  return page.addInitScript(install, { headers })
}

/** What the fake bridge recorded in the current document. */
export function bridgeState(page) {
  return page.evaluate(() => window.__usage)
}
