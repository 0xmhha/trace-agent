import { Fragment, type ReactNode } from 'react'

const PLACEHOLDER = /\{(\w+)\}/

/**
 * Renders a translated sentence whose {name} placeholders stand for code
 * elements, so a message can place inline code wherever its language needs
 * it. Placeholders without a value stay as written. Everything is plain text;
 * no markup is parsed from the message.
 */
export function withCode(message: string, codes: Readonly<Record<string, string>>): ReactNode {
  // split with a capture group alternates text and placeholder names.
  const parts = message.split(PLACEHOLDER)
  return parts.map((part, index) => {
    if (index % 2 === 0) return <Fragment key={index}>{part}</Fragment>
    const code = Object.hasOwn(codes, part) ? codes[part] : undefined
    return code === undefined ? (
      <Fragment key={index}>{'{' + part + '}'}</Fragment>
    ) : (
      <code key={index}>{code}</code>
    )
  })
}
