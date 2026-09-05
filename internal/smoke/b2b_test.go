//go:build smoke

package smoke

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/internal/modules/b2b"
)

// b2bSpendingLimit is the initial spending limit the scenario gives the
// employee (minor unit). The value itself does not matter; what matters is that
// the SAME number can be read back from the storefront.
const b2bSpendingLimit int64 = 500_000

// b2bNewLimit is the limit written by the update made through the admin
// endpoint.
//
// Being DIFFERENT from the first one is a precondition of the scenario: had the
// same number been written, there would be no way to tell whether the
// storefront read the updated record or the old one.
const b2bNewLimit int64 = 250_000

// b2bStoreEmployee holds the fields of the storefront's employee record that
// the scenario reads.
//
// The module's DTO type is NOT imported; the rationale is in the [zarfVerisi]
// doc comment.
type b2bStoreEmployee struct {
	ID                       string     `json:"id"`
	CompanyID                string     `json:"company_id"`
	CustomerID               string     `json:"customer_id"`
	SpendingLimit            *int64     `json:"spending_limit"`
	SpendingLimitResetPeriod string     `json:"spending_limit_reset_period"`
	SpendingWindowStart      *time.Time `json:"spending_window_start"`
	IsCompanyAdmin           bool       `json:"is_company_admin"`
}

// b2bOpenCustomer opens a customer through the admin endpoint and returns its
// id.
//
// The customer is NOT b2b's record but the customer module's, and the employee
// bond can only be formed with a real "cust_" id (see b2b service requireID).
// Had a made-up id been used, the scenario would never have exercised the link
// layer that ties the two modules together.
func b2bOpenCustomer(t *testing.T, s *proc, token, email string) string {
	t.Helper()

	code, body := s.adminRequest(http.MethodPost, "/admin/v1/customers", token,
		map[string]any{"email": email, "first_name": "Smoke", "last_name": "B2B"})
	require.Equal(t, http.StatusCreated, code, "could not open the customer; body: %s", body)

	customer := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, body)
	require.NotEmpty(t, customer.ID, "the customer must return an id; body: %s", body)

	return customer.ID
}

// b2bOpenCompany opens a company through the admin endpoint and returns its id.
func b2bOpenCompany(t *testing.T, s *proc, token, name, email, period string) string {
	t.Helper()

	code, body := s.adminRequest(http.MethodPost, "/admin/v1/b2b/companies", token, map[string]any{
		"name":                        name,
		"email":                       email,
		"currency_code":               "TRY",
		"spending_limit_reset_period": period,
	})
	require.Equal(t, http.StatusCreated, code, "could not open the company; body: %s", body)

	company := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, body)
	require.NotEmpty(t, company.ID, "the company must return an id; body: %s", body)

	return company.ID
}

// b2bReadStorefrontEmployee reads the customer's OWN employee record from the
// storefront endpoint.
func b2bReadStorefrontEmployee(t *testing.T, s *proc, key, customerID string) b2bStoreEmployee {
	t.Helper()

	code, body := s.storefrontRequest(http.MethodGet,
		"/store/v1/b2b/customers/"+customerID+"/employee", key, nil)
	require.Equal(t, http.StatusOK, code,
		"could not read the storefront employee record; body: %s", body)

	return zarfVerisi[b2bStoreEmployee](t, body)
}

