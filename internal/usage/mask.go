package usage

import (
	"regexp"
	"strings"
)

// MaskToken replaces every masked secret.
const MaskToken = "[가림]"

// Thresholds of the assignment rule for values that are not quoted.
const (
	minBareSecretLen = 8
)

// A maskRule masks one capture group of its pattern: group 0 is the whole
// match. When keyGroup is set, the rule fires only if secretAssignment
// accepts the key and the value, which keeps counters such as
// "input_tokens": 1234 readable.
type maskRule struct {
	pattern     *regexp.Regexp
	secretGroup int
	keyGroup    int
}

const secretWords = `password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|encryption[_-]?key|credential|[_-]pass`

var maskRules = []maskRule{
	{pattern: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----[\s\S]*?(?:-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----|\z)`)},
	{pattern: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{pattern: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)},
	{pattern: regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`)},
	{pattern: regexp.MustCompile(`\b[sr]k_(?:live|test)_[A-Za-z0-9]{16,}`)},
	{pattern: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`)},
	{pattern: regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`)},
	{pattern: regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{16,}`)},
	{pattern: regexp.MustCompile(`\bATATT[A-Za-z0-9_=-]{16,}`)},
	{pattern: regexp.MustCompile(`\batl_[A-Za-z0-9_-]{16,}`)},
	{pattern: regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{pattern: regexp.MustCompile(`\bxapp-[A-Za-z0-9-]{10,}`)},
	{pattern: regexp.MustCompile(`\bhvs\.[A-Za-z0-9_-]{20,}`)},
	{pattern: regexp.MustCompile(`\bnpm_[A-Za-z0-9]{30,}`)},
	{pattern: regexp.MustCompile(`\bhf_[A-Za-z0-9]{30,}`)},
	{pattern: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
	{pattern: regexp.MustCompile(`(?i)\bBearer\s+([A-Za-z0-9._~+/=-]{16,})`), secretGroup: 1},
	{pattern: regexp.MustCompile(`(?i)\bAuthorization\\?["']?\s*[:=]\s*\\?["']?Basic\s+([A-Za-z0-9+/=]{8,})`), secretGroup: 1},
	{pattern: regexp.MustCompile(`(?i)\bCookie\\?["']?\s*[:=]\s*\\?["']?([^\n"'\\]{8,})`), secretGroup: 1},
	{pattern: regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^/\s:@]+:([^@\s/]{1,256})@`), secretGroup: 1},
	{pattern: regexp.MustCompile(`(?i)\bhttps?://([A-Za-z0-9_-]{20,})@`), secretGroup: 1},
	{pattern: regexp.MustCompile(`(?i)(?:^|\s)--?(?:password|passwd|pass|token|secret|api-?key|private-?key|access-?key|auth-?token)(?:=|\s+)("[^"\n]{1,256}"|'[^'\n]{1,256}'|[^\s"'-][^\s"']{3,255})`), secretGroup: 1},
	{pattern: regexp.MustCompile(`(?:^|\s)(?:-u|--user)\s+[^\s:]{1,128}:([^\s"']{1,256})`), secretGroup: 1},
	{
		pattern:     regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(?:` + secretWords + `)[A-Za-z0-9_.-]*)\\?["']?\s*[:=]\s*(\\"[^"\n]{4,}?\\"|"[^"\n]{4,}"|'[^'\n]{4,}'|[^\s"',;\\]{4,})`),
		secretGroup: 2,
		keyGroup:    1,
	},
}

var (
	secretKeyPattern = regexp.MustCompile(`(?i)(?:` + secretWords + `)`)
	// Keys that contain a secret word but name a count, a label or a location.
	benignKeyPattern = regexp.MustCompile(`(?i)(?:tokens|tokeni[sz]e|token[_-]?(?:count|limit|type|usage|budget|ttl|len|length))|(?:name|names|endpoint|url|uri|path|file|dir|id|ids|type|len|length|count|limit|ttl|expiry|expires|header|field|param|env|var|prefix|suffix|regex|pattern|policy|required|enabled|mode)$`)
	literalPattern   = regexp.MustCompile(`(?i)^(?:-?\d+(?:\.\d+)?|true|false|null|nil|none|undefined)$`)
	callPattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*[(\[<]`)
)

// SecretKey reports whether a field or attribute name looks like it holds a
// secret, so that its whole value should be masked.
func SecretKey(name string) bool {
	return secretKeyPattern.MatchString(name) && !benignKeyPattern.MatchString(name)
}

// secretAssignment decides whether value, assigned to key, is masked.
func secretAssignment(key, value string) bool {
	if !SecretKey(key) {
		return false
	}
	bare := strings.Trim(value, `"'\`)
	if literalPattern.MatchString(bare) {
		return false
	}
	if bare != value {
		return true
	}
	// An unquoted value is masked only when it looks like a generated secret
	// rather than a word, a type or a call.
	return len(bare) >= minBareSecretLen && strings.ContainsAny(bare, "0123456789") && !callPattern.MatchString(bare)
}

// Mask replaces secret-looking values in text with MaskToken and returns the
// number of replacements. A value that an earlier rule already masked is left
// alone and not counted again. It is a display safeguard for locally stored
// transcripts, not a guarantee that no secret remains: it does not mask
// e-mail addresses, IP addresses, host names or bare hex strings.
func Mask(text string) (string, int) {
	total := 0
	for _, rule := range maskRules {
		matches := rule.pattern.FindAllStringSubmatchIndex(text, -1)
		if len(matches) == 0 {
			continue
		}
		var out strings.Builder
		last := 0
		for _, m := range matches {
			start, end := m[2*rule.secretGroup], m[2*rule.secretGroup+1]
			if start < 0 || strings.Contains(text[start:end], MaskToken) {
				continue
			}
			if rule.keyGroup > 0 && !secretAssignment(text[m[2*rule.keyGroup]:m[2*rule.keyGroup+1]], text[start:end]) {
				continue
			}
			out.WriteString(text[last:start])
			out.WriteString(MaskToken)
			last = end
			total++
		}
		if total == 0 || last == 0 {
			continue
		}
		out.WriteString(text[last:])
		text = out.String()
	}
	return text, total
}
