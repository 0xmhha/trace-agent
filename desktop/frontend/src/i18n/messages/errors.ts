// Messages of the errors namespace. Keys are written without the namespace
// prefix; components ask for "errors.<key>". Every key in en must exist in ko
// (the type below enforces it). Placeholders look like {name}; plural forms
// use the suffixes _one and _other.
export const en = {
  generic: 'The request could not be completed.',
} as const satisfies Record<string, string>

export const ko: Record<keyof typeof en, string> = {
  generic: '요청을 처리하지 못했습니다.',
}
