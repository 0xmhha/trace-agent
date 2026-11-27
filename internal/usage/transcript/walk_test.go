package transcript

import (
	"sort"
	"strings"
	"testing"
)

func TestWalkObject(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		complete bool
		want     string // visited members as key=value, sorted, joined by a bar
	}{
		{"empty object", `{}`, true, ""},
		{"empty object with spaces", " { } \r", true, ""},
		{"scalars", `{"a":1,"b":true,"c":null,"d":-2.5e3}`, true, "a=1|b=true|c=null|d=-2.5e3"},
		{"string", `{"a":"x"}`, true, `a="x"`},
		{"spaces around tokens", "{ \"a\" : 1 ,\t\"b\" : \"x\" }", true, `a=1|b="x"`},
		{"nested values are returned whole", `{"a":{"b":[1,{"c":2}]},"d":[]}`, true, `a={"b":[1,{"c":2}]}|d=[]`},
		{"brackets inside strings", `{"a":"}]{[","b":["}","]"],"c":1}`, true, `a="}]{["|b=["}","]"]|c=1`},
		{"escaped quote", `{"a":"x\"y","b":1}`, true, `a="x\"y"|b=1`},
		{"escaped backslash before the quote", `{"a":"x\\","b":1}`, true, `a="x\\"|b=1`},
		{"odd run of backslashes", `{"a":"x\\\"","b":1}`, true, `a="x\\\""|b=1`},
		{"escaped quote in a key", `{"a\"b":1}`, true, `a\"b=1`},
		{"duplicate key", `{"a":1,"a":2}`, true, "a=1|a=2"},

		{"empty input", ``, false, ""},
		{"not an object", `[1]`, false, ""},
		{"text", `hello`, false, ""},
		{"unterminated object", `{"a":1`, false, "a=1"},
		{"cut inside a string", `{"a":1,"b":"xy`, false, "a=1"},
		{"cut inside a nested value", `{"a":1,"b":{"c":[1,2`, false, "a=1"},
		{"cut after the colon", `{"a":1,"b":`, false, "a=1"},
		{"cut after a comma", `{"a":1,`, false, "a=1"},
		{"missing colon", `{"a" 1}`, false, ""},
		{"missing comma", `{"a":1 "b":2}`, false, "a=1"},
		{"bare key", `{a:1}`, false, ""},
		{"missing value", `{"a":}`, false, ""},
		{"trailing text", `{"a":1} x`, false, "a=1"},
		{"two objects", `{"a":1}{"b":2}`, false, "a=1"},
		{"unbalanced closing bracket", `{"a":]}`, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seen []string
			complete := walkObject([]byte(tc.input), func(key, value []byte) {
				seen = append(seen, string(key)+"="+string(value))
			})
			sort.Strings(seen)
			if got := strings.Join(seen, "|"); complete != tc.complete || got != tc.want {
				t.Fatalf("walkObject(%s) = %v, members %s\nwant %v, members %s", tc.input, complete, got, tc.complete, tc.want)
			}
		})
	}
}

func TestMember(t *testing.T) {
	cases := []struct {
		name, input, key, want string
	}{
		{"found", `{"a":1,"content":[1,2],"b":2}`, "content", "[1,2]"},
		{"last occurrence wins", `{"content":1,"content":"two"}`, "content", `"two"`},
		{"absent", `{"a":1}`, "content", ""},
		{"nested key is not a member", `{"a":{"content":1}}`, "content", ""},
		{"not an object", `"content"`, "content", ""},
		{"empty", ``, "content", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(member([]byte(tc.input), tc.key)); got != tc.want {
				t.Fatalf("member(%s, %q) = %s, want %s", tc.input, tc.key, got, tc.want)
			}
		})
	}
}

