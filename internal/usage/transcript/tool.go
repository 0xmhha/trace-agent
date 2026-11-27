package transcript

import (
	"strings"

	"github.com/0xmhha/trace-agent/internal/usage"
)

const (
	// mcpSeparator divides the server and the tool of an MCP tool name.
	mcpSeparator = "__"
	// pluginServerSeparator divides the plugin and the server inside the
	// server part of a plugin-provided MCP tool.
	pluginServerSeparator = "_"
	// skillPluginSeparator divides the plugin and the skill of a plugin skill.
	skillPluginSeparator = ":"
)

// classify fills the fields of call that follow from the tool name and the
// decoded input: category, MCP server and tool, plugin, skill, sub-agent type
// and, for Bash, the program name. Nothing else of the input is kept.
func classify(call *usage.ToolCall, name string, input toolInput) {
	call.Name = label(name)
	call.Category = usage.ToolBuiltin
	switch {
	case strings.HasPrefix(name, usage.MCPToolPrefix):
		call.Category = usage.ToolMCP
		server, tool, plugin := splitMCP(name)
		call.MCPServer, call.MCPTool, call.Plugin = label(server), label(tool), label(plugin)
	case name == usage.ToolNameSkill:
		call.Category = usage.ToolSkill
		call.Skill = label(input.Skill)
		call.Plugin = label(skillPlugin(input.Skill))
	case name == usage.ToolNameAgent, name == usage.ToolNameTask:
		call.Category = usage.ToolAgent
		call.SubagentType = label(input.SubagentType)
	case name == usage.ToolNameBash:
		call.Target = program(input.Command)
	}
}

// splitMCP splits mcp__<server>__<tool>. When the server part has the shape
// plugin_<plugin>_<server> the plugin is returned and removed from the
// server. A server part that only looks like the prefix is left whole: a
// plugin without a server name is not that shape.
func splitMCP(name string) (server, tool, plugin string) {
	server, tool, _ = strings.Cut(strings.TrimPrefix(name, usage.MCPToolPrefix), mcpSeparator)
	rest, ok := strings.CutPrefix(server, usage.MCPPluginPrefix)
	if !ok {
		return server, tool, ""
	}
	owner, inner, ok := strings.Cut(rest, pluginServerSeparator)
	if !ok || owner == "" || inner == "" {
		return server, tool, ""
	}
	return inner, tool, owner
}

// skillPlugin returns the plugin of a <plugin>:<skill> name, or nothing for a
// skill that does not come from a plugin.
func skillPlugin(skill string) string {
	plugin, rest, ok := strings.Cut(skill, skillPluginSeparator)
	if !ok || plugin == "" || rest == "" {
		return ""
	}
	return plugin
}

const (
	// maxProgramLen is the longest program name stored as a Bash target.
	maxProgramLen = 32
	// maxCommandScan bounds how much of a command is tokenised. The program
	// name is at the front; a here-document behind it can be megabytes.
	maxCommandScan = 4 << 10
)

// Shell syntax the word splitter recognises beyond quotes.
const (
	substitutionOpen = "$("
	lineContinuation = "\\\n"
	andOperator      = "&&"
	// statementEnds are the characters that end a statement and let the
	// next one run unconditionally.
	statementEnds = ";\n\r"
)

// Options of the command wrappers that take their value as the next word.
// Each list is space-delimited on both ends so that a lookup is one
// substring test.
const (
	sudoValueOptions = " -u -g -h -p -C -D -R -T -U --user --group --host --prompt --chdir --chroot "
	envValueOptions  = " -u -C -S --unset --chdir --split-string "
	timeValueOptions = " -f -o --format --output "
	noValueOptions   = " "
)

// wrapperValueOptions reports whether name is a command that only runs
// another command, and which of its options consume the following word.
// This switch is the one place that knows the wrappers.
func wrapperValueOptions(name string) (string, bool) {
	switch name {
	case "sudo":
		return sudoValueOptions, true
	case "env":
		return envValueOptions, true
	case "time":
		return timeValueOptions, true
	case "nohup":
		return noValueOptions, true
	}
	return "", false
}

// program returns the name of the program a shell command starts with:
// leading VAR=value assignments and the wrappers sudo, env, time and nohup
// are stepped over, and a path prefix is removed. A statement that only
// assigns variables runs no program, so the search moves on to the statement
// after it. It returns the empty string unless the name matches
// [A-Za-z0-9._+-]{1,32}. Only this name leaves the function; arguments are
// never returned, and a name that looks like a secret is dropped rather than
// stored.
func program(command string) string {
	command = strings.TrimLeft(command, jsonSpace)
	truncated := len(command) > maxCommandScan
	if truncated {
		command = command[:maxCommandScan]
	}
	words := shellWords{text: command}
	wrapper, assigned := "", false
	for {
		word, ok := words.next()
		if truncated && words.pos >= len(words.text) {
			// A word that reaches the cut of a truncated command may be
			// incomplete and is not trusted.
			return wrapper
		}
		if !ok {
			// Nothing runs under a wrapper, so the wrapper is the program:
			// "env | sort" runs env.
			if wrapper != "" || !assigned || !words.nextStatement() {
				return wrapper
			}
			assigned = false
			continue
		}
		if isAssignment(word) {
			assigned = true
			continue
		}
		name := word[strings.LastIndexByte(word, '/')+1:]
		valued, ok := wrapperValueOptions(name)
		if !ok {
			return programName(name)
		}
		wrapper = name
		words.skipOptions(valued)
	}
}

