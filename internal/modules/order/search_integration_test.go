//go:build integration

package order_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// This file proves the order list's q (ADR 0456) and payment_status
// (ADR 0457) against a real database. Every test lists one customer's orders,
// because the database is shared; that the customer narrows the hits is part
// of what is proved. The Turkish letters are \u escapes so that the file stays
// English (ADR 0012): U+0130 is the dotted capital I, U+00C7 the capital c
// with cedilla, U+0131 the dotless i.

func searchedOrders(t *testing.T, svc *service.Service, customer, q string) (ids []string, count int64) {
	t.Helper()

	page, err := svc.ListOrders(context.Background(), service.ListOrdersInput{
		CustomerID: &customer, Q: &q, Page: service.Page{Limit: 50},
	})
	require.NoError(t, err)

	ids = make([]string, 0, len(page.Items))
	for i := range page.Items {
		ids = append(ids, page.Items[i].ID)
	}

	return ids, page.Count
}

func searchAddress(kind models.AddressType, first, last, company, phone string) models.OrderAddress {
	return models.OrderAddress{
		Type: kind, FirstName: first, LastName: last, Company: company,
		Address1: "Street 1", City: "Istanbul", CountryCode: "TR", Phone: phone,
	}
}

func TestTheOrderListFindsANumberAnEmailANameOrAPhone(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	stamp := time.Now().UnixNano()
	customer := fmt.Sprintf("cus_search_%d", stamp)
	token := fmt.Sprintf("ord%d", stamp)
	phone := fmt.Sprintf("5%09d", stamp%1_000_000_000)

	in := validInput()
	in.CustomerID = customer
	in.Email = "buyer." + token + "@example.com"
	in.Addresses = []models.OrderAddress{
		searchAddress(models.AddressShipping, "\u0130smail", "\u00c7el\u0131k", "Acme",
			"+90 "+phone[:3]+" "+phone[3:6]+" "+phone[6:]),
		searchAddress(models.AddressBilling, "Billing", "Person", "Ledger Ltd", ""),
	}
	found, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	other, err := svc.CreateOrder(ctx, func() service.CreateOrderInput {
		o := validInput()
		o.CustomerID = customer
		o.Email = "someone.else@example.com"

		return o
	}())
	require.NoError(t, err)
	number := strconv.FormatInt(found.DisplayID, 10)

	cases := map[string]struct {
		q    string
		want []string
	}{
		"the number alone":             {q: "#" + number, want: []string{found.ID}},
		"a number is also words":       {q: number, want: []string{found.ID}},
		"an e-mail by a part":          {q: token, want: []string{found.ID}},
		"a name folded":                {q: "ismail celik", want: []string{found.ID}},
		"capitals and the dotless i":   {q: "\u0130SMA\u0130L \u00c7EL\u0131K", want: []string{found.ID}},
		"the billing address":          {q: "ledger pers", want: []string{found.ID}},
		"words split over addresses":   {q: "ismail ledger", want: nil},
		"words split over e-mail+name": {q: token + " ismail", want: nil},
		"a phone as typed":             {q: "0" + phone[:3] + " " + phone[3:6] + " " + phone[6:8] + " " + phone[8:], want: []string{found.ID}},
		"a phone's last digits":        {q: phone[4:], want: []string{found.ID}},
		"both e-mails by their domain": {q: "example", want: []string{found.ID, other.ID}},
		"signs alone find nothing":     {q: "@.-", want: nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ids, count := searchedOrders(t, svc, customer, tc.q)
			assert.ElementsMatch(t, tc.want, ids)
			assert.EqualValues(t, len(tc.want), count, "the count is the hits'")
		})
	}

	// A corrected shipping address is searched as it stands now.
	corrected := searchAddress(models.AddressShipping, "Zeynep", "Kaya", "Acme", "")
	_, err = svc.CorrectShippingAddress(ctx, found.ID, corrected)
	require.NoError(t, err)
	ids, _ := searchedOrders(t, svc, customer, "ismail")
	assert.Empty(t, ids, "an address a correction closed is not searched")
	ids, _ = searchedOrders(t, svc, customer, "zeynep kaya")
	assert.Equal(t, []string{found.ID}, ids)

	_, err = svc.ListOrders(ctx, service.ListOrdersInput{Q: func() *string { s := strings.Repeat("a", 201); return &s }()})
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "%v", err)
}

func TestAnErasedOrderIsNotFound(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	stamp := time.Now().UnixNano()
	customer := fmt.Sprintf("cus_search_erased_%d", stamp)
	email := fmt.Sprintf("erased%d@example.com", stamp)

	ord := placeOrderFor(t, svc, customer, email)
	settleOrder(t, svc, ord)
	ids, _ := searchedOrders(t, svc, customer, "ayse yilmaz")
	require.Equal(t, []string{ord.ID}, ids)

	_, err := svc.Erase(ctx, personaldata.Subject{CustomerID: customer, Email: email})
	require.NoError(t, err)

	for _, q := range []string{"ayse yilmaz", fmt.Sprintf("erased%d", stamp), "555 000 00 00"} {
		ids, _ = searchedOrders(t, svc, customer, q)
		assert.Empty(t, ids, "the erasure's UPDATE takes %q out of the index", q)
	}
}

