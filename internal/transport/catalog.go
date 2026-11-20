// Package transport serves the embedded desktop client's bounded GraphQL operations.
package transport

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator/rules"
)

//go:embed schema.graphql source.graphql operations/*.graphql
var documents embed.FS

type object = map[string]any

type operation struct {
	document *ast.QueryDocument
	root     *ast.Field
	name     string
	kind     ast.Operation
}
type catalog struct {
	schema  *ast.Schema
	allowed map[[32]byte]operation
}

func loadCatalog() (*catalog, error) {
	source, err := documents.ReadFile("schema.graphql")
	if err != nil {
		return nil, err
	}
	extension, err := documents.ReadFile("source.graphql")
	if err != nil {
		return nil, err
	}
	schema, problem := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(source)}, &ast.Source{Name: "source.graphql", Input: string(extension)})
	if problem != nil {
		return nil, problem
	}
	files, err := documents.ReadDir("operations")
	if err != nil {
		return nil, err
	}
	c := &catalog{schema: schema, allowed: make(map[[32]byte]operation)}
	for _, f := range files {
		b, err := documents.ReadFile(path.Join("operations", f.Name()))
		if err != nil {
			return nil, err
		}
		doc, problems := gqlparser.LoadQueryWithRules(schema, string(b), rules.NewDefaultRules())
		if len(problems) > 0 {
			return nil, fmt.Errorf("%s: %w", f.Name(), problems)
		}
		def := doc.Operations[0]
		field := def.SelectionSet[0].(*ast.Field)
		c.allowed[documentHash(string(b))] = operation{doc, field, def.Name, def.Operation}
	}
	return c, nil
}
func documentHash(s string) [32]byte { return sha256.Sum256([]byte(strings.TrimSpace(s))) }

var errVariables = errors.New("invalid operation variables")

func (c *catalog) resolve(query, name string, vars object) (operation, object, error) {
	op, ok := c.allowed[documentHash(query)]
	if !ok || name != "" && name != op.name {
		return operation{}, nil, errors.New("operation is not registered")
	}
	def := op.document.Operations[0]
	for k := range vars {
		if def.VariableDefinitions.ForName(k) == nil {
			return operation{}, nil, errVariables
		}
	}
	out := object{}
	for _, v := range def.VariableDefinitions {
		value, exists := vars[v.Variable]
		if !exists && v.DefaultValue != nil {
			value, _ = v.DefaultValue.Value(nil)
			exists = true
		}
		if !exists && !v.Type.NonNull {
			continue
		}
		coerced, err := c.input(v.Type, value)
		if err != nil {
			return operation{}, nil, err
		}
		out[v.Variable] = coerced
	}
	return op, out, nil
}
func (c *catalog) input(t *ast.Type, value any) (any, error) {
	if value == nil {
		if t.NonNull {
			return nil, errVariables
		}
		return nil, nil
	}
	if t.Elem != nil {
		a, ok := value.([]any)
		if !ok || len(a) > 200 {
			return nil, errVariables
		}
		for i, v := range a {
			item, err := c.input(t.Elem, v)
			if err != nil {
				return nil, err
			}
			a[i] = item
		}
		return a, nil
	}
	def := c.schema.Types[t.NamedType]
	switch def.Kind {
	case ast.InputObject:
		o, ok := value.(object)
		if !ok {
			return nil, errVariables
		}
		for k := range o {
			if def.Fields.ForName(k) == nil {
				return nil, errVariables
			}
		}
		for _, f := range def.Fields {
			val, ok := o[f.Name]
			if !ok && f.DefaultValue != nil {
				val, _ = f.DefaultValue.Value(nil)
				ok = true
			}
			if !ok && !f.Type.NonNull {
				continue
			}
			item, err := c.input(f.Type, val)
			if err != nil {
				return nil, err
			}
			o[f.Name] = item
		}
		if def.Name == "PageInput" {
			n, ok := o["first"].(int)
			if !ok || n < 1 || n > 200 {
				return nil, errVariables
			}
		}
		return o, nil
	case ast.Enum:
		s, ok := value.(string)
		if !ok || def.EnumValues.ForName(s) == nil {
			return nil, errVariables
		}
		return s, nil
	}
	switch t.NamedType {
	case "Int":
		switch n := value.(type) {
		case int64:
			if n >= math.MinInt32 && n <= math.MaxInt32 {
				return int(n), nil
			}
		case float64:
			if n >= math.MinInt32 && n <= math.MaxInt32 && math.Trunc(n) == n {
				return int(n), nil
			}
		}
	case "Boolean":
		if _, ok := value.(bool); ok {
			return value, nil
		}
	default:
		s, ok := value.(string)
		if !ok || len(s) == 0 || len(s) > 8192 {
			return nil, errVariables
		}
		switch t.NamedType {
		case "Instant":
			if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
				return nil, errVariables
			}
		case "Revision":
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil || n < 0 || strconv.FormatInt(n, 10) != s {
				return nil, errVariables
			}
		}
		return s, nil
	}
	return nil, errVariables
}

// Apply only the fields in the reviewed operation. Repeated fragments are bounded
// by the embedded document, never by the remote peer.
func (op operation) project(data any) (any, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var normalized any
	if err = json.Unmarshal(raw, &normalized); err != nil {
		return nil, err
	}
	return completeValue(op.root.Definition.Type, op.root.SelectionSet, normalized)
}
func completeValue(t *ast.Type, set ast.SelectionSet, v any) (any, error) {
	if v == nil {
		if t.Elem != nil && t.NonNull {
			return []any{}, nil
		}
		if t.NonNull {
			return nil, fmt.Errorf("missing %s", t.String())
		}
		return nil, nil
	}
	if t.Elem != nil {
		list, ok := v.([]any)
		if !ok {
			return nil, errors.New("invalid result list")
		}
		for i, x := range list {
			y, err := completeValue(t.Elem, set, x)
			if err != nil {
				return nil, err
			}
			list[i] = y
		}
		return list, nil
	}
	if len(set) == 0 {
		return v, nil
	}
	m, ok := v.(object)
	if !ok {
		return nil, errors.New("invalid result object")
	}
	typ := str(m, "__typename")
	if typ == "" {
		typ = t.NamedType
	}
	dst := object{}
	if err := selectFields(dst, m, typ, set); err != nil {
		return nil, err
	}
	return dst, nil
}
func selectFields(dst, src object, typ string, set ast.SelectionSet) error {
	for _, node := range set {
		switch n := node.(type) {
		case *ast.Field:
			if n.Name == "__typename" {
				dst[n.Alias] = typ
				continue
			}
			val, err := completeValue(n.Definition.Type, n.SelectionSet, src[n.Name])
			if err != nil {
				return fmt.Errorf("%s.%s: %w", typ, n.Name, err)
			}
			dst[n.Alias] = val
		case *ast.FragmentSpread:
			if err := selectFields(dst, src, typ, n.Definition.SelectionSet); err != nil {
				return err
			}
		case *ast.InlineFragment:
			if n.TypeCondition == typ {
				if err := selectFields(dst, src, typ, n.SelectionSet); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func str(m object, k string) string { v, _ := m[k].(string); return v }
func obj(m object, k string) object { v, _ := m[k].(object); return v }