func TestArrayLen(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{`[]`, 0},
		{` [ ] `, 0},
		{`[1]`, 1},
		{`[1, "a,b", {"c":[1,2]}, [3,4]]`, 4},
		{`[1,2`, 0},
		{`[1 2]`, 0},
		{`[1,]`, 0},
		{`{"a":1}`, 0},
		{`null`, 0},
		{``, 0},
	}
	for _, tc := range cases {
		if got := arrayLen([]byte(tc.input)); got != tc.want {
			t.Errorf("arrayLen(%s) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestJSONString(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"plain", `"main"`, "main"},
		{"escapes", `"a\tbé\"c"`, "a\tbé\"c"},
		{"surrogate pair", `"😀"`, "😀"},
		{"multi-byte", `"세션"`, "세션"},
		{"invalid utf-8 is replaced", "\"a\xffb\"", "a�b"},
		{"empty", `""`, ""},
		{"null", `null`, ""},
		{"number", `12`, ""},
		{"object", `{"a":"b"}`, ""},
		{"bad escape", `"a\qb"`, ""},
		{"unterminated", `"abc`, ""},
		{"nothing", ``, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jsonString([]byte(tc.input)); got != tc.want {
				t.Fatalf("jsonString(%s) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestNumbers(t *testing.T) {
	cases := []struct {
		input     string
		wantInt   int64
		isInt     bool
		wantCount int64
		wantFloat float64
		isFloat   bool
	}{
		{"0", 0, true, 0, 0, true},
		{"42", 42, true, 42, 42, true},
		{"-7", -7, true, 0, -7, true},
		{"12.9", 12, true, 12, 12.9, true},
		{"-12.9", -12, true, 0, -12.9, true},
		{"1e3", 1000, true, 1000, 1000, true},
		{"9223372036854775807", 9223372036854775807, true, 9223372036854775807, 9223372036854775807, true},
		{"9223372036854775808", 0, false, 0, 9223372036854775808, true},
		{"1e400", 0, false, 0, 0, false},
		{`"42"`, 0, false, 0, 0, false},
		{"null", 0, false, 0, 0, false},
		{"true", 0, false, 0, 0, false},
		{"NaN", 0, false, 0, 0, false},
		{"inf", 0, false, 0, 0, false},
		{"+1", 0, false, 0, 0, false},
		{"", 0, false, 0, 0, false},
		{"-", 0, false, 0, 0, false},
		{"{}", 0, false, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			if got, ok := integer([]byte(tc.input)); got != tc.wantInt || ok != tc.isInt {
				t.Errorf("integer = %d, %v; want %d, %v", got, ok, tc.wantInt, tc.isInt)
			}
			if got := count([]byte(tc.input)); got != tc.wantCount {
				t.Errorf("count = %d, want %d", got, tc.wantCount)
			}
			if got, ok := float([]byte(tc.input)); got != tc.wantFloat || ok != tc.isFloat {
				t.Errorf("float = %v, %v; want %v, %v", got, ok, tc.wantFloat, tc.isFloat)
			}
		})
	}
}

func TestDecodeIsLenientAboutTypesOnly(t *testing.T) {
	type inner struct {
		Flag bool `json:"flag"`
	}
	type target struct {
		Name  string `json:"name"`
		Count number `json:"count"`
		Inner inner  `json:"inner"`
	}
	cases := []struct {
		name  string
		input string
		ok    bool
		want  target
	}{
		{"all as expected", `{"name":"a","count":3,"inner":{"flag":true}}`, true, target{Name: "a", Count: 3, Inner: inner{Flag: true}}},
		{"wrong types leave zero and keep the rest", `{"name":7,"count":"x","inner":"y"}`, true, target{}},
		{"one wrong type among good ones", `{"name":["a"],"count":2.7}`, true, target{Count: 2}},
		{"null members", `{"name":null,"count":null,"inner":null}`, true, target{}},
		{"not an object", `"text"`, true, target{}},
		{"syntax error", `{"name":"a",}`, false, target{}},
		{"cut off", `{"name":"a"`, false, target{}},
		{"empty", ``, false, target{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got target
			ok := decode([]byte(tc.input), &got)
			if ok != tc.ok || (ok && got != tc.want) {
				t.Fatalf("decode(%s) = %v, %+v; want %v, %+v", tc.input, ok, got, tc.ok, tc.want)
			}
		})
	}
}

func TestCutRunes(t *testing.T) {
	cases := []struct {
		input string
		n     int
		want  string
	}{
		{"", 3, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
		{"가나다라", 3, "가나다"},
		{"가나", 3, "가나"},
		{"a가b나", 2, "a가"},
		{"abc", 0, ""},
	}
	for _, tc := range cases {
		if got := cutRunes(tc.input, tc.n); got != tc.want {
			t.Errorf("cutRunes(%q, %d) = %q, want %q", tc.input, tc.n, got, tc.want)
		}
	}
}
