import type { Coverage, ToolUsage } from '../../types'
import { Empty, Stat, fmtInt, fmtPct, ratio } from '../../ui'
import { categories, failureTone, type CategoryMeta } from './model'
import { t } from '../../../i18n'

const SHARE_BAR_HEIGHT = 8
const SHARE_GAP_PX = 2
/**
 * Smallest drawn share of a category that has calls. A category with a few
 * calls would otherwise be narrower than the gap beside it and vanish. The
 * legend always shows the true share.
 */
const SHARE_MIN_DRAWN = 0.015

interface Share {
  meta: CategoryMeta
  calls: number
  /** True share of all calls, null when there are no calls. */
  share: number | null
}

function sharesOf(tools: readonly ToolUsage[], total: number): Share[] {
  return categories.map((meta) => {
    const calls = tools.reduce((n, tool) => (tool.category === meta.id ? n + tool.calls : n), 0)
    return { meta, calls, share: ratio(calls, total) }
  })
}

/** Drawn widths in [0, 1]: true shares lifted to the minimum and scaled back to sum to one. */
function drawnWidths(shares: readonly Share[]): number[] {
  const lifted = shares.map((s) => (s.calls > 0 ? Math.max(s.share ?? 0, SHARE_MIN_DRAWN) : 0))
  const sum = lifted.reduce((a, b) => a + b, 0)
  return lifted.map((w) => (sum > 0 ? w / sum : 0))
}

const pct = (part: number) => part * 100 + '%'

/** Thin segmented bar: one whole split by category. The legend below carries the names and numbers. */
function ShareBar({ shares }: { shares: readonly Share[] }) {
  const widths = drawnWidths(shares)
  const starts = widths.map((_, i) => widths.slice(0, i).reduce((a, b) => a + b, 0))
  const label = t('tools.summary.shareAria', {
    parts: shares.map((s) => `${t(s.meta.labelKey)} ${fmtPct(s.share)}`).join(', '),
  })
  return (
    <svg
      className="u-tools-share-bar"
      width="100%"
      height={SHARE_BAR_HEIGHT}
      role="img"
      aria-label={label}
    >
      {shares.map((s, i) =>
        s.calls > 0 ? (
          <rect
            key={s.meta.id}
            x={pct(starts[i] ?? 0)}
            y={0}
            width={pct(widths[i] ?? 0)}
            height={SHARE_BAR_HEIGHT}
            style={{ fill: s.meta.color }}
          >
            <title>
              {t('tools.summary.shareTitle', {
                count: s.calls,
                n: fmtInt(s.calls),
                label: t(s.meta.labelKey),
                share: fmtPct(s.share),
              })}
            </title>
          </rect>
        ) : null,
      )}
      {/* Touching segments are separated by a gap in the surface colour, not by a border. */}
      {shares.map((s, i) =>
        s.calls > 0 && (starts[i] ?? 0) > 0 ? (
          <rect
            key={s.meta.id}
            className="u-tools-share-gap"
            x={pct(starts[i] ?? 0)}
            y={0}
            width={SHARE_GAP_PX}
            height={SHARE_BAR_HEIGHT}
            transform={`translate(${-SHARE_GAP_PX / 2} 0)`}
          />
        ) : null,
      )}
    </svg>
  )
}

/** Totals of the tool list and the share of calls by category. */
export function Summary({ tools, coverage }: { tools: readonly ToolUsage[]; coverage: Coverage }) {
  const calls = tools.reduce((n, tool) => n + tool.calls, 0)
  const errors = tools.reduce((n, tool) => n + tool.errors, 0)
  const pending = tools.reduce((n, tool) => n + tool.pending, 0)
  const rate = ratio(errors, calls)
  const shares = sharesOf(tools, calls)
  return (
    <div className="u-tools-summary">
      <Stat
        label={t('tools.summary.calls')}
        value={t('tools.summary.callsValue', { count: calls, n: fmtInt(calls) })}
        hint={t('tools.summary.tools', { count: tools.length, n: fmtInt(tools.length) })}
      />
      <Stat
        label={t('tools.summary.rate')}
        value={fmtPct(rate)}
        tone={failureTone(rate)}
        hint={t('tools.summary.rateHint', { failed: fmtInt(errors), pending: fmtInt(pending) })}
      />
      <div className="u-stat u-tools-share">
        <span className="u-stat-label">{t('tools.summary.share')}</span>
        {calls > 0 ? (
          <>
            <ShareBar shares={shares} />
            <ul className="u-tools-legend">
              {shares.map((s) => (
                <li key={s.meta.id}>
                  <i
                    className="u-tools-swatch"
                    style={{ background: s.meta.color }}
                    aria-hidden="true"
                  />
                  <span>{t(s.meta.labelKey)}</span>
                  <b>{t('tools.summary.legendCalls', { count: s.calls, n: fmtInt(s.calls) })}</b>
                  <span className="u-tools-legend-share">{fmtPct(s.share)}</span>
                </li>
              ))}
            </ul>
          </>
        ) : (
          <Empty coverage={coverage} />
        )}
      </div>
    </div>
  )
}
