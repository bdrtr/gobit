package service

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/textfold"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// orderSearch reads the order list's q (ADR 0456). A q with nothing in it is
// nil, the list unfiltered; one past textfold.MaxQueryLen is refused.
//
// A number is matched as an order's number only when it is one: "#1042" asks
// for that number alone, and "1042" for it and for the words holding it,
// because a q of digits is also a part of a phone or a company. The words
// are folded and split by the database (order_search_query), so this
// function only decides which of the two the q names.
func orderSearch(raw string) (*models.OrderSearch, error) {
	q, ok := textfold.Query(raw)
	if !ok {
		return nil, nil
	}
	if utf8.RuneCountInString(q) > textfold.MaxQueryLen {
		return nil, errors.Invalid(CodeInvalidInput,
			"q is at most %d characters", textfold.MaxQueryLen)
	}

	digits, hashed := strings.CutPrefix(q, "#")
	number, err := strconv.ParseInt(strings.TrimSpace(digits), 10, 64)
	isNumber := err == nil && number > 0

	search := &models.OrderSearch{}
	if isNumber {
		search.DisplayID = &number
	}
	if !hashed || !isNumber {
		search.Text = &q
	}

	return search, nil
}
