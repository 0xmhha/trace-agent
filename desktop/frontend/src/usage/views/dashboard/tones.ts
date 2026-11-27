// Thresholds that decide when a dashboard figure is flagged. They mirror the
// insight rules of the design (section 8) so that a tile and an insight do not
// disagree about what counts as high or low.
import type { Tone } from '../../ui'

/** Tool failure rate from which the figure is shown as a warning. */
const TOOL_FAILURE_WARN = 0.05
/** Tool failure rate of the "tool error rate" rule. */
const TOOL_FAILURE_BAD = 0.15
/** Cache hit ratio below which the "low cache hit" rule fires. */
const CACHE_HIT_LOW = 0.5

export function toolFailureTone(rate: number | null): Tone {
  if (rate == null) return 'muted'
  if (rate >= TOOL_FAILURE_BAD) return 'bad'
  if (rate >= TOOL_FAILURE_WARN) return 'warn'
  return 'plain'
}

export function isCacheHitLow(hitRatio: number | null): boolean {
  return hitRatio != null && hitRatio < CACHE_HIT_LOW
}
