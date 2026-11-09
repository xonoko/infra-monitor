package api

import (
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// Lists are never null in responses: the spec says so (generated UI types rely on it) and
// emptySlices makes it true by replacing nil slices with empty ones before encoding.
func init() { huma.DefaultArrayNullable = false }

func emptySlices(_ huma.Context, _ string, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer && !rv.IsNil() {
		fillSlices(rv.Elem(), 0)
		return v, nil
	}
	if rv.Kind() == reflect.Struct || rv.Kind() == reflect.Slice {
		// Not addressable: work on a copy.
		cp := reflect.New(rv.Type()).Elem()
		cp.Set(rv)
		fillSlices(cp, 0)
		return cp.Interface(), nil
	}
	return v, nil
}

var rawBytes = reflect.TypeOf([]byte(nil))

func fillSlices(v reflect.Value, depth int) {
	if depth > 12 || !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			fillSlices(v.Elem(), depth+1)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fillSlices(v.Field(i), depth+1)
			}
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 || v.Type().ConvertibleTo(rawBytes) {
			return // []byte / json.RawMessage: left as is
		}
		if v.IsNil() {
			if v.CanSet() {
				v.Set(reflect.MakeSlice(v.Type(), 0, 0))
			}
			return
		}
		for i := 0; i < v.Len(); i++ {
			fillSlices(v.Index(i), depth+1)
		}
	default:
	}
}
