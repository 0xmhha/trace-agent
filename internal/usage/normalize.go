package usage

import "reflect"

// NonNil replaces every nil slice reachable from the pointed-to value with an
// empty one, so that responses encode empty lists as [] rather than null. It
// walks structs, pointers to structs and slices of them; maps are left as
// they are. v must be a non-nil pointer.
func NonNil(v any) {
	root := reflect.ValueOf(v)
	if root.Kind() != reflect.Pointer || root.IsNil() {
		return
	}
	nonNil(root.Elem())
}

func nonNil(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			nonNil(v.Elem())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if field := v.Field(i); field.CanSet() {
				nonNil(field)
			}
		}
	case reflect.Slice:
		if v.IsNil() {
			v.Set(reflect.MakeSlice(v.Type(), 0, 0))
			return
		}
		if elem := v.Type().Elem().Kind(); elem != reflect.Struct && elem != reflect.Pointer && elem != reflect.Slice {
			return
		}
		for i := range v.Len() {
			nonNil(v.Index(i))
		}
	}
}
