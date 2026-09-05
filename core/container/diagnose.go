package container

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
)

// typeMismatch produces the error explaining that the registered value does not
// satisfy the expected type. The message quality is deliberately kept high
// here: because modules do not import each other under ADR 0001, a mismatch is
// only seen at runtime, exactly at this point.
func typeMismatch(name string, value any, want reflect.Type) error {
	got := reflect.TypeOf(value)
	return errors.Invalid(codeTypeMismatch,
		"the service %q is registered as type %s; the expected type is %s%s",
		name, typeName(got), typeName(want), mismatchDetail(got, want)).
		WithDetails(map[string]any{
			"service":         name,
			"registered_type": typeName(got),
			"expected_type":   typeName(want),
		})
}

// mismatchDetail explains the reason for the mismatch when the expected type is
// an interface; when it cannot be explained it returns an empty string.
func mismatchDetail(got, want reflect.Type) string {
	if got == nil || want == nil || want.Kind() != reflect.Interface {
		return ""
	}

	// A common mistake: the methods have pointer receivers but the service was
	// registered as a value.
	if got.Kind() != reflect.Pointer && reflect.PointerTo(got).Implements(want) {
		return fmt.Sprintf(" — %s's methods have pointer receivers; register the service as *%s",
			typeName(got), typeName(got))
	}

	missing := make([]string, 0, want.NumMethod())
	for i := range want.NumMethod() {
		m := want.Method(i)
		gm, ok := got.MethodByName(m.Name)
		if !ok {
			missing = append(missing, "missing: "+m.Name+methodSig(m.Type, false))
			continue
		}
		if !sameSignature(gm.Type, m.Type) {
			missing = append(missing, fmt.Sprintf("mismatched: %s%s (registered: %s%s)",
				m.Name, methodSig(m.Type, false), m.Name, methodSig(gm.Type, true)))
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return " — " + strings.Join(missing, ", ")
}

// sameSignature compares a concrete type's method with an interface method.
// The concrete method's first parameter is the receiver; it is skipped in the
// comparison.
func sameSignature(withRecv, iface reflect.Type) bool {
	if withRecv.NumIn()-1 != iface.NumIn() ||
		withRecv.NumOut() != iface.NumOut() ||
		withRecv.IsVariadic() != iface.IsVariadic() {
		return false
	}
	for i := range iface.NumIn() {
		if withRecv.In(i+1) != iface.In(i) {
			return false
		}
	}
	for i := range iface.NumOut() {
		if withRecv.Out(i) != iface.Out(i) {
			return false
		}
	}
	return true
}

// methodSig writes the signature in "(inputs) (outputs)" form. With skipRecv
// true the first parameter (the receiver) is skipped.
func methodSig(t reflect.Type, skipRecv bool) string {
	first := 0
	if skipRecv {
		first = 1
	}

	var b strings.Builder
	b.WriteByte('(')
	for i := first; i < t.NumIn(); i++ {
		if i > first {
			b.WriteString(", ")
		}
		if t.IsVariadic() && i == t.NumIn()-1 {
			b.WriteString("..." + typeName(t.In(i).Elem()))
			continue
		}
		b.WriteString(typeName(t.In(i)))
	}
	b.WriteByte(')')

	switch t.NumOut() {
	case 0:
		return b.String()
	case 1:
		b.WriteString(" " + typeName(t.Out(0)))
		return b.String()
	default:
		outs := make([]string, 0, t.NumOut())
		for i := range t.NumOut() {
			outs = append(outs, typeName(t.Out(i)))
		}
		b.WriteString(" (" + strings.Join(outs, ", ") + ")")
		return b.String()
	}
}

// typeName returns the type's readable name; a nil type becomes "<nil>".
func typeName(t reflect.Type) string {
	if t == nil {
		return "<nil>"
	}
	return t.String()
}
