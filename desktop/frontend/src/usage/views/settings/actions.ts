import { useCallback, useEffect, useRef, useState } from 'react'
import type { UsageApi } from '../../api'
import type { Status } from '../../types'
import { errorText } from '../../../i18n'

/** Card a mutation belongs to; its outcome is shown inside that card. */
export type Scope = 'watch' | 'window' | 'projects' | 'manage'

/** Ids of the mutations that are not tied to a list row. */
export const ACTION = {
  watch: 'watch',
  window: 'window',
  rescan: 'rescan',
  clear: 'clear',
} as const

const PROJECT_ACTION_PREFIX = 'project:'
export function projectAction(key: string): string {
  return PROJECT_ACTION_PREFIX + key
}

export interface ActionNote {
  scope: Scope
  text: string
}

export interface Actions {
  /** The mutation in flight. Only one runs at a time. */
  pending: { scope: Scope; id: string } | null
  failure: ActionNote | null
  notice: ActionNote | null
  /**
   * Starts work unless another mutation is in flight. The resolved text, if
   * any, becomes the notice of the scope; a rejection becomes its failure.
   */
  run: (scope: Scope, id: string, work: () => Promise<string | void>) => void
}

/** Props shared by the cards that change settings. */
export interface CardProps {
  api: UsageApi
  status: Status
  actions: Actions
  /** Hands a fresh Status to the page and reloads what depends on it. */
  apply: (status: Status) => void
}

/**
 * Runs the page's mutations one at a time. Every mutation returns a whole
 * Status, so two overlapping ones could hand the page an older Status last.
 */
export function useActions(): Actions {
  const [pending, setPending] = useState<Actions['pending']>(null)
  const [failure, setFailure] = useState<ActionNote | null>(null)
  const [notice, setNotice] = useState<ActionNote | null>(null)
  // State updates are asynchronous; the ref stops a second click that lands
  // before the disabled state is rendered.
  const busy = useRef(false)
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])

  const run = useCallback<Actions['run']>((scope, id, work) => {
    if (busy.current) return
    busy.current = true
    setPending({ scope, id })
    setFailure(null)
    setNotice(null)
    // The native client can throw before it returns a promise, so the call
    // itself runs inside the chain.
    Promise.resolve()
      .then(work)
      .then(
        (text) => {
          if (alive.current && text) setNotice({ scope, text })
        },
        (reason: unknown) => {
          // The Wails bridge rejects with a plain string, fetch-like code with
          // an Error; errorText reads both and translates the stable codes.
          if (alive.current) setFailure({ scope, text: errorText(reason) })
        },
      )
      .finally(() => {
        busy.current = false
        if (alive.current) setPending(null)
      })
  }, [])

  return { pending, failure, notice, run }
}
