import { obj, str, type Row } from './data'
const files = import.meta.glob<string>(
  '../../../internal/transport/operations/*.graphql',
  { eager: true, query: '?raw', import: 'default' },
)
const docs = Object.fromEntries(
  Object.entries(files).map(([path, text]) => [
    path.split('/').at(-1)!.replace('.graphql', ''),
    text,
  ]),
)
type NativeBridge = {
  Bootstrap: () => Promise<{ url: string; ticket: string }>
  Pick: (kind: string) => Promise<string>
  PickHistory: (id: string) => Promise<string>
  HasReceiver: (id: string) => Promise<boolean>
  PickObserver: (id: string, kind: string, name: string) => Promise<string>
  CopyReceiverSettings: (id: string) => Promise<void>
}
declare global {
  interface Window {
    go?: { main?: { Bridge?: NativeBridge } }
  }
}
export function native() {
  const b = window.go?.main?.Bridge
  if (!b) throw Error('설치한 앱에서 열어 주세요.')
  return b
}
function watchFields(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(watchFields)
  if (value === null || typeof value !== 'object') return value
  return Object.fromEntries(
    Object.entries(value).map(([key, child]) => [
      /^[A-Za-z]+PageItems$/.test(key) ? 'items' : key,
      watchFields(child),
    ]),
  )
}
type Listener = {
  name: string
  next: (value: Row) => void
  fail: (reason: string) => void
  done: () => void
}
export class Connection {
  private id = 0
  private requests = new Map<string, Listener>()
  private constructor(private socket: WebSocket) {}
  static async open(closed: () => void): Promise<Connection> {
    const { url, ticket } = await native().Bootstrap()
    const target = new URL(url)
    if (
      target.protocol !== 'ws:' ||
      target.hostname !== '127.0.0.1' ||
      !target.port ||
      target.username ||
      target.password ||
      target.pathname !== '/graphql' ||
      target.search ||
      target.hash ||
      !ticket
    )
      throw Error('로컬 연결 정보를 확인할 수 없습니다.')
    // Narrow the static loopback policy to this app instance's listener.
    if (!document.getElementById('trace-connection-policy')) {
      const policy = document.createElement('meta')
      policy.id = 'trace-connection-policy'
      policy.httpEquiv = 'Content-Security-Policy'
      policy.content = "connect-src 'self' " + target.origin
      document.head.appendChild(policy)
    }
    const ws = new WebSocket(target, 'graphql-transport-ws'),
      connection = new Connection(ws)
    return new Promise((resolve, reject) => {
      let ready = false
      const timer = setTimeout(() => ws.close(), 8000)
      ws.onopen = () =>
        ws.send(
          JSON.stringify({ type: 'connection_init', payload: { ticket } }),
        )
      ws.onmessage = (e) => {
        try {
          const m = obj(JSON.parse(e.data))
          if (m.type === 'connection_ack' && !ready) {
            clearTimeout(timer)
            ready = true
            resolve(connection)
            return
          }
          if (m.type === 'ping') {
            ws.send(JSON.stringify({ type: 'pong' }))
            return
          }
          const handler = connection.requests.get(str(m.id))
          if (!handler) return
          const payload = obj(m.payload)
          if (m.type === 'error' || Array.isArray(payload.errors)) {
            const errors = m.type === 'error' ? m.payload : payload.errors
            const message = Array.isArray(errors)
              ? str(obj(errors[0]).message)
              : ''
            handler.fail(message || '요청을 처리할 수 없습니다.')
            connection.requests.delete(str(m.id))
            return
          }
          if (m.type === 'next') {
            const value = obj(payload.data)[handler.name]
            handler.next(
              obj(handler.name === 'watch' ? watchFields(value) : value),
            )
          }
          if (m.type === 'complete') {
            handler.done()
            connection.requests.delete(str(m.id))
          }
        } catch {
          ws.close(4400, 'invalid frame')
        }
      }
      ws.onclose = () => {
        clearTimeout(timer)
        if (!ready) reject(Error('로컬 저장소에 연결하지 못했습니다.'))
        connection.requests.forEach((h) => h.fail('연결이 끊겼습니다.'))
        connection.requests.clear()
        if (ready) closed()
      }
      ws.onerror = () => ws.close()
    })
  }
  subscribe(
    name: string,
    variables: Row,
    next: Listener['next'],
    fail: Listener['fail'],
    done = () => {},
  ): () => void {
    const query = docs[name]
    if (!query || this.socket.readyState !== WebSocket.OPEN) {
      fail('사용할 수 없는 연산입니다.')
      return () => {}
    }
    const id = String(++this.id)
    this.requests.set(id, { name, next, fail, done })
    this.socket.send(
      JSON.stringify({
        id,
        type: 'subscribe',
        payload: { operationName: name, query, variables },
      }),
    )
    return () => {
      if (this.requests.delete(id) && this.socket.readyState === WebSocket.OPEN)
        this.socket.send(JSON.stringify({ id, type: 'complete' }))
    }
  }
  query(name: string, variables: Row): Promise<Row> {
    return new Promise((resolve, reject) => {
      let received = false
      let cancel = () => {}
      const timer = setTimeout(() => {
        cancel()
        reject(Error('응답 시간이 초과됐습니다.'))
      }, 15000)
      cancel = this.subscribe(
        name,
        variables,
        (data) => {
          clearTimeout(timer)
          received = true
          resolve(data)
        },
        (reason) => {
          clearTimeout(timer)
          reject(Error(reason))
        },
        () => {
          clearTimeout(timer)
          if (!received) reject(Error('빈 응답입니다.'))
        },
      )
    })
  }
  close() {
    this.socket.close()
  }
}
