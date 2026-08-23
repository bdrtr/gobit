package container

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// typeMismatch kayıtlı değerin beklenen tipi karşılamadığını anlatan hatayı
// üretir. Mesaj kalitesi burada bilinçli olarak yüksek tutulur: ADR 0001'de
// modüller birbirini import etmediği için uyumsuzluk yalnızca çalışma
// zamanında, tam da burada görülür.
func typeMismatch(name string, value any, want reflect.Type) error {
	got := reflect.TypeOf(value)
	return errors.Invalid(codeTypeMismatch,
		"%q servisi %s tipinde kayıtlı; beklenen tip %s%s",
		name, typeName(got), typeName(want), mismatchDetail(got, want)).
		WithDetails(map[string]any{
			"servis":       name,
			"kayitli_tip":  typeName(got),
			"beklenen_tip": typeName(want),
		})
}

// mismatchDetail beklenen tip bir arayüzse uyumsuzluğun nedenini açıklar;
// açıklanamıyorsa boş dize döner.
func mismatchDetail(got, want reflect.Type) string {
	if got == nil || want == nil || want.Kind() != reflect.Interface {
		return ""
	}

	// Sık yapılan hata: metodlar işaretçi alıcılı, servis değer olarak kaydedilmiş.
	if got.Kind() != reflect.Pointer && reflect.PointerTo(got).Implements(want) {
		return fmt.Sprintf(" — %s metodları işaretçi alıcılıdır; servisi *%s olarak kaydedin",
			typeName(got), typeName(got))
	}

	missing := make([]string, 0, want.NumMethod())
	for i := range want.NumMethod() {
		m := want.Method(i)
		gm, ok := got.MethodByName(m.Name)
		if !ok {
			missing = append(missing, "eksik: "+m.Name+methodSig(m.Type, false))
			continue
		}
		if !sameSignature(gm.Type, m.Type) {
			missing = append(missing, fmt.Sprintf("uyumsuz: %s%s (kayıtlı: %s%s)",
				m.Name, methodSig(m.Type, false), m.Name, methodSig(gm.Type, true)))
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return " — " + strings.Join(missing, ", ")
}

// sameSignature somut tipin metodu ile arayüz metodunun imzasını karşılaştırır.
// Somut metodun ilk parametresi alıcıdır; karşılaştırmada atlanır.
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

// methodSig imzayı "(girdiler) (çıktılar)" biçiminde yazar. skipRecv true ise
// ilk parametre (alıcı) atlanır.
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

// typeName tipin okunabilir adını döner; nil tip "<nil>" olur.
func typeName(t reflect.Type) string {
	if t == nil {
		return "<nil>"
	}
	return t.String()
}
