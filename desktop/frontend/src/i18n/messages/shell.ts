// Messages of the shell namespace. Keys are written without the namespace
// prefix; components ask for "shell.<key>". Every key in en must exist in ko
// (the type below enforces it). Placeholders look like {name}; plural forms
// use the suffixes _one and _other.
export const en = {
} as const satisfies Record<string, string>

export const ko: Record<keyof typeof en, string> = {
}
