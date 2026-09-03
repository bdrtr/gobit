package eventbus

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// crockfordAlphabet is the encoding alphabet newEventID uses.
const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// quietLogger returns a logger whose output is discarded, for the in-package
// tests.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewEventIDIsUniqueWithinSameMillisecond(t *testing.T) {
	// The timestamp is held fixed: only the random part can provide
	// uniqueness. If the randomness weakens (fewer bytes filled in, say), a
	// collision shows up here.
	const count = 100_000
	when := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)

	seen := make(map[string]struct{}, count)
	for range count {
		id := newEventID(when)
		if _, dup := seen[id]; dup {
			t.Fatalf("a repeated id was generated within the same millisecond: %q (after %d ids)",
				id, len(seen))
		}
		seen[id] = struct{}{}
	}
}

func TestNewEventIDHasFixedLengthAndCrockfordAlphabet(t *testing.T) {
	// 16 bytes encode into exactly 26 characters with padless Base32.
	const wantLen = len(idPrefix) + 26

	for range 1_000 {
		id := newEventID(time.Now())
		if len(id) != wantLen {
			t.Fatalf("id length = %d (%q), expected %d", len(id), id, wantLen)
		}
		body, ok := strings.CutPrefix(id, idPrefix)
		if !ok {
			t.Fatalf("id %q does not start with the %q prefix", id, idPrefix)
		}
		for _, r := range body {
			if !strings.ContainsRune(crockfordAlphabet, r) {
				t.Fatalf("id %q holds the character %q, which is outside the Crockford alphabet", id, r)
			}
		}
	}
}

func TestNewEventIDSortsLexicographicallyByTime(t *testing.T) {
	// The lexicographic order must be the same as the time order; that is what
	// lets the ids be used like a sortable primary key in a database.
	base := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)

	prev := newEventID(base)
	for i := 1; i <= 1_000; i++ {
		when := base.Add(time.Duration(i) * time.Millisecond)
		id := newEventID(when)
		if id <= prev {
			t.Fatalf("the id %q generated for %v does not come after the previous one (%q)",
				id, when, prev)
		}
		prev = id
	}
}

func TestNewEventIDClampsTimesBefore1970(t *testing.T) {
	// A timestamp before 1970 gives negative milliseconds; without clamping to
	// the floor the encoded byte sequence overflows and the ordering breaks.
	old := newEventID(time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC))
	epoch := newEventID(time.Unix(0, 0))
	later := newEventID(time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC))

	if len(old) != len(epoch) {
		t.Fatalf("the length of a pre-1970 id = %d, expected %d", len(old), len(epoch))
	}
	if old >= later {
		t.Errorf("the pre-1970 id %q must come before the 2026 id (%q)", old, later)
	}
}

func TestNormalizeFillsIDAndTime(t *testing.T) {
	before := time.Now().UTC()

	e, err := normalize(Event{Name: "order.placed"})
	if err != nil {
		t.Fatalf("normalize returned an error: %v", err)
	}
	if !strings.HasPrefix(e.ID, idPrefix) {
		t.Errorf("ID = %q, expected a generated id with the %q prefix", e.ID, idPrefix)
	}
	if e.OccurredAt.Before(before) {
		t.Errorf("OccurredAt = %v, it cannot be before the moment of the call (%v)", e.OccurredAt, before)
	}
	if e.OccurredAt.Location() != time.UTC {
		t.Errorf("the location of OccurredAt = %v, expected UTC", e.OccurredAt.Location())
	}
}
