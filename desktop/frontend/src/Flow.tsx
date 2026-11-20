import { code, date, short, str, type Row } from './data'

/** The horizontal span represents the observed endpoints. A local event occupies one lane. */
export function Flow({
  lanes,
  events,
  selected,
  onSelect,
}: {
  lanes: Row[]
  events: Row[]
  selected: string
  onSelect: (id: string) => void
}) {
  const index = new Map(lanes.map((lane, i) => [str(lane.id), i + 1]))
  return (
    <div
      className="flowViewport"
      role="region"
      aria-label="참여자별 사건 흐름"
      tabIndex={0}
    >
      <div
        className="flow"
        style={{
          gridTemplateColumns:
            'repeat(' + Math.max(1, lanes.length) + ', minmax(100px, 1fr))',
        }}
      >
        {lanes.map((lane, i) => (
          <div
            key={str(lane.id)}
            className="flowLane"
            style={{
              gridColumn: i + 1,
              gridRow: '1 / span ' + (events.length + 1),
            }}
          >
            <small>
              {code(lane.kind)}
              <br />
              {short(lane.id)}
            </small>
          </div>
        ))}
        {events.map((e, i) => {
          const from = index.get(str(e.fromLaneId)),
            to = index.get(str(e.toLaneId))
          const start = Math.min(from ?? to ?? 1, to ?? from ?? 1),
            end = Math.max(from ?? to ?? 1, to ?? from ?? 1)
          return (
            <button
              key={str(e.id)}
              style={{ gridColumn: start + ' / ' + (end + 1), gridRow: i + 2 }}
              className={
                'flowEvent ' +
                (e.id === selected ? 'picked ' : '') +
                (start === end ? 'local' : '')
              }
              title={date(e.occurred) + ' · ' + code(e.grade)}
              aria-label={code(e.action) + ' · ' + short(e.id)}
              onClick={() => onSelect(str(e.id))}
            >
              {from && to && from > to ? '◀ ' : ''}
              {code(e.action)}
              {from && to && to > from ? ' ▶' : ''}
            </button>
          )
        })}
      </div>
    </div>
  )
}