// programName returns name when it is an acceptable target and the empty
// string otherwise.
func programName(name string) string {
	if len(name) == 0 || len(name) > maxProgramLen {
		return ""
	}
	for i := 0; i < len(name); i++ {
		if !programChar(name[i]) {
			return ""
		}
	}
	if _, masked := usage.Mask(name); masked > 0 {
		return ""
	}
	return name
}

func programChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '.', c == '_', c == '+', c == '-':
		return true
	}
	return false
}

// isAssignment reports whether word is a NAME=value environment assignment.
func isAssignment(word string) bool {
	name, _, ok := strings.Cut(word, "=")
	if !ok || name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		letter := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// shellWords splits the start of a shell command into words. It understands
// quotes, backslashes and command substitutions well enough to find where a
// word ends. It is not a shell parser: keywords, redirections and expansions
// are ordinary words or operators to it.
type shellWords struct {
	text string
	pos  int
}

// next returns the next word with its quoting removed. It reports false at
// a control operator or the end of the text and stays in front of the
// operator, which nextStatement may then step over.
func (w *shellWords) next() (string, bool) {
	var word []byte
	started := false
	for w.pos < len(w.text) {
		c := w.text[w.pos]
		switch {
		case isBlank(c) && !started:
			w.pos++
			continue
		case isBlank(c), isOperator(c):
			return string(word), started
		case strings.HasPrefix(w.text[w.pos:], lineContinuation):
			// The shell removes a backslash-newline pair; it neither ends
			// nor starts a word.
			w.pos += len(lineContinuation)
			continue
		case c == '\\':
			w.pos++
			if w.pos < len(w.text) {
				word = append(word, w.text[w.pos])
				w.pos++
			}
		case c == '\'':
			word = w.quoted(word, '\'', false)
		case c == '"':
			word = w.quoted(word, '"', true)
		case c == '`':
			w.pos++
			w.skipSubstitution('`')
			word = append(word, c)
		case c == '$' && strings.HasPrefix(w.text[w.pos:], substitutionOpen):
			w.pos += len(substitutionOpen)
			w.skipSubstitution(')')
			word = append(word, c)
		default:
			word = append(word, c)
			w.pos++
		}
		started = true
	}
	return string(word), started
}

// quoted appends the text of the quoted section that starts at w.pos and
// moves past its closing quote. An unterminated section runs to the end.
func (w *shellWords) quoted(word []byte, quote byte, escapes bool) []byte {
	w.pos++
	for w.pos < len(w.text) {
		c := w.text[w.pos]
		w.pos++
		switch {
		case c == quote:
			return word
		case escapes && c == '\\' && w.pos < len(w.text):
			word = append(word, w.text[w.pos])
			w.pos++
		default:
			word = append(word, c)
		}
	}
	return word
}

// skipSubstitution moves past a command substitution whose opening has been
// consumed, up to and including the closing character. What runs inside it
// is not the program of the command; the caller keeps one marker character
// so that the word is not empty and cannot pass as a program name. Nested
// parentheses and quotes are balanced; an unterminated one runs to the end.
func (w *shellWords) skipSubstitution(closing byte) {
	depth := 1
	for w.pos < len(w.text) {
		c := w.text[w.pos]
		w.pos++
		switch {
		case c == '\\':
			w.pos = min(w.pos+1, len(w.text))
		case c == closing:
			depth--
			if depth == 0 {
				return
			}
		case closing == ')' && c == '(':
			depth++
		case closing == ')' && (c == '\'' || c == '"'):
			// quoted expects to stand on the opening quote.
			w.pos--
			w.quoted(nil, c, c == '"')
		}
	}
}

// nextStatement moves to the statement after the current one and reports
// whether there is one that runs whenever the current one succeeds: after a
// semicolon, a newline or "&&". A pipe, "||" or "&" does not qualify.
func (w *shellWords) nextStatement() bool {
	rest := w.text[w.pos:]
	switch {
	case strings.HasPrefix(rest, andOperator):
		w.pos += len(andOperator)
	case rest != "" && endsStatement(rest[0]):
		w.pos++
	default:
		return false
	}
	for w.pos < len(w.text) && (isBlank(w.text[w.pos]) || endsStatement(w.text[w.pos])) {
		w.pos++
	}
	return true
}

func endsStatement(c byte) bool {
	return strings.IndexByte(statementEnds, c) >= 0
}

// skipOptions moves past the options that follow a wrapper, including the
// value of an option listed in valued. It stops before the first word that
// is not an option, which is the wrapped command or an assignment.
func (w *shellWords) skipOptions(valued string) {
	for {
		before := w.pos
		word, ok := w.next()
		if !ok || !strings.HasPrefix(word, "-") {
			w.pos = before
			return
		}
		if word == "--" {
			return
		}
		if takesValue(word, valued) {
			w.next()
		}
	}
}

// takesValue reports whether option leaves its value in the next word. A
// cluster of short options such as -Eu does when its last letter is one that
// takes a value; in -uroot the value is already attached.
func takesValue(option, valued string) bool {
	if strings.HasPrefix(option, "--") || len(option) <= 2 {
		return strings.Contains(valued, " "+option+" ")
	}
	for i := 1; i < len(option); i++ {
		if strings.Contains(valued, " -"+option[i:i+1]+" ") {
			return i == len(option)-1
		}
	}
	return false
}

func isBlank(c byte) bool {
	return c == ' ' || c == '\t'
}

// isOperator reports whether c ends a simple command or starts a
// redirection, a group or a substitution.
func isOperator(c byte) bool {
	switch c {
	case ';', '|', '&', '(', ')', '<', '>', '\n', '\r':
		return true
	}
	return false
}
