import { useLayoutEffect, useRef, useState, type RefObject } from 'react'

/**
 * Tracks the content width of an element. Charts draw at real pixel sizes so
 * that text keeps its size while the card grows from narrow to wide windows.
 * The element must be mounted for the whole life of the component.
 */
export function useWidth<T extends HTMLElement>(): [RefObject<T | null>, number] {
  const ref = useRef<T>(null)
  const [width, setWidth] = useState(0)
  useLayoutEffect(() => {
    const element = ref.current
    if (!element) return
    setWidth(Math.floor(element.clientWidth))
    const observer = new ResizeObserver((entries) => {
      const next = entries[0]?.contentRect.width
      if (next != null) setWidth(Math.floor(next))
    })
    observer.observe(element)
    return () => observer.disconnect()
  }, [])
  return [ref, width]
}