// b2bVerifySchema verifies that the b2b tables are created on a cold start.
//
// The query looks at the tables THEMSELVES, not at an endpoint: even if a
// module's migration forgot to be wired into startup, its endpoints could still
// be mounted and the first request would die with "relation does not exist" —
// that is, a test looking through the read surface would only see the fault
// once that endpoint was hit, and then with an incomprehensible error.
//
// The version ledger is inspected as well, because the EXISTENCE of the table
// is not enough on its own: a half-finished migration can also leave a table
// behind. The "dirty" flag is the only visible trace of that state.
func b2bVerifySchema(t *testing.T, dsn string) {
	t.Helper()

	pool, err := db.New(t.Context(), db.DefaultConfig(dsn), nil)
	require.NoError(t, err, "could not connect to the scenario database")
	defer pool.Close()

	ledger, err := db.MigrationsTable(b2b.ModuleName)
	require.NoError(t, err, "could not derive the name of the b2b version table")

	for _, table := range []string{"b2b_company", "b2b_company_employee", ledger} {
		var exists bool
		require.NoError(t,
			pool.Pool().QueryRow(t.Context(), "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists),
			"could not query the %s table", table)
		assert.True(t, exists,
			"a cold start must create the %q table: b2b is a NEW module and if its "+
				"migration is not wired into startup the table is never created", table)
	}

	var (
		version int64
		dirty   bool
	)
	require.NoError(t,
		pool.Pool().QueryRow(t.Context(), "SELECT version, dirty FROM "+ledger).Scan(&version, &dirty),
		"could not read the b2b version ledger")

	assert.Positive(t, version, "the b2b migration must have been applied")
	assert.False(t, dirty, "the b2b migration must not be left half-finished (dirty)")
}

// TestB2BEndToEndInARealProcess is scenario F: the B2B module runs on the real
// binary, with the real startup sequence.
//
// # Why a real process
//
// b2b was added in Phase 10 and has never run ON THE REAL BINARY. internal/e2e
// proves the module's flow but builds the services ITSELF: it skips the
// registry.Add line in the composition root, the migration order at startup,
// the guard stack and the real network. internal/app's own doc comment states the
// price of that gap: "a module NOT ADDED here does not EXIST in any
// installation" — and b2b's spending limit disappeared in exactly this way once.
//
// The scenario opens ONE process and drives this chain: cold start → migration
// → admin credentials → sales channel → publishable key → customer → company →
// employee → reading back from the storefront.
//
// # This scenario tests the limit's RULE, not its ENFORCEMENT
//
// The rule side of the limit (how large the limit is, in which window, in which
// currency) is proven here by reading it back from the storefront. Enforcing
// the limit, on the other hand, happens while the order is being opened (order
// service CreateOrder), and the only way there is the path that turns a cart
// into an order.
//
// This doc comment once said "that path does NOT exist in the running binary",
// and it was right: cmd/server was leaving only the saga ENGINE in the
// container, and the only place calling the constructor of the cart and
// checkout flows was internal/e2e. That fault has been closed since — the flows
// are built in the composition root, and the fact that the storefront path is
// OPEN in a real process was pinned down in this package
// (see [TestStorefrontFromCartToOrderInARealProcess]).
//
// So the limit can now be triggered in this process too; that it is not
// triggered is not an impossibility but a deliberate SCOPE decision: moving the
// scenario there would require setting up the whole catalog fixture (product,
// price, stock, location) a second time and opening a second server process.
// That the rule IS ENFORCED is proven with the same steps in e2e
// (internal/e2e/b2b_test.go): a purchase exceeding the limit does not become an
// order, no money is captured, stock stays untouched. That the rejection
// reaches all the way INTO THE BODY — that is, that the storefront can tell
// "your limit was not enough" apart from "try again" — is tested there
// separately from the HTTP endpoints (TestStorefrontB2BLimitRejectionReportsReason).
func TestB2BEndToEndInARealProcess(t *testing.T) {
	dsn := scenarioDatabase(t)

	cfg := baseSettings(dsn, freePort(t))
	cfg["ADMIN_BOOTSTRAP_EMAIL"] = seedEmail
	cfg["ADMIN_BOOTSTRAP_PASSWORD"] = seedPassword

	s := startServer(t, cfg)
	s.waitForReady(startupTimeout)

	t.Run("cold start creates the b2b schema", func(t *testing.T) {
		b2bVerifySchema(t, dsn)
	})

	token, _, storefrontKey := setUpAdminHarness(t, s, "Smoke B2B Channel")

	customerID := b2bOpenCustomer(t, s, token, "smoke-b2b@example.test")
	companyID := b2bOpenCompany(t, s, token,
		"Smoke B2B Inc.", "smoke-b2b-company@example.test", "monthly")

	limit := b2bSpendingLimit
	code, body := s.adminRequest(http.MethodPost, "/admin/v1/b2b/employees", token, map[string]any{
		"company_id":       companyID,
		"customer_id":      customerID,
		"spending_limit":   limit,
		"is_company_admin": true,
	})
	require.Equal(t, http.StatusCreated, code, "could not add the employee; body: %s", body)

	employeeID := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, body).ID
	require.NotEmpty(t, employeeID, "the employee must return an id; body: %s", body)

	t.Run("storefront returns the customer's own company", func(t *testing.T) {
		code, body := s.storefrontRequest(http.MethodGet,
			"/store/v1/b2b/customers/"+customerID+"/company", storefrontKey, nil)
		require.Equal(t, http.StatusOK, code,
			"the storefront company endpoint must return 200; body: %s", body)

		company := zarfVerisi[struct {
			ID                       string `json:"id"`
			Name                     string `json:"name"`
			CurrencyCode             string `json:"currency_code"`
			SpendingLimitResetPeriod string `json:"spending_limit_reset_period"`
		}](t, body)

		assert.Equal(t, companyID, company.ID,
			"the storefront must return the company RESOLVED from the employee record; body: %s", body)
		assert.Equal(t, "TRY", company.CurrencyCode,
			"the currency code must be stored normalized; body: %s", body)
		assert.Equal(t, "monthly", company.SpendingLimitResetPeriod,
			"the reset period must be read back as it was written from the admin side; body: %s", body)
	})

	t.Run("storefront reads the spending rule back", func(t *testing.T) {
		employee := b2bReadStorefrontEmployee(t, s, storefrontKey, customerID)

		assert.Equal(t, employeeID, employee.ID, "the storefront must return the same employee record")
		assert.Equal(t, customerID, employee.CustomerID,
			"the customer id must be filled in by the link layer; seeing it empty means "+
				"the bond was never formed")
		require.NotNil(t, employee.SpendingLimit, "the spending limit must be set")
		assert.Equal(t, b2bSpendingLimit, *employee.SpendingLimit,
			"the storefront must return exactly the SAME limit written from the admin side")
		assert.Equal(t, "monthly", employee.SpendingLimitResetPeriod,
			"the period is derived from the COMPANY, not from the employee's own record")

		// The window comes from the CALENDAR (see
		// models.SpendingResetPeriod.WindowStart): a monthly limit starts on the
		// 1st of the month, at midnight UTC. The assertion looks not at today's
		// date but at the window's OWN date; otherwise a test running at the turn
		// of the month would fail all by itself.
		require.NotNil(t, employee.SpendingWindowStart,
			"with a monthly period the window start must be set")

		window := employee.SpendingWindowStart.UTC()
		assert.Equal(t,
			time.Date(window.Year(), window.Month(), 1, 0, 0, 0, 0, time.UTC), window,
			"the monthly window must start at the first instant of the month (UTC)")
		assert.False(t, window.After(time.Now().UTC()),
			"the window cannot start in the future")
	})

	t.Run("updating the limit is reflected in the storefront", func(t *testing.T) {
		// This is the proof that the rule is read LIVE: had the storefront read
		// the employee record once at startup and cached it, this subtest would
		// fail and a limit the operator raised would never be enforced.
		updated := b2bNewLimit
		code, body := s.adminRequest(http.MethodPut, "/admin/v1/b2b/employees/"+employeeID, token,
			map[string]any{"spending_limit": updated})
		require.Equal(t, http.StatusOK, code, "could not update the employee; body: %s", body)

		employee := b2bReadStorefrontEmployee(t, s, storefrontKey, customerID)
		require.NotNil(t, employee.SpendingLimit, "the updated limit must be set")
		assert.Equal(t, b2bNewLimit, *employee.SpendingLimit,
			"the storefront must return the CURRENT limit")
	})

	t.Run("a customer cannot be added as an employee to a second company", func(t *testing.T) {
		// The rule lives not in the application but in the DATABASE (the unique
		// index of the link table, see b2b service Definitions). This subtest
		// drives it against the real schema: had the index not been created on a
		// cold start, the request would return 201 and which company's limit the
		// customer is subject to would become ambiguous — and ambiguity means the
		// rule is not enforced at all.
		otherCompany := b2bOpenCompany(t, s, token,
			"Smoke B2B Second Inc.", "smoke-b2b-second@example.test", "never")

		code, body := s.adminRequest(http.MethodPost, "/admin/v1/b2b/employees", token,
			map[string]any{"company_id": otherCompany, "customer_id": customerID})

		assert.Equal(t, http.StatusConflict, code,
			"the same customer must not be bound to a second company; body: %s", body)
	})

	t.Run("a storefront request without a key returns 401", func(t *testing.T) {
		code, body := s.storefrontRequest(http.MethodGet,
			"/store/v1/b2b/customers/"+customerID+"/employee", "", nil)

		assert.Equal(t, http.StatusUnauthorized, code,
			"a storefront request without a publishable key must be rejected; body: %s", body)
	})

	t.Run("an admin request without credentials returns 401", func(t *testing.T) {
		// The EXISTENCE of the path must not leak either: the guard runs before
		// route resolution.
		code, body := s.request(http.MethodGet, "/admin/v1/b2b/companies", "")

		assert.Equal(t, http.StatusUnauthorized, code,
			"a b2b admin request without credentials must be rejected; body: %s", body)
	})
}