// TestThePaymentStatusFilterOnTheRealQuery is ADR 0457's CASE against a real
// PostgreSQL, in the plain list and the searched one: the four statements
// carry it written out.
func TestThePaymentStatusFilterOnTheRealQuery(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	stamp := time.Now().UnixNano()
	customer := fmt.Sprintf("cus_paystatus_%d", stamp)
	token := fmt.Sprintf("pay%d", stamp)

	place := func() string {
		t.Helper()

		in := validInput()
		in.CustomerID = customer
		in.Email = token + "@example.com"
		order, err := svc.CreateOrder(ctx, in)
		require.NoError(t, err)

		return order.ID
	}
	collected := func(orderID string, paid, refunded int64) {
		t.Helper()

		_, err := svc.SetOrderSummaryTotals(ctx, orderID, service.SummaryTotalsInput{
			PaidTotal: paid, RefundedTotal: refunded,
		})
		require.NoError(t, err)
	}
	total := validInput().Total

	unpaid := place()
	partly := place()
	collected(partly, total/2, 0)
	paid := place()
	collected(paid, total, 0)
	credited := place()
	collected(credited, total-1, 0)
	_, err := svc.CreateCreditLine(ctx, credited, service.CreateCreditLineInput{Amount: 1, Reason: "goodwill"})
	require.NoError(t, err)
	partlyBack := place()
	collected(partlyBack, total, 1)
	back := place()
	collected(back, total, total)

	want := map[models.PaymentStatus][]string{
		models.PaymentNotPaid:           {unpaid},
		models.PaymentPartiallyCaptured: {partly},
		models.PaymentCaptured:          {paid, credited},
		models.PaymentPartiallyRefunded: {partlyBack},
		models.PaymentRefunded:          {back},
	}
	for status, ids := range want {
		for _, q := range []*string{nil, &token} {
			raw := string(status)
			page, err := svc.ListOrders(ctx, service.ListOrdersInput{
				CustomerID: &customer, PaymentStatus: &raw, Q: q, Page: service.Page{Limit: 50},
			})
			require.NoError(t, err)
			got := make([]string, 0, len(page.Items))
			for i := range page.Items {
				got = append(got, page.Items[i].ID)
			}
			assert.ElementsMatch(t, ids, got, "%s, searched %v", status, q != nil)
			assert.EqualValues(t, len(ids), page.Count, "%s: the count applies the same CASE", status)
		}
	}

	unknown := "authorized"
	_, err = svc.ListOrders(ctx, service.ListOrdersInput{PaymentStatus: &unknown})
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "%v", err)
}

// TestTheOrderSearchIsReadThroughItsIndexes fails when a statement's
// expression and its index's drift apart. The tables are filled inside a
// transaction that is rolled back, because on a handful of rows any plan is
// the cheapest.
func TestTheOrderSearchIsReadThroughItsIndexes(t *testing.T) {
	ctx := context.Background()

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `INSERT INTO orders (id, status, region_id, email, currency_code,
			subtotal, discount_total, tax_total, shipping_total, total, placed_at)
		SELECT 'order_plan' || g, 'pending', 'reg_plan', 'plan' || g || '@example.com', 'try',
			0, 0, 0, 0, 0, now()
		FROM generate_series(1, 3000) AS g`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO order_addresses (id, order_id, address_type, first_name,
			last_name, address_1, city, country_code, phone)
		SELECT 'oaddr_plan' || g, 'order_plan' || g, 'shipping', 'Plan', 'Row' || g, 'Street', 'City',
			'TR', '0555 000 ' || g
		FROM generate_series(1, 3000) AS g`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `ANALYZE orders; ANALYZE order_addresses`)
	require.NoError(t, err)

	explain := func(query string) string {
		rows, err := tx.Query(ctx, "EXPLAIN "+query)
		require.NoError(t, err)
		defer rows.Close()

		var plan strings.Builder
		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line))
			plan.WriteString(line + "\n")
		}
		require.NoError(t, rows.Err())

		return plan.String()
	}

	byEmail := explain(`SELECT o.id FROM orders o
		WHERE order_search_document(NULL, o.email) @@ order_search_query('kaya')`)
	assert.Contains(t, byEmail, "orders_search_idx", byEmail)
	byAddress := explain(`SELECT a.order_id FROM order_addresses a
		WHERE a.superseded_at IS NULL
		  AND order_search_document(a.phone, a.first_name, a.last_name, a.company)
		      @@ order_search_query('kaya')`)
	assert.Contains(t, byAddress, "order_addresses_search_idx", byAddress)
}
