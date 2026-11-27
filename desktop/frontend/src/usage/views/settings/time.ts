/**
 * Reports whether an instant of the API is a real point in time. Go encodes
 * "never" as its zero time, year 1, which parses to a negative number.
 */
export function happened(at: string): boolean {
  return Date.parse(at) > 0
}
