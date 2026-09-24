// Copyright (c) 2026 qyiun666
// SPDX-License-Identifier: MIT OR Apache-2.0

// A read hands the host a value, and a host is entitled to treat it as its own: sort the
// listing, blank a field before rendering, keep two reads and compare them later. None of
// that is safe if the value reached back into memory the library still uses — a decoded
// record is fresh, but a cache, a mirror, or a slice the门面 forgot to re-wrap is not, and the
// damage shows up far away from the mutation, on the next read someone else makes.
//
// This walks the same reads the ordering gate walks, against the same populated file: take two
// copies, rewrite every leaf of the first through a fresh addressable copy of it (so the walk
// reaches shared backing arrays and maps even when the read returned a struct by value), and
// require the second copy to answer exactly as it did before.

package api

import (
	"reflect"
	"testing"
)

func TestSurfaceReadsHandBackNoLibraryMemory(t *testing.T) {
	_, _, taken := surfaceReadFixture(t)

	for name, read := range taken {
		first, second := read(), read()
		before := encode(t, name, second)

		addressable := reflect.New(reflect.ValueOf(first).Type()).Elem()
		addressable.Set(reflect.ValueOf(first))
		mutateLeaves(addressable)

		if encode(t, name, second) != before {
			t.Errorf("%s: a read handed back memory the library still uses — rewriting the value one host got "+
				"changed what the next read answers\n before: %s\n after:  %s",
				name, before, encode(t, name, second))
		}
		// The walk has to have reached something, or the check above is comparing two
		// untouched copies and proves nothing.
		if encode(t, name, addressable.Interface()) == before {
			t.Errorf("%s: the mutation reached no leaf of this read, so the check is vacuous: %s", name, before)
		}
	}
}

// mutateLeaves rewrites every leaf reflect can reach: strings, numbers, booleans, and map
// entries (whose values cannot be addressed in place, so they are replaced). It does not
// grow or shrink a container — a length change would be a different question, since a host
// cannot corrupt the library by appending to a slice it was handed unless that slice is
// shared, which is exactly the element-writing this detects.
func mutateLeaves(v reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		if v.CanSet() {
			v.SetString("mutated")
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v.CanSet() {
			v.SetInt(-7)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v.CanSet() {
			v.SetUint(7)
		}
	case reflect.Float32, reflect.Float64:
		if v.CanSet() {
			v.SetFloat(-7.5)
		}
	case reflect.Bool:
		if v.CanSet() {
			v.SetBool(!v.Bool())
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			mutateLeaves(v.Elem())
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			mutateLeaves(v.Index(i))
		}
	case reflect.Map:
		clone := reflect.MakeMapWithSize(v.Type(), v.Len())
		for _, key := range v.MapKeys() {
			value := v.MapIndex(key)
			if value.Kind() == reflect.String {
				clone.SetMapIndex(key, reflect.ValueOf("mutated"))
				continue
			}
			replacement := reflect.New(value.Type()).Elem()
			replacement.Set(value)
			mutateLeaves(replacement)
			clone.SetMapIndex(key, replacement)
		}
		if v.CanSet() {
			v.Set(clone)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				mutateLeaves(v.Field(i))
			}
		}
	}
}
