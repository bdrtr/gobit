//go:build integration

package customer_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// This file proves the customer list's q (ADR 0456) against a real database:
// what it finds, that the list's other narrowings, paging and count hold over
// it, that an erased customer leaves it, and that the search is read through
// its index.
//
// The Turkish letters are \u escapes so that this file stays English
// (ADR 0012): U+0130 is the dotted capital I, U+015E and U+015F the capital
// and small s with cedilla, U+0131 the dotless i.

// searchToken is a word no other test writes, so that a search is answered by
// this test's rows alone on the shared database.
func searchToken(t *testing.T) string {
	t.Helper()

	return fmt.Sprintf("srch%d", time.Now().UnixNano())
}

// searchPhone is a mobile number no other test writes, as its ten national
// digits.
func searchPhone() string {
	return fmt.Sprintf("5%09d", time.Now().UnixNano()%1_000_000_000)
}

func searchIDs(items []models.Customer) []string {
	ids := make([]string, 0, len(items))
	for i := range items {
		ids = append(ids, items[i].ID)
	}

	return ids
}

func ptr[T any](v T) *T { return &v }

func TestTheCustomerListFindsAPartOfANameAnEmailOrAPhone(t *testing.T) {
	ctx := context.Background()
	svc := service.New(repository.New(testPool.Pool()), service.Options{})
	token := searchToken(t)
	phone := searchPhone()

	named, err := svc.CreateCustomer(ctx, service.CustomerInput{
		Email:     "ayse." + token + "@example.com",
		FirstName: "\u015eule",
		LastName:  "\u0130\u015eIK",
		Phone:     "+90 " + phone[:3] + " " + phone[3:6] + " " + phone[6:],
	})
	require.NoError(t, err)

	cases := map[string]struct {
		q    string
		want bool
	}{
		"folded name":              {q: "sule isik " + token, want: true},
		"dotless and capitals":     {q: "\u015eULE \u0131\u015f\u0131k " + token, want: true},
		"start of a word":          {q: "is " + token[:len(token)-2], want: true},
		"an e-mail splits":         {q: "example " + token, want: true},
		"a word not there":         {q: "isik " + token + " nowhere", want: false},
		"inside a word is no hit":  {q: "sik " + token, want: false},
		"phone as typed":           {q: "0" + phone[:3] + " " + phone[3:6] + " " + phone[6:8] + " " + phone[8:], want: true},
		"phone with country code":  {q: "+90" + phone, want: true},
		"phone's last digits":      {q: phone[3:], want: true},
		"phone digits out of turn": {q: phone[3:] + phone[:3], want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			listed, err := svc.ListCustomers(ctx, service.ListCustomersInput{Q: ptr(tc.q), Limit: 100})
			require.NoError(t, err)
			if tc.want {
				assert.Contains(t, searchIDs(listed.Items), named.ID)
			} else {
				assert.NotContains(t, searchIDs(listed.Items), named.ID)
			}
		})
	}
}

// TestASearchKeepsTheListsNarrowingsPagingAndCount pages three hits two at a
// time and narrows them by has_account, so a search that dropped a narrowing
// or counted the page rather than the hits fails here.
func TestASearchKeepsTheListsNarrowingsPagingAndCount(t *testing.T) {
	ctx := context.Background()
	svc := service.New(repository.New(testPool.Pool()), service.Options{})
	token := searchToken(t)

	var accounts []string
	for i := range 3 {
		created, err := svc.CreateCustomer(ctx, service.CustomerInput{
			Email: fmt.Sprintf("walk%d.%s@example.com", i, token), LastName: "Kaya",
		})
		require.NoError(t, err)
		accounts = append(accounts, created.ID)
	}

	first, err := svc.ListCustomers(ctx, service.ListCustomersInput{Q: ptr(token + " kaya"), Limit: 2})
	require.NoError(t, err)
	assert.EqualValues(t, 3, first.Count, "the count is the hits', not the page's")
	require.Len(t, first.Items, 2)
	require.NotEmpty(t, first.NextCursor)

	after, err := page.Decode(service.CustomerListing, first.NextCursor)
	require.NoError(t, err)
	second, err := svc.ListCustomers(ctx, service.ListCustomersInput{Q: ptr(token), Limit: 2, After: after})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	assert.ElementsMatch(t, accounts, append(searchIDs(first.Items), searchIDs(second.Items)...))
	assert.Empty(t, second.NextCursor)

	guests, err := svc.ListCustomers(ctx, service.ListCustomersInput{Q: ptr(token), HasAccount: ptr(false)})
	require.NoError(t, err)
	assert.Zero(t, guests.Count, "has_account narrows the hits")

	blank, err := svc.ListCustomers(ctx, service.ListCustomersInput{Q: ptr("   "), Limit: 1})
	require.NoError(t, err)
	all, err := svc.ListCustomers(ctx, service.ListCustomersInput{Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, all.Count, blank.Count, "a q with nothing in it filters nothing")

	_, err = svc.ListCustomers(ctx, service.ListCustomersInput{Q: ptr(strings.Repeat("a", 201))})
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "%v", err)
}

func TestAnErasedCustomerIsNotFound(t *testing.T) {
	ctx := context.Background()
	svc := service.New(repository.New(testPool.Pool()), service.Options{})
	token := searchToken(t)

	created, err := svc.CreateCustomer(ctx, service.CustomerInput{
		Email: token + "@example.com", FirstName: "Erased", Phone: "+90 " + searchPhone(),
	})
	require.NoError(t, err)

	found, err := svc.ListCustomers(ctx, service.ListCustomersInput{Q: ptr(token)})
	require.NoError(t, err)
	require.Equal(t, []string{created.ID}, searchIDs(found.Items))

	_, err = svc.Erase(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)

	gone, err := svc.ListCustomers(ctx, service.ListCustomersInput{Q: ptr(token)})
	require.NoError(t, err)
	assert.Zero(t, gone.Count, "the erasure's UPDATE takes the customer out of the index")
}

// TestTheCustomerSearchIsReadThroughItsIndex fails when the statement's
// expression and the index's drift apart: the planner then has nothing but a
// scan that computes the document for every row. The table is filled inside a
// transaction that is rolled back, because on a handful of rows any plan is
// the cheapest.
func TestTheCustomerSearchIsReadThroughItsIndex(t *testing.T) {
	ctx := context.Background()

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `INSERT INTO customer (id, email, first_name, last_name, phone, has_account)
		SELECT 'cus_plan' || g, 'plan' || g || '@example.com', 'Plan', 'Row' || g, '', false
		FROM generate_series(1, 3000) AS g`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `ANALYZE customer`)
	require.NoError(t, err)

	rows, err := tx.Query(ctx, `EXPLAIN SELECT count(*) FROM customer c
		WHERE c.deleted_at IS NULL
		  AND customer_search_document(c.phone, c.first_name, c.last_name, c.email)
		      @@ customer_search_query('kaya')`)
	require.NoError(t, err)

	var plan strings.Builder
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan.WriteString(line + "\n")
	}
	rows.Close()
	require.NoError(t, rows.Err())
	assert.Contains(t, plan.String(), "customer_search_idx", plan.String())
}
