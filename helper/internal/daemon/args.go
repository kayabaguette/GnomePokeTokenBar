package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// request is one parsed command line (protocol §4).
type request struct {
	ID   int64
	Cmd  string // "" when cmd is missing or not a string
	Args json.RawMessage
}

const maxSafeInt = 1<<53 - 1

// parseRequest validates the envelope (§11.1). ok is false for a line that gets no reply: not
// JSON, not an object, or without a positive integer id. why says what was wrong.
func parseRequest(line []byte) (req request, ok bool, why string) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil || m == nil {
		return request{}, false, "not a JSON object"
	}
	rawID, has := m["id"]
	if !has {
		return request{}, false, "no id"
	}
	id, isInt := positiveInt(rawID)
	if !isInt {
		return request{}, false, "id is not a positive integer"
	}
	req.ID = id
	if raw, has := m["cmd"]; has {
		var cmd string
		if json.Unmarshal(raw, &cmd) == nil {
			req.Cmd = cmd
		}
	}
	req.Args = m["args"]
	return req, true, ""
}

// positiveInt accepts only an integer literal in 1…2^53−1: no sign, fraction, exponent or quotes.
func positiveInt(raw json.RawMessage) (int64, bool) {
	s := string(bytes.TrimSpace(raw))
	if s == "" || s[0] < '1' || s[0] > '9' {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v > maxSafeInt {
		return 0, false
	}
	return v, true
}

// args reads typed command arguments. The first error sticks; later reads return defaults.
// Unknown args are ignored (§2.7).
type args struct {
	m   map[string]json.RawMessage
	err error
}

// errBadArgs marks a badArgs failure; its message names the field.
type errBadArgs struct{ msg string }

func (e errBadArgs) Error() string { return e.msg }

func badArgs(format string, a ...any) error { return errBadArgs{fmt.Sprintf(format, a...)} }

func newArgs(raw json.RawMessage) (*args, error) {
	a := &args{m: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return a, nil
	}
	if err := json.Unmarshal(raw, &a.m); err != nil || a.m == nil {
		return nil, badArgs("args must be an object")
	}
	return a, nil
}

func (a *args) fail(err error) {
	if a.err == nil {
		a.err = err
	}
}

// get returns the raw value; present is false when the key is absent. A JSON null counts as present.
func (a *args) get(name string, required bool) (json.RawMessage, bool) {
	raw, ok := a.m[name]
	if !ok {
		if required {
			a.fail(badArgs("%s is required", name))
		}
		return nil, false
	}
	return raw, true
}

func isNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }

// intLiteral parses an integer literal, rejecting fractions, exponents and strings.
func intLiteral(raw json.RawMessage) (int64, bool) {
	s := string(bytes.TrimSpace(raw))
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil
}

func (a *args) int(name string, required bool, def, lo, hi int64) int64 {
	raw, ok := a.get(name, required)
	if !ok || a.err != nil {
		return def
	}
	v, isInt := intLiteral(raw)
	if !isInt {
		a.fail(badArgs("%s must be an integer", name))
		return def
	}
	if v < lo || v > hi {
		a.fail(badArgs("%s must be %d..%d", name, lo, hi))
		return def
	}
	return v
}

// nullableInt reads an int or null; a missing key gives nil unless required.
func (a *args) nullableInt(name string, required bool, lo, hi int64) *int {
	raw, ok := a.get(name, required)
	if !ok || a.err != nil || isNull(raw) {
		return nil
	}
	v := int(a.int(name, true, 0, lo, hi))
	if a.err != nil {
		return nil
	}
	return &v
}

func (a *args) number(name string, required bool, def float64) float64 {
	raw, ok := a.get(name, required)
	if !ok || a.err != nil {
		return def
	}
	var v float64
	if isNull(raw) || json.Unmarshal(raw, &v) != nil {
		a.fail(badArgs("%s must be a number", name))
		return def
	}
	return v
}

func (a *args) boolean(name string, required bool, def bool) bool {
	raw, ok := a.get(name, required)
	if !ok || a.err != nil {
		return def
	}
	var v bool
	if isNull(raw) || json.Unmarshal(raw, &v) != nil {
		a.fail(badArgs("%s must be a boolean", name))
		return def
	}
	return v
}

func (a *args) str(name string, required bool, def string) string {
	raw, ok := a.get(name, required)
	if !ok || a.err != nil {
		return def
	}
	var v string
	if isNull(raw) || json.Unmarshal(raw, &v) != nil {
		a.fail(badArgs("%s must be a string", name))
		return def
	}
	return v
}

// nullableStr reads a string or null; a missing key gives nil.
func (a *args) nullableStr(name string) *string {
	raw, ok := a.get(name, false)
	if !ok || a.err != nil || isNull(raw) {
		return nil
	}
	v := a.str(name, true, "")
	if a.err != nil {
		return nil
	}
	return &v
}

// enum reads a string that must be one of allowed; a missing key gives def.
func (a *args) enum(name, def string, allowed ...string) string {
	v := a.str(name, false, def)
	if a.err != nil {
		return def
	}
	for _, x := range allowed {
		if v == x {
			return v
		}
	}
	a.fail(badArgs("%s must be one of %v", name, allowed))
	return def
}

// nullableEnum reads one of allowed or null.
func (a *args) nullableEnum(name string, allowed ...string) *string {
	v := a.nullableStr(name)
	if v == nil {
		return nil
	}
	for _, x := range allowed {
		if *v == x {
			return v
		}
	}
	a.fail(badArgs("%s must be null or one of %v", name, allowed))
	return nil
}

func isBadArgs(err error) bool {
	var e errBadArgs
	return errors.As(err, &e)
}
