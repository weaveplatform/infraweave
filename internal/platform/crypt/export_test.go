package crypt

// ValueOf wraps b as an opened value.
func ValueOf(b []byte) Value { return Value{b} }
