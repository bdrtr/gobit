package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// TestTheOrderSearchReadsANumberOnlyWhereItIsOne pins which of the two
// readings a q names (ADR 0456): "#1042" the number alone, "1042" the number
// and the words, anything else the words.
func TestTheOrderSearchReadsANumberOnlyWhereItIsOne(t *testing.T) {
	cases := map[string]struct {
		raw     string
		display int64
		text    string
	}{
		"hashed number":      {raw: " #1042 ", display: 1042},
		"bare number":        {raw: "1042", display: 1042, text: "1042"},
		"hash and a space":   {raw: "# 1042", display: 1042},
		"words":              {raw: "ayse yilmaz", text: "ayse yilmaz"},
		"hash before a word": {raw: "#abc", text: "#abc"},
		"zero is no number":  {raw: "#0", text: "#0"},
		"a phone":            {raw: "0532 111 22 33", text: "0532 111 22 33"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			search, err := orderSearch(tc.raw)
			require.NoError(t, err)
			require.NotNil(t, search)
			if tc.display == 0 {
				assert.Nil(t, search.DisplayID)
			} else {
				require.NotNil(t, search.DisplayID)
				assert.Equal(t, tc.display, *search.DisplayID)
			}
			if tc.text == "" {
				assert.Nil(t, search.Text)
			} else {
				require.NotNil(t, search.Text)
				assert.Equal(t, tc.text, *search.Text)
			}
		})
	}

	blank, err := orderSearch("   ")
	require.NoError(t, err)
	assert.Nil(t, blank, "a q with nothing in it filters nothing")

	_, err = orderSearch(strings.Repeat("x", 201))
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}
