package eventbus

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// crockfordAlphabet newEventID'nin kullandığı kodlama alfabesidir.
const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// quietLogger paket içi testlerde çıktısı atılan bir logger döner.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewEventIDIsUniqueWithinSameMillisecond(t *testing.T) {
	// Zaman damgası sabit tutulur: tekilliği yalnızca rastgele bölüm sağlayabilir.
	// Rastgelelik zayıflarsa (örn. daha az bayt doldurulursa) burada çakışma çıkar.
	const count = 100_000
	when := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)

	seen := make(map[string]struct{}, count)
	for range count {
		id := newEventID(when)
		if _, dup := seen[id]; dup {
			t.Fatalf("aynı milisaniyede tekrarlanan kimlik üretildi: %q (%d kimlik sonra)",
				id, len(seen))
		}
		seen[id] = struct{}{}
	}
}

func TestNewEventIDHasFixedLengthAndCrockfordAlphabet(t *testing.T) {
	// 16 bayt, dolgusuz Base32 ile tam 26 karaktere kodlanır.
	const wantLen = len(idPrefix) + 26

	for range 1_000 {
		id := newEventID(time.Now())
		if len(id) != wantLen {
			t.Fatalf("kimlik uzunluğu = %d (%q), beklenen %d", len(id), id, wantLen)
		}
		body, ok := strings.CutPrefix(id, idPrefix)
		if !ok {
			t.Fatalf("kimlik %q, %q önekiyle başlamıyor", id, idPrefix)
		}
		for _, r := range body {
			if !strings.ContainsRune(crockfordAlphabet, r) {
				t.Fatalf("kimlik %q, Crockford alfabesi dışında %q karakteri içeriyor", id, r)
			}
		}
	}
}

func TestNewEventIDSortsLexicographicallyByTime(t *testing.T) {
	// Sözlüksel sıra zaman sırasıyla aynı olmalı; kimlikler bu sayede
	// veritabanında sıralanabilir birincil anahtar gibi kullanılabilir.
	base := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)

	prev := newEventID(base)
	for i := 1; i <= 1_000; i++ {
		when := base.Add(time.Duration(i) * time.Millisecond)
		id := newEventID(when)
		if id <= prev {
			t.Fatalf("%v için üretilen kimlik %q, bir önceki (%q) kimlikten sonra gelmiyor",
				when, id, prev)
		}
		prev = id
	}
}

func TestNewEventIDClampsTimesBefore1970(t *testing.T) {
	// 1970 öncesi zaman damgası negatif milisaniye verir; tabana çekilmezse
	// kodlanan bayt dizisi taşar ve sıralama bozulur.
	old := newEventID(time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC))
	epoch := newEventID(time.Unix(0, 0))
	later := newEventID(time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC))

	if len(old) != len(epoch) {
		t.Fatalf("1970 öncesi kimlik uzunluğu = %d, beklenen %d", len(old), len(epoch))
	}
	if old >= later {
		t.Errorf("1970 öncesi kimlik %q, 2026 kimliğinden (%q) önce gelmeli", old, later)
	}
}

func TestNormalizeFillsIDAndTime(t *testing.T) {
	before := time.Now().UTC()

	e, err := normalize(Event{Name: "order.placed"})
	if err != nil {
		t.Fatalf("normalize hata verdi: %v", err)
	}
	if !strings.HasPrefix(e.ID, idPrefix) {
		t.Errorf("ID = %q, beklenen %q önekli üretilmiş kimlik", e.ID, idPrefix)
	}
	if e.OccurredAt.Before(before) {
		t.Errorf("OccurredAt = %v, çağrı anından (%v) önce olamaz", e.OccurredAt, before)
	}
	if e.OccurredAt.Location() != time.UTC {
		t.Errorf("OccurredAt konumu = %v, beklenen UTC", e.OccurredAt.Location())
	}
}
