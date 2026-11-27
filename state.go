package ga

import (
	"reflect"
	"time"
)

func cloneState(src map[string]any) map[string]any {
	if src == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	seen := make(map[uintptr]reflect.Value)
	for k, v := range src {
		out[k] = deepCopy(reflect.ValueOf(v), seen).Interface()
	}
	return out
}

var timeTypeRef = reflect.TypeOf(time.Time{})

func deepCopy(v reflect.Value, seen map[uintptr]reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(deepCopy(v.Elem(), seen))
		return out

	case reflect.Ptr:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		key := v.Pointer()
		if got, ok := seen[key]; ok {
			return got
		}
		out := reflect.New(v.Type().Elem())
		seen[key] = out
		out.Elem().Set(deepCopy(v.Elem(), seen))
		return out

	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		key := v.Pointer()
		if got, ok := seen[key]; ok {
			return got
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		seen[key] = out
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(deepCopy(iter.Key(), seen), deepCopy(iter.Value(), seen))
		}
		return out

	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		key := v.Pointer()
		if key != 0 {
			if got, ok := seen[key]; ok {
				return got
			}
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		if key != 0 {
			seen[key] = out
		}
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(deepCopy(v.Index(i), seen))
		}
		return out

	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(deepCopy(v.Index(i), seen))
		}
		return out

	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		if v.Type() == timeTypeRef {
			return out
		}
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			if !t.Field(i).IsExported() || !out.Field(i).CanSet() {
				continue
			}
			out.Field(i).Set(deepCopy(v.Field(i), seen))
		}
		return out

	default:
		return v
	}
}
