package transcript

import (
	"strings"
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

func TestProgram(t *testing.T) {
	// Assembled at run time so that no secret-shaped literal is committed.
	keyID := "AK" + "IA" + strings.Repeat("Q", 16)
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"plain command", "git status", "git"},
		{"command alone", "ls", "ls"},
		{"leading whitespace", " \t\n  make all", "make"},
		{"absolute path", "/usr/bin/git log --oneline", "git"},
		{"relative path", "./scripts/build.sh --fast", "build.sh"},
		{"home path", "~/bin/tool run", "tool"},
		{"allowed punctuation", "g++-13 -o out main.cc", "g++-13"},
		{"dot command", ". ./env.sh", "."},

		{"one assignment", "FOO=1 make test", "make"},
		{"several assignments", "A=1 B=two _C3=x go build ./...", "go"},
		{"quoted assignment value", `MSG="hello world" NAME='a b' echo done`, "echo"},
		{"empty assignment value", "DEBUG= node app.js", "node"},
		{"only assignments", "FOO=1 BAR=2", ""},
		{"word with an equals sign that is no assignment", "--flag=1 cmd", ""},

		{"assignment statement, then a semicolon", "OUT=/work/out; mkdir -p $OUT", "mkdir"},
		{"assignment statements on their own lines", "A=1\nB=2\r\n\n  make all", "make"},
		{"assignment statement in an and-list", "A=1 && go test ./...", "go"},
		{"several assignment statements", "A=1; B=2 C=3;; D=4 && ls", "ls"},
		{"assignment statement before an assignment prefix", "A=1; B=2 env C=3 node x.js", "node"},
		{"assignment from a substitution", "ROOT=$(git rev-parse --show-toplevel)\ncd \"$ROOT\" && make", "make"},
		{"assignment from a substitution as a prefix", "NOW=$(date +%s) ./run.sh", "run.sh"},
		{"substitution with nested parentheses and quotes", `X=$(echo ")" '(' "\")" $(date)) ls -l`, "ls"},
		{"assignment from backticks", "X=`pwd; ls` wc -l", "wc"},
		{"unterminated substitution", "X=$(pwd", ""},
		{"unterminated backticks", "X=`pwd ; ls", ""},
		{"assignment statement in a pipeline", "A=1 | cat", ""},
		{"assignment statement in an or-list", "A=1 || echo failed", ""},
		{"assignment statement in the background", "A=1 & ls", ""},
		{"assignment statement and nothing else", "A=1;", ""},
		{"assignment statement before a subshell", "A=1; (cd /work && make)", ""},
		{"leading assignment and setup statements are skipped", "A=1; cd /work; make", "make"},
		{"cd before the real command", "cd /work/repo && git status", "git"},
		{"several setup statements", "cd /a && export GOFLAGS=-mod=mod && pushd b; go build ./...", "go"},
		{"cd alone is the program", "cd /work/repo", "cd"},
		{"cd in a pipeline is the program", "cd /work | cat", "cd"},
		{"cd before an or-list is the program", "cd /work || exit 1", "cd"},
		{"setup under a wrapper is not skipped", "sudo cd /root && ls", "cd"},
		{"export before the real command", "export PATH=/opt/bin:$PATH; npm test", "npm"},
		{"wrapper before a separator is the program", "sudo; ls", "sudo"},
		{"substitution as the program", "`which git` status", ""},

		{"line continuation after an assignment", "FOO=1 \\\n  go build ./...", "go"},
		{"line continuation after a wrapper", "sudo \\\n  -u deploy \\\n  make install", "make"},
		{"line continuation inside a word", "gi\\\nt status", "git"},
		{"line continuation after the program", "docker run \\\n  --rm image", "docker"},

		{"sudo", "sudo apt-get update", "apt-get"},
		{"sudo with a valued option", "sudo -u deploy systemctl restart app", "systemctl"},
		{"sudo with a flag", "sudo -E make install", "make"},
		{"sudo with clustered options", "sudo -Eu deploy make install", "make"},
		{"sudo with an attached value", "sudo -udeploy make install", "make"},
		{"sudo with a long option", "sudo --user deploy --preserve-env make", "make"},
		{"sudo with a long option and value in one word", "sudo --user=deploy make", "make"},
		{"sudo with double dash", "sudo -- ls -la", "ls"},
		{"sudo with an assignment", "sudo FOO=1 /usr/local/bin/tool", "tool"},
		{"sudo alone", "sudo -l", "sudo"},
		{"env", "env FOO=1 BAR=2 python3 run.py", "python3"},
		{"env with a flag", "env -i PATH=/bin sh -c true", "sh"},
		{"env with a valued option", "env -u HOME -C /work make", "make"},
		{"env alone", "env", "env"},
		{"env in a pipeline", "env | sort", "env"},
		{"time", "time go test ./...", "go"},
		{"time with a flag", "time -p make", "make"},
		{"nohup", "nohup ./server --port 8080 &", "server"},
		{"wrappers stack", "sudo env FOO=1 nohup time /opt/tool/bin/run now", "run"},
		{"assignment before a wrapper", "LC_ALL=C sudo -E sort file", "sort"},
		{"wrapper given by path", "/usr/bin/env python3 script.py", "python3"},

		{"semicolon ends the word", "ls;pwd", "ls"},
		{"pipe ends the word", "cat|wc", "cat"},
		{"and-list", "cd /work && make", "make"},
		{"redirection ends the word", "echo>out.txt", "echo"},
		{"newline ends the command", "pwd\nrm -rf build", "pwd"},
		{"wrapper cut off by an operator", "sudo && ls", "sudo"},
		{"subshell", "(cd /work && make)", ""},
		{"substitution", "$(which git) status", ""},
		{"variable as the command", "$EDITOR file", ""},
		{"comment", "# just a note", ""},
		{"negation", "! grep -q x file", ""},
		{"test bracket", "[ -f file ] && echo yes", ""},

		{"quoted program", `"git" status`, "git"},
		{"single-quoted program", `'/usr/bin/git' status`, "git"},
		{"quoted name with a space", `"my tool" run`, ""},
		{"escaped space in the name", `my\ tool run`, ""},
		{"unterminated quote", `"git status`, ""},
		{"empty quotes", `"" arg`, ""},
		{"trailing backslash", `git\`, "git"},

		{"empty", "", ""},
		{"blank", "   ", ""},
		{"path ending in a slash", "/usr/bin/ arg", ""},
		{"name at the length limit", strings.Repeat("a", maxProgramLen) + " x", strings.Repeat("a", maxProgramLen)},
		{"name over the length limit", strings.Repeat("a", maxProgramLen+1) + " x", ""},
		{"non-ascii name", "도구 실행", ""},
		{"name that looks like a credential", keyID + " --help", ""},
		{"assignments beyond the scanned prefix", "X=" + strings.Repeat("v", maxCommandScan) + " git status", ""},
		{"program cut by the scanned prefix", "X=" + strings.Repeat("v", maxCommandScan-6) + " gitlab-runner run", ""},
		{"program inside the scanned prefix", "X=" + strings.Repeat("v", maxCommandScan-10) + " git status", "git"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := program(tc.command); got != tc.want {
				t.Fatalf("program(%.60q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

// Arguments must never leak into the target, whatever the command.
func TestProgramNeverReturnsArguments(t *testing.T) {
	commands := []string{
		"curl -H 'X-Key: value' https://example.com/path",
		"sudo -u deploy psql -c 'select 1'",
		"FOO=bar env BAZ=qux docker run image",
	}
	for _, command := range commands {
		got := program(command)
		if got == "" || strings.ContainsAny(got, " '=:/") {
			t.Errorf("program(%q) = %q, want a bare program name", command, got)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		input toolInput
		want  usage.ToolCall
	}{
		{"builtin", "Read", toolInput{}, usage.ToolCall{Category: usage.ToolBuiltin}},
		{"builtin ignores stray input", "Edit", toolInput{Command: "git status", Skill: "x", SubagentType: "y"},
			usage.ToolCall{Category: usage.ToolBuiltin}},
		{"empty name", "", toolInput{}, usage.ToolCall{Category: usage.ToolBuiltin}},
		{"bash", "Bash", toolInput{Command: "FOO=1 /usr/bin/git push"}, usage.ToolCall{Category: usage.ToolBuiltin, Target: "git"}},
		{"bash without a command", "Bash", toolInput{}, usage.ToolCall{Category: usage.ToolBuiltin}},

		{"mcp", "mcp__issues__search", toolInput{}, usage.ToolCall{Category: usage.ToolMCP, MCPServer: "issues", MCPTool: "search"}},
		{"mcp server with underscores", "mcp__my_server__get_item", toolInput{},
			usage.ToolCall{Category: usage.ToolMCP, MCPServer: "my_server", MCPTool: "get_item"}},
		{"mcp tool containing the separator", "mcp__srv__a__b", toolInput{},
			usage.ToolCall{Category: usage.ToolMCP, MCPServer: "srv", MCPTool: "a__b"}},
		{"mcp without a tool", "mcp__srv", toolInput{}, usage.ToolCall{Category: usage.ToolMCP, MCPServer: "srv"}},
		{"mcp prefix alone", "mcp__", toolInput{}, usage.ToolCall{Category: usage.ToolMCP}},
		{"mcp from a plugin", "mcp__plugin_devkit_chain__status", toolInput{},
			usage.ToolCall{Category: usage.ToolMCP, MCPServer: "chain", MCPTool: "status", Plugin: "devkit"}},
		{"mcp from a plugin with a hyphen", "mcp__plugin_dev-kit_chain_bench__run_all", toolInput{},
			usage.ToolCall{Category: usage.ToolMCP, MCPServer: "chain_bench", MCPTool: "run_all", Plugin: "dev-kit"}},
		{"plugin prefix without a server", "mcp__plugin_solo__run", toolInput{},
			usage.ToolCall{Category: usage.ToolMCP, MCPServer: "plugin_solo", MCPTool: "run"}},
		{"plugin prefix with an empty plugin", "mcp__plugin__srv__run", toolInput{},
			usage.ToolCall{Category: usage.ToolMCP, MCPServer: "plugin", MCPTool: "srv__run"}},
		{"plugin prefix with an empty server", "mcp__plugin_kit___run", toolInput{},
			usage.ToolCall{Category: usage.ToolMCP, MCPServer: "plugin_kit", MCPTool: "_run"}},
		{"plugin prefix with a trailing separator", "mcp__plugin_kit_", toolInput{},
			usage.ToolCall{Category: usage.ToolMCP, MCPServer: "plugin_kit_"}},
		{"mcp is matched by prefix only", "xmcp__a__b", toolInput{}, usage.ToolCall{Category: usage.ToolBuiltin}},

		{"skill", "Skill", toolInput{Skill: "review"}, usage.ToolCall{Category: usage.ToolSkill, Skill: "review"}},
		{"skill from a plugin", "Skill", toolInput{Skill: "devkit:deploy-check"},
			usage.ToolCall{Category: usage.ToolSkill, Skill: "devkit:deploy-check", Plugin: "devkit"}},
		{"skill with a nested name", "Skill", toolInput{Skill: "kit:group:leaf"},
			usage.ToolCall{Category: usage.ToolSkill, Skill: "kit:group:leaf", Plugin: "kit"}},
		{"skill with an empty plugin", "Skill", toolInput{Skill: ":leaf"}, usage.ToolCall{Category: usage.ToolSkill, Skill: ":leaf"}},
		{"skill with an empty name", "Skill", toolInput{Skill: "kit:"}, usage.ToolCall{Category: usage.ToolSkill, Skill: "kit:"}},
		{"skill without input", "Skill", toolInput{}, usage.ToolCall{Category: usage.ToolSkill}},
		{"skill is case-sensitive", "skill", toolInput{Skill: "kit:leaf"}, usage.ToolCall{Category: usage.ToolBuiltin}},

		{"agent", "Agent", toolInput{SubagentType: "Explore"}, usage.ToolCall{Category: usage.ToolAgent, SubagentType: "Explore"}},
		{"task", "Task", toolInput{SubagentType: "kit:planner"}, usage.ToolCall{Category: usage.ToolAgent, SubagentType: "kit:planner"}},
		{"agent without a type", "Agent", toolInput{}, usage.ToolCall{Category: usage.ToolAgent}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got usage.ToolCall
			classify(&got, tc.tool, tc.input)
			want := tc.want
			want.Name = tc.tool
			if got != want {
				t.Fatalf("classify(%q)\n got  %+v\n want %+v", tc.tool, got, want)
			}
		})
	}
}
