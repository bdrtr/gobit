package adminui

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// A screen reads only what its privilege's module owns (ADR 0260).
//
// The panel spells its privileges itself, and internal/arch holds each spelling
// to a scope some module declares. That catches a misspelling and not a
// plausible wrong choice: a screen listed under one module's privilege while it
// prints another module's data compiles and passes everything else, and it is a
// second door into data the API keeps behind the other module's privilege.
//
// So every screen is requested by an operator holding exactly its privilege,
// through the router the panel binds, and every read and write it makes is
// held to the module that declares that privilege. Who owns what is read from
// SOURCE: an entity or an admin surface belongs to the tree that registers it
// in the container, a link's far end to the tree that registers that end's
// entity, and a privilege to the tree whose constant declares it. Nothing in
// the check is a table of owners somebody keeps.

// ownerTrees are the trees whose top-level directories each own something.
var ownerTrees = []string{filepath.Join("..", "modules"), filepath.Join("..", "..", "plugins")}

// scopeShape is a privilege's value: a module and a verb.
var scopeShape = regexp.MustCompile(`^[a-z0-9_]+:[a-z0-9_]+$`)

// linkEnds is one link definition's two ends, by entity name.
type linkEnds struct{ from, to string }

// ownership is what the source says each tree owns.
type ownership struct {
	// provided maps a container name to the tree that registers it.
	provided map[string]string
	// links maps a link name to its two ends.
	links map[string]linkEnds
	// scopes maps a privilege to the trees that declare it.
	scopes map[string][]string
}

// packageConsts is one tree's constants, by package name and constant name.
type packageConsts map[string]map[string]ast.Expr

// readOwnership parses every tree and every non-test file in it.
func readOwnership(t *testing.T) ownership {
	t.Helper()

	out := ownership{provided: map[string]string{}, links: map[string]linkEnds{}, scopes: map[string][]string{}}

	// query.ProviderSuffix is how a provider's name is spelled, and it lives in
	// core rather than in any tree.
	coreQuery := parseTree(t, filepath.Join("..", "..", "core", "query"))

	for _, tree := range ownerTrees {
		entries, err := os.ReadDir(tree)
		require.NoError(t, err)

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			owner := filepath.ToSlash(filepath.Join(filepath.Base(tree), entry.Name()))
			files := parseTree(t, filepath.Join(tree, entry.Name()))

			consts := packageConsts{}
			for _, table := range []map[string][]*ast.File{coreQuery, files} {
				for pkg, parsed := range table {
					for _, file := range parsed {
						collectConsts(consts, pkg, file)
					}
				}
			}

			for pkg, parsed := range files {
				for _, file := range parsed {
					readFile(t, &out, consts, owner, pkg, file)
				}
			}
		}
	}

	return out
}

// parseTree parses the non-test Go files under a directory, grouped by
// package name.
func parseTree(t *testing.T, dir string) map[string][]*ast.File {
	t.Helper()

	out := map[string][]*ast.File{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "testdata" {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		out[file.Name.Name] = append(out[file.Name.Name], file)

		return nil
	})
	require.NoError(t, err)

	return out
}

// collectConsts records a file's constants that have a value of their own.
func collectConsts(consts packageConsts, pkg string, file *ast.File) {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Values) != len(value.Names) {
				continue
			}
			if consts[pkg] == nil {
				consts[pkg] = map[string]ast.Expr{}
			}
			for i, name := range value.Names {
				consts[pkg][name.Name] = value.Values[i]
			}
		}
	}
}

// evalString evaluates a constant string expression: a literal, a constant of
// the file's own package or of another package of the same tree, or a sum of
// those.
func evalString(consts packageConsts, pkg string, expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(e.Value)

		return value, err == nil
	case *ast.ParenExpr:
		return evalString(consts, pkg, e.X)
	case *ast.Ident:
		value, ok := consts[pkg][e.Name]
		if !ok {
			return "", false
		}

		return evalString(consts, pkg, value)
	case *ast.SelectorExpr:
		other, ok := e.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		value, ok := consts[other.Name][e.Sel.Name]
		if !ok {
			return "", false
		}

		return evalString(consts, other.Name, value)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, ok := evalString(consts, pkg, e.X)
		if !ok {
			return "", false
		}
		right, ok := evalString(consts, pkg, e.Y)

		return left + right, ok
	}

	return "", false
}

// readFile records what one file registers, links and declares.
func readFile(t *testing.T, out *ownership, consts packageConsts, owner, pkg string, file *ast.File) {
	t.Helper()

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Values) != len(value.Names) {
				continue
			}
			for i, name := range value.Names {
				if !strings.HasPrefix(name.Name, "Scope") {
					continue
				}
				scope, ok := evalString(consts, pkg, value.Values[i])
				if ok && scopeShape.MatchString(scope) && !slices.Contains(out.scopes[scope], owner) {
					out.scopes[scope] = append(out.scopes[scope], owner)
				}
			}
		}
	}

	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			selector, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Provide" || len(n.Args) != 2 {
				return true
			}
			name, ok := evalString(consts, pkg, n.Args[0])
			if !ok {
				return true
			}
			if previous, taken := out.provided[name]; taken && previous != owner {
				t.Errorf("%q is registered by both %s and %s; the owner of what a screen reads "+
					"under it cannot be told", name, previous, owner)
			}
			out.provided[name] = owner
		case *ast.CompositeLit:
			if name, ends, ok := linkDefinition(consts, pkg, n); ok {
				out.links[name] = ends
			}
		}

		return true
	})
}

// linkDefinition reads a link definition's name and its ends' entities: a
// literal carrying Name, From and To, the last two being link sides.
func linkDefinition(consts packageConsts, pkg string, lit *ast.CompositeLit) (string, linkEnds, bool) {
	fields := map[string]ast.Expr{}
	for _, element := range lit.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := pair.Key.(*ast.Ident); ok {
			fields[key.Name] = pair.Value
		}
	}

	name, ok := evalString(consts, pkg, fields["Name"])
	if !ok {
		return "", linkEnds{}, false
	}
	from, ok := linkSideEntity(consts, pkg, fields["From"])
	if !ok {
		return "", linkEnds{}, false
	}
	to, ok := linkSideEntity(consts, pkg, fields["To"])
	if !ok {
		return "", linkEnds{}, false
	}

	return name, linkEnds{from: from, to: to}, true
}

// linkSideEntity reads a link side's entity, which is its module name when it
// names no entity (the rule core/link applies).
func linkSideEntity(consts packageConsts, pkg string, expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return "", false
	}
	selector, ok := lit.Type.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "LinkSide" {
		return "", false
	}

	var module, entity string
	for _, element := range lit.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, _ := pair.Key.(*ast.Ident)
		if key == nil {
			continue
		}
		value, ok := evalString(consts, pkg, pair.Value)
		if !ok {
			continue
		}
		switch key.Name {
		case "Module":
			module = value
		case "Entity":
			entity = value
		}
	}
	if entity == "" {
		entity = module
	}

	return entity, entity != ""
}

// storefrontPublished is what a screen may read whatever privilege opened it:
// what the storefront hands to anyone holding the publishable key, which
// carries no privilege at all, so reading it through the panel grants nothing.
//
// Each entry names the entity and the fields, and a read asking for any other
// field is held to its owner like every other read.
var storefrontPublished = []struct {
	entity string
	fields []string
	why    string
}{
	{
		EntityRegion, []string{fieldID, fieldCurrencyCod, fieldCurrency},
		"a currency's scale turns a minor-unit amount into a price, and /store/v1/regions publishes it",
	},
}

// published reports which storefront entry a read falls under, or -1.
func published(spec query.GraphSpec) int {
	if len(spec.Expand) > 0 {
		return -1
	}
	for i, entry := range storefrontPublished {
		if spec.Entity != entry.entity || len(spec.Fields) == 0 {
			continue
		}
		within := true
		for _, field := range spec.Fields {
			within = within && slices.Contains(entry.fields, field)
		}
		if within {
			return i
		}
	}

	return -1
}

// observed is one thing a screen read or wrote, with its owner.
type observed struct {
	// what names it: an entity, a link from an entity, or an admin surface.
	what string
	// owner is the tree the source says owns it, or "" when none does.
	owner string
	// published is the storefront entry it falls under, or -1.
	published int
}

// recordingCatalog answers every read with one record carrying every field
// and expansion it asked for, so a screen walks as far as its code goes.
type recordingCatalog struct{ specs []query.GraphSpec }

func (c *recordingCatalog) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	c.specs = append(c.specs, spec)

	return []query.Record{walkRecord(spec.Fields, spec.Expand)}, nil
}

// walkRecord is a record holding every asked-for field and expansion.
func walkRecord(fields []string, expand []query.Expansion) query.Record {
	record := query.Record{fieldID: "walk"}
	for _, field := range fields {
		if _, set := record[field]; !set {
			record[field] = "walk"
		}
	}
	for _, expansion := range expand {
		key := expansion.As
		if key == "" {
			key = expansion.Link
		}
		record[key] = walkRecord(expansion.Fields, expansion.Expand)
	}

	return record
}

// recordingSurfaces stands in for the three admin surfaces and records which
// one a screen reached, by the container name it was resolved under.
//
// When refuse is set every write is refused as invalid input, which is what
// sends a write back to drawing its page with the module's sentence on it.
type recordingSurfaces struct {
	reached []string
	refuse  bool
}

// reach records a surface and answers as the walk wants it to.
func (s *recordingSurfaces) reach(name string) error {
	s.reached = append(s.reached, name)
	if s.refuse {
		return errors.Invalid("walk_refused", "the walk refuses this write")
	}

	return nil
}

func (s *recordingSurfaces) StockVariant(context.Context, string) (string, error) {
	return "", s.reach(ServiceProductAdmin)
}

func (s *recordingSurfaces) PriceVariant(context.Context, string, string, int64) error {
	return s.reach(ServiceProductAdmin)
}

func (s *recordingSurfaces) CreateProduct(context.Context, string, string) (string, error) {
	return "", s.reach(ServiceProductAdmin)
}

func (s *recordingSurfaces) AddVariant(context.Context, string, string, string) (string, error) {
	return "", s.reach(ServiceProductAdmin)
}

func (s *recordingSurfaces) UpdateProductBasics(context.Context, string, string, string, string, int64) error {
	return s.reach(ServiceProductAdmin)
}

func (s *recordingSurfaces) ScheduleProduct(context.Context, string, *time.Time, *time.Time) error {
	return s.reach(ServiceProductAdmin)
}

func (s *recordingSurfaces) UnscheduleProduct(context.Context, string) error {
	return s.reach(ServiceProductAdmin)
}

func (s *recordingSurfaces) SetProductRelations(context.Context, string, map[string][]string) error {
	return s.reach(ServiceProductAdmin)
}

func (s *recordingSurfaces) SetProductAddOns(context.Context, string, []string) error {
	return s.reach(ServiceProductAdmin)
}

func (s *recordingSurfaces) SetVariantBundle(context.Context, string, []string, []int64, int64) error {
	return s.reach(ServiceProductAdmin)
}

func (s *recordingSurfaces) RevisionsJSON(context.Context, string, int32, int32) (json.RawMessage, int64, error) {
	return json.RawMessage(`[]`), 0, s.reach(ServiceProductAdmin)
}

func (s *recordingSurfaces) RestoreRevision(context.Context, string, int64, int64) ([]string, error) {
	return nil, s.reach(ServiceProductAdmin)
}

// recordingPrices is the pricing surface; a separate type because its method
// set would otherwise have to share a receiver with the product surface's.
type recordingPrices struct{ surfaces *recordingSurfaces }

func (p recordingPrices) PriceListsJSON(context.Context, int32, int32) (json.RawMessage, int64, error) {
	return json.RawMessage(`[]`), 0, p.surfaces.reach(ServicePricingAdmin)
}

func (p recordingPrices) CreatePriceList(
	context.Context, string, string, string, string, *time.Time, *time.Time,
) (string, error) {
	return "plist_walk", p.surfaces.reach(ServicePricingAdmin)
}

func (p recordingPrices) SwitchPriceListStatus(context.Context, string, string, string) error {
	return p.surfaces.reach(ServicePricingAdmin)
}

func (p recordingPrices) ListPricesJSON(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`[]`), p.surfaces.reach(ServicePricingAdmin)
}

func (p recordingPrices) AddListPrice(context.Context, string, string, string, int64, []string) error {
	return p.surfaces.reach(ServicePricingAdmin)
}

func (p recordingPrices) RemoveListPrice(context.Context, string, string) error {
	return p.surfaces.reach(ServicePricingAdmin)
}

func (p recordingPrices) SetBasePriceAmount(context.Context, string, string, int64, int64) error {
	return p.surfaces.reach(ServicePricingAdmin)
}

// recordingStock is the inventory surface.
type recordingStock struct{ surfaces *recordingSurfaces }

func (s recordingStock) StockLevelsJSON(context.Context, string) (json.RawMessage, error) {
	s.surfaces.reached = append(s.surfaces.reached, ServiceInventoryAdmin)
	return json.RawMessage(`[{"location_id":"walk","location_name":"walk"}]`), nil
}

func (s recordingStock) SetStockLevel(context.Context, string, string, int64, int64) error {
	return s.surfaces.reach(ServiceInventoryAdmin)
}

// recordingPayments records the payment module's surface (ADR 0287).
type recordingPayments struct{ surfaces *recordingSurfaces }

func (p recordingPayments) OfflineMethods(context.Context) []string {
	_ = p.surfaces.reach(ServicePaymentAdmin)

	return nil
}

func (p recordingPayments) RecordReceived(
	context.Context, string,
) (paymentID string, amount int64, currencyCode string, err error) {
	return "", 0, "", p.surfaces.reach(ServicePaymentAdmin)
}

// recordingPromotions records the promotion module's surface (ADR 0311).
type recordingPromotions struct{ surfaces *recordingSurfaces }

func (p recordingPromotions) PromotionsJSON(context.Context, string, int32, int32) (json.RawMessage, int64, error) {
	return json.RawMessage(`[]`), 0, p.surfaces.reach(ServicePromotionAdmin)
}

func (p recordingPromotions) SwitchPromotionStatus(context.Context, string, string, string) error {
	return p.surfaces.reach(ServicePromotionAdmin)
}

func (p recordingPromotions) CreateCoupon(
	context.Context, string, string, string, string, int64, string, *int64,
) (string, error) {
	return "promo_walk", p.surfaces.reach(ServicePromotionAdmin)
}

func (p recordingPromotions) AddPromotionRule(context.Context, string, string, string, string, []string) error {
	return p.surfaces.reach(ServicePromotionAdmin)
}

func (p recordingPromotions) RemovePromotionRule(context.Context, string, string) error {
	return p.surfaces.reach(ServicePromotionAdmin)
}

// recordingMemberships records the customer module's surface (ADR 0322).
type recordingMemberships struct{ surfaces *recordingSurfaces }

func (m recordingMemberships) AddCustomerToGroup(context.Context, string, string) error {
	return m.surfaces.reach(ServiceCustomerAdmin)
}

func (m recordingMemberships) RemoveCustomerFromGroup(context.Context, string, string) error {
	return m.surfaces.reach(ServiceCustomerAdmin)
}

func (m recordingMemberships) CreateGroup(context.Context, string, int32) (string, error) {
	return "custgrp_walk", m.surfaces.reach(ServiceCustomerAdmin)
}

func (m recordingMemberships) ReviseGroup(context.Context, string, string, int32, string, int32) error {
	return m.surfaces.reach(ServiceCustomerAdmin)
}

// recordingNotifications records the notification module's surface (ADR 0317).
type recordingNotifications struct{ surfaces *recordingSurfaces }

func (n recordingNotifications) DeliveriesJSON(context.Context, string, string, int32, int32) (json.RawMessage, int64, error) {
	return json.RawMessage(`[]`), 0, n.surfaces.reach(ServiceNotificationAdmin)
}

func (n recordingNotifications) ResendDelivery(context.Context, string) (string, error) {
	return "sent", n.surfaces.reach(ServiceNotificationAdmin)
}

func (p recordingPromotions) CampaignsJSON(context.Context, int32, int32) (json.RawMessage, int64, error) {
	return json.RawMessage(`[]`), 0, p.surfaces.reach(ServicePromotionAdmin)
}

func (p recordingPromotions) CreateCampaign(
	context.Context, string, string, string, *time.Time, *time.Time, string, *int64, string,
) (string, error) {
	return "camp_walk", p.surfaces.reach(ServicePromotionAdmin)
}

func (p recordingPromotions) SetPromotionCampaign(context.Context, string, string, string) error {
	return p.surfaces.reach(ServicePromotionAdmin)
}

func (p recordingPromotions) PromotionJSON(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{"rules":[],"latest_uses":[]}`), p.surfaces.reach(ServicePromotionAdmin)
}

// recordingCarts records the cart module's surface (ADR 0290).
type recordingCarts struct{ surfaces *recordingSurfaces }

func (c recordingCarts) OpenCart(context.Context, string, string, string) (string, error) {
	return "", c.surfaces.reach(ServiceCartAdmin)
}

func (c recordingCarts) AddLine(context.Context, string, string, string, int64) (string, error) {
	return "", c.surfaces.reach(ServiceCartAdmin)
}

func (c recordingCarts) SetShippingAddress(context.Context, string, map[string]string) error {
	return c.surfaces.reach(ServiceCartAdmin)
}

func (c recordingCarts) ShippingOptions(context.Context, string) (ids, names []string, amounts []int64, err error) {
	return nil, nil, nil, c.surfaces.reach(ServiceCartAdmin)
}

func (c recordingCarts) AddShippingMethod(context.Context, string, string) (string, error) {
	return "", c.surfaces.reach(ServiceCartAdmin)
}

func (c recordingCarts) SetBillingAddress(context.Context, string, map[string]string) error {
	return c.surfaces.reach(ServiceCartAdmin)
}

func (c recordingCarts) RemoveLine(context.Context, string, string) error {
	return c.surfaces.reach(ServiceCartAdmin)
}

func (c recordingCarts) Discard(context.Context, string) error {
	return c.surfaces.reach(ServiceCartAdmin)
}

func (c recordingCarts) Complete(
	context.Context, string, string, string, int64,
) (orderID string, outstanding int64, err error) {
	return "", 0, c.surfaces.reach(ServiceCartAdmin)
}

// recordingAfterSales is the order module's panel surface (ADR 0271); the walk
// takes one act, and every act reaches the same surface.
type recordingAfterSales struct{ surfaces *recordingSurfaces }

func (a recordingAfterSales) ReceiveReturn(
	context.Context, string, string,
) (lines int, units int64, warnings []string, err error) {
	return 0, 0, nil, a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) RefundReturn(
	context.Context, string, int64, string,
) (refunded int64, recorded bool, warnings []string, err error) {
	return 0, true, nil, a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) CancelReturn(context.Context, string) error {
	return a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) SettleClaim(
	context.Context, string, int64, string,
) (refunded int64, recorded bool, warnings []string, err error) {
	return 0, true, nil, a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) CancelClaim(context.Context, string) error {
	return a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) FundExchange(context.Context, string, string) error {
	return a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) RefundExchange(context.Context, string, string) error {
	return a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) CancelExchange(context.Context, string) error {
	return a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) DispatchReplacement(
	context.Context, string,
) (parcel string, units int64, already bool, err error) {
	return "", 0, false, a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) WithdrawReplacement(context.Context, string) error {
	return a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) OpenReturn(
	context.Context, string, []string, []int64, []int64, int64, string,
) (string, error) {
	return "", a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) OpenClaim(context.Context, string, string, int64, string) (string, error) {
	return "", a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) OpenParcel(context.Context, string, string) (parcel string, already bool, err error) {
	return "ful_walk", false, a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) ClaimEvidenceJSON(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`[]`), a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) AttachClaimEvidence(context.Context, string, string, string) (string, error) {
	return "cev_walk", a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) DetachClaimEvidence(context.Context, string) error {
	return a.surfaces.reach(ServiceOrderAdmin)
}

// recordingFiles records the file module's surface (ADR 0325).
type recordingFiles struct{ surfaces *recordingSurfaces }

func (f recordingFiles) MaxUploadBytes() int64 { return 1 << 20 }

func (f recordingFiles) UploadFile(context.Context, string, string, string, io.Reader) (id, address string, err error) {
	return "upl_walk", "/files/walk", f.surfaces.reach(ServiceFileAdmin)
}

func (f recordingFiles) UploadURL(context.Context, string) (string, error) {
	return "/files/walk", f.surfaces.reach(ServiceFileAdmin)
}

func (f recordingFiles) DeleteUpload(context.Context, string) error {
	return f.surfaces.reach(ServiceFileAdmin)
}

// recordingParcels records the fulfillment module's surface (ADR 0324).
type recordingParcels struct{ surfaces *recordingSurfaces }

func (p recordingParcels) ShipParcel(context.Context, string, string, string) error {
	return p.surfaces.reach(ServiceFulfillmentAdmin)
}

func (p recordingParcels) DeliverParcel(context.Context, string) error {
	return p.surfaces.reach(ServiceFulfillmentAdmin)
}

func (p recordingParcels) ReturnParcel(context.Context, string) error {
	return p.surfaces.reach(ServiceFulfillmentAdmin)
}

func (p recordingParcels) CancelParcel(context.Context, string) error {
	return p.surfaces.reach(ServiceFulfillmentAdmin)
}

func (a recordingAfterSales) OpenExchange(context.Context, string, int64, string) (string, error) {
	return "", a.surfaces.reach(ServiceOrderAdmin)
}

func (a recordingAfterSales) OpenReplacement(
	context.Context, string, string, []string, []int64, []string, []int64, string, string,
) (string, error) {
	return "", a.surfaces.reach(ServiceOrderAdmin)
}

// walkRequires are the privileges a write asks for beside its route's, in its
// handler. Adding a variant's price reaches the product module's surface,
// which links the price set, and writes pricing's price, so it asks for
// pricing:write as the import does (ADR 0207, ADR 0309). The walk holds them
// with the route's privilege; what the write reaches is still held to the
// route's owner.
var walkRequires = map[string][]string{
	routeKey(http.MethodPost, VariantPricesPath): {scopePricingWrite},
	// And keeping a variant's stock asks for inventory's write (ADR 0310).
	routeKey(http.MethodPost, VariantStockItemPath): {scopeInventoryWrite},
	// And attaching a claim's evidence stores a file (ADR 0325).
	routeKey(http.MethodPost, OrderClaimEvidencePath): {scopeFileWrite},
}

// requiredOwners are the modules whose write privilege the route's handler
// asks for beside the route's own (walkRequires). A write that holds an
// operator to another module's privilege may reach that module's surface: the
// API's two calls, each behind its own module's privilege, made by the panel
// in one form (ADR 0325).
func requiredOwners(t *testing.T, owners ownership, key string) []string {
	t.Helper()

	var out []string
	for _, scope := range walkRequires[key] {
		out = append(out, ownerOf(t, owners, scope))
	}

	return out
}

// mayReach reports whether the route may reach what the module owner owns:
// the route's privilege's module, or one whose write privilege the route's
// handler asks for beside it (ADR 0325).
func mayReach(t *testing.T, owners ownership, key, routeOwner, owner string) bool {
	t.Helper()

	return owner == routeOwner || slices.Contains(requiredOwners(t, owners, key), owner)
}

// TestAWriteReachesOnlyTheModulesItsHandlerAsksFor holds the walk's one
// exemption to what it asks of it: attaching evidence may reach the file
// module, whose write its handler asks for, and no third module; a write
// whose handler asks for nothing more reaches its route's module alone.
func TestAWriteReachesOnlyTheModulesItsHandlerAsksFor(t *testing.T) {
	t.Parallel()

	owners := readOwnership(t)
	evidence := routeKey(http.MethodPost, OrderClaimEvidencePath)
	order, file, customer := ownerOf(t, owners, scopeOrderWrite), ownerOf(t, owners, scopeFileWrite),
		ownerOf(t, owners, scopeCustomerWrite)
	require.NotEqual(t, order, file)

	assert.True(t, mayReach(t, owners, evidence, order, order))
	assert.True(t, mayReach(t, owners, evidence, order, file), "the file its handler asks to write")
	assert.False(t, mayReach(t, owners, evidence, order, customer), "a module nobody asked for")
	assert.False(t, mayReach(t, owners, routeKey(http.MethodPost, OrderParcelsPath), order, file),
		"a write whose handler asks for nothing more")
}

// walkMultipart are the writes whose form is a multipart body, sent with a
// file under its file field (ADR 0325).
var walkMultipart = map[string]string{
	routeKey(http.MethodPost, OrderClaimEvidencePath): formEvidenceFile,
}

// walkScopes is the route's privilege with those its handler asks for beside
// it, and any given after them.
func walkScopes(key, scope string, more ...string) []string {
	return append(append([]string{scope}, walkRequires[key]...), more...)
}

// walkForms is a form each write accepts, so the walk reaches the surface
// behind it. A write the panel binds with no entry here fails the walk.
var walkForms = map[string]url.Values{
	routeKey(http.MethodPost, ProductEditPath): {
		"title": {"Walk"}, "handle": {"walk"}, "status": {statusDraft}, "version": {"1"},
	},
	routeKey(http.MethodPost, ProductRelationsPath): {FieldCrossSellIDs: {""}},
	// Creating a product and adding a variant (ADR 0307).
	routeKey(http.MethodPost, ProductNewPath):      {"title": {"Walk"}},
	routeKey(http.MethodPost, ProductVariantsPath): {"variant_title": {"Walk"}},
	// Adding a variant's price (ADR 0309).
	routeKey(http.MethodPost, VariantPricesPath): {"currency": {"TRY"}, "amount": {"1"}},
	// Keeping a variant's stock (ADR 0310).
	routeKey(http.MethodPost, VariantStockItemPath): {},
	routeKey(http.MethodPost, ProductAddOnsPath):    {"add_ons": {""}},
	routeKey(http.MethodPost, VariantPricePath): {
		"price_set_id": {"walk"}, "currency": {"TRY"}, "amount": {"1"}, "read_amount": {"1"}, "minor": {"1"},
	},
	routeKey(http.MethodPost, VariantStockPath): {
		"inventory_item_id": {"walk"}, "location_id": {"walk"}, "quantity": {"1"}, "read_quantity": {"0"},
	},
	routeKey(http.MethodPost, VariantBundlePath): {"parts": {""}, "version": {"1"}},
	// The walk withdraws a return; every act reaches the same surface.
	routeKey(http.MethodPost, OrderAfterSalePath): {},
	// And opens one (ADR 0272).
	routeKey(http.MethodPost, OrderAfterSaleOpenPath): {},
	// And records an offline payment (ADR 0287).
	routeKey(http.MethodPost, OrderPaymentReceivedPath): {},
	// The telephone order opens a cart and adds a line (ADR 0290).
	routeKey(http.MethodPost, CartsPath):     {formCountryCode: {"TR"}, formEmail: {"caller@example.com"}},
	routeKey(http.MethodPost, CartLinesPath): {formSalesChannelID: {"sc_1"}, formVariantID: {"variant_1"}, formQuantity: {"1"}},
	// And completes it (ADR 0291).
	routeKey(http.MethodPost, CartAddressPath):  {"first_name": {"Ada"}, "country_code": {"TR"}},
	routeKey(http.MethodPost, CartBillingPath):  {"billing_first_name": {"Ada"}, "billing_country_code": {"TR"}},
	routeKey(http.MethodPost, CartShippingPath): {formShippingOption: {"so_1"}},
	routeKey(http.MethodPost, CartCompletePath): {
		formSalesChannelID: {"sc_1"}, formPaymentMethod: {"bank_transfer"}, formReadTotal: {"1000"},
	},
	// And corrects the operator's own cart (ADR 0300).
	routeKey(http.MethodPost, CartLineRemovePath): {},
	routeKey(http.MethodPost, CartDiscardPath):    {},
	// Switching a promotion's status (ADR 0312).
	routeKey(http.MethodPost, PromotionStatusPath): {formStatusFrom: {"active"}, formStatusTo: {"inactive"}},
	// A promotion's rules (ADR 0315).
	routeKey(http.MethodPost, PromotionRulesPath):      {formCategory: {"pcat_walk"}},
	routeKey(http.MethodPost, PromotionRuleRemovePath): {},
	// Writing a customer group (ADR 0323) and revising one (ADR 0329).
	routeKey(http.MethodPost, CustomerGroupListPath): {formGroupName: {"Walk"}},
	routeKey(http.MethodPost, CustomerGroupRevisePath): {
		formReadName: {"Walk"}, formReadRank: {"0"}, formGroupName: {"Walked"}, formGroupRank: {"1"},
	},
	// A variant's list prices (ADR 0327).
	routeKey(http.MethodPost, VariantListPricesPath): {
		formPriceSetID: {"pset_walk"}, formListPriceList: {"plist_walk"},
		formListPriceCurrency: {"TRY"}, formListPriceAmount: {"10"},
	},
	routeKey(http.MethodPost, VariantListPriceRemovePath): {formPriceSetID: {"pset_walk"}},
	// Switching a price list's status (ADR 0328).
	routeKey(http.MethodPost, PriceListStatusPath): {formStatusFrom: {"draft"}, formStatusTo: {"active"}},
	// Writing a price list (ADR 0326).
	routeKey(http.MethodPost, PriceListsPath): {formPriceListTitle: {"Walk"}},
	// A claim's evidence (ADR 0325).
	routeKey(http.MethodPost, OrderClaimEvidencePath):       {formEvidenceCaption: {"walk"}},
	routeKey(http.MethodPost, OrderClaimEvidenceDetachPath): {},
	// An order's parcels (ADR 0324).
	routeKey(http.MethodPost, OrderParcelsPath):   {formParcelKey: {"walk"}},
	routeKey(http.MethodPost, OrderParcelActPath): {},
	// A customer's groups (ADR 0322).
	routeKey(http.MethodPost, CustomerGroupsPath):      {formCustomerGroup: {"custgrp_walk"}},
	routeKey(http.MethodPost, CustomerGroupRemovePath): {},
	// Limiting a promotion to customer groups (ADR 0321).
	routeKey(http.MethodPost, PromotionGroupRulesPath): {formGroup: {"custgrp_walk"}},
	// Putting a promotion into a campaign (ADR 0320).
	routeKey(http.MethodPost, PromotionCampaignPath): {formCampaignTo: {"camp_walk"}},
	// Writing a campaign (ADR 0319).
	routeKey(http.MethodPost, CampaignsPath): {formCampaignName: {"Walk"}, formCampaignIdentifier: {"WALK"}},
	// Sending a notification again (ADR 0317).
	routeKey(http.MethodPost, NotificationResendPath): {"status": {"failed"}},
	// Restoring a product's revision (ADR 0316).
	routeKey(http.MethodPost, ProductRevisionRestorePath): {fieldVersion: {"3"}},
	// Writing a coupon (ADR 0314).
	routeKey(http.MethodPost, PromotionsPath): {
		formCouponCode: {"WALK"}, formCouponMeasure: {"percentage"}, formCouponAmount: {"10"}, formCouponTarget: {"order"},
	},
}

// panelWalk is one panel built on the recording doubles.
type panelWalk struct {
	router   chi.Router
	catalog  *recordingCatalog
	surfaces *recordingSurfaces
	owners   ownership
}

// newPanelWalk builds the panel through its constructor, as the composition
// root does, with each surface resolved under its own container name.
func newPanelWalk(t *testing.T, owners ownership) *panelWalk {
	t.Helper()

	walk := &panelWalk{catalog: &recordingCatalog{}, surfaces: &recordingSurfaces{}, owners: owners}

	c := container.New(nil)
	require.NoError(t, c.Provide(ServiceQuery, Catalog(walk.catalog)))
	require.NoError(t, c.Provide(ServiceAuth, wiringSession{}))
	require.NoError(t, c.Provide(InteropAuth, fakeAuthenticator{}))
	require.NoError(t, c.Provide(ServiceProductAdmin, ProductWriter(walk.surfaces)))
	require.NoError(t, c.Provide(ServicePricingAdmin, PriceWriter(recordingPrices{walk.surfaces})))
	require.NoError(t, c.Provide(ServiceInventoryAdmin, StockAdmin(recordingStock{walk.surfaces})))
	require.NoError(t, c.Provide(ServiceOrderAdmin, AfterSalesAdmin(recordingAfterSales{walk.surfaces})))
	require.NoError(t, c.Provide(ServiceFulfillmentAdmin, ParcelMover(recordingParcels{walk.surfaces})))
	require.NoError(t, c.Provide(ServiceFileAdmin, FileUploader(recordingFiles{walk.surfaces})))
	require.NoError(t, c.Provide(ServicePaymentAdmin, PaymentReceiver(recordingPayments{walk.surfaces})))
	require.NoError(t, c.Provide(ServiceCartAdmin, TelephoneCarts(recordingCarts{walk.surfaces})))
	require.NoError(t, c.Provide(ServicePromotionAdmin, PromotionLister(recordingPromotions{walk.surfaces})))
	require.NoError(t, c.Provide(ServiceNotificationAdmin, NotificationLister(recordingNotifications{walk.surfaces})))
	require.NoError(t, c.Provide(ServiceCustomerAdmin, GroupMembership(recordingMemberships{walk.surfaces})))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	ui, err := FromContainer(c, false, nil)
	require.NoError(t, err)

	walk.router = chi.NewRouter()
	ui.Routes(walk.router)

	return walk
}

// walkCase is one way a route is requested: a read, a write the surface
// accepts, or a write it refuses.
type walkCase struct {
	form   url.Values
	refuse bool
}

// walkCases is how a route is walked. A write is walked twice: accepted, which
// must reach a surface, and refused, which answers by drawing its page again.
func walkCases(t *testing.T, key string) []walkCase {
	t.Helper()

	if !strings.HasPrefix(key, http.MethodPost+" ") {
		return []walkCase{{}}
	}
	form, listed := walkForms[key]
	require.True(t, listed, "%s is a write with no form in walkForms, so the walk cannot "+
		"reach what it writes", key)

	return []walkCase{{form: form}, {form: form, refuse: true}}
}

// request sends one request as an operator holding exactly the given
// privileges and returns the status and everything it read and wrote.
func (w *panelWalk) request(t *testing.T, key string, walk walkCase, scopes ...string) (int, []observed) {
	t.Helper()

	form := walk.form

	method, pattern, _ := strings.Cut(key, " ")
	path := strings.NewReplacer("{id}", "walk", "{variantID}", "walk",
		"{kind}", "return", "{record}", "walk", "{act}", "cancel", "{line}", "walk",
		"{ruleID}", "walk", "{revision}", "2", "{parcel}", "walk", "{claim}", "walk",
		"{evidence}", "walk", "{priceID}", "walk").Replace(pattern)

	var req *http.Request
	if fileField, multi := walkMultipart[key]; multi {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for name, values := range form {
			for _, value := range values {
				require.NoError(t, writer.WriteField(name, value))
			}
		}
		part, err := writer.CreateFormFile(fileField, "walk.txt")
		require.NoError(t, err)
		_, err = part.Write([]byte("the walk's evidence"))
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		req = httptest.NewRequest(method, path, &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
	} else if method == http.MethodPost {
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, http.NoBody)
	}
	req = req.WithContext(corehttp.WithPrincipal(req.Context(),
		corehttp.Principal{ID: "usr_walk", Kind: "user", Scopes: scopes}))

	w.catalog.specs, w.surfaces.reached, w.surfaces.refuse = nil, nil, walk.refuse
	rec := httptest.NewRecorder()
	w.router.ServeHTTP(rec, req)

	var out []observed
	for _, spec := range w.catalog.specs {
		out = append(out, observed{
			what:      "the " + spec.Entity + " entity",
			owner:     w.owners.provided[spec.Entity+query.ProviderSuffix],
			published: published(spec),
		})
		out = append(out, w.expansions(spec.Entity, spec.Expand)...)
	}
	for _, name := range w.surfaces.reached {
		out = append(out, observed{what: "the " + name + " surface", owner: w.owners.provided[name], published: -1})
	}

	return rec.Code, out
}

// expansions resolves each expansion to the entity at the link's far end.
func (w *panelWalk) expansions(from string, expand []query.Expansion) []observed {
	var out []observed
	for _, expansion := range expand {
		ends, known := w.owners.links[expansion.Link]
		far := ""
		switch {
		case known && ends.from == from:
			far = ends.to
		case known && ends.to == from:
			far = ends.from
		}
		out = append(out, observed{
			what:      "the " + expansion.Link + " link from " + from,
			owner:     w.owners.provided[far+query.ProviderSuffix],
			published: -1,
		})
		if far != "" {
			out = append(out, w.expansions(far, expansion.Expand)...)
		}
	}

	return out
}

// scopedRoutes is every route the panel ships that asks for a privilege, in a
// stable order.
func scopedRoutes() []string {
	var out []string
	for key, scope := range builtInScopes() {
		if scope != "" {
			out = append(out, key)
		}
	}
	sort.Strings(out)

	return out
}

// ownerOf is the one tree that declares a privilege.
func ownerOf(t *testing.T, owners ownership, scope string) string {
	t.Helper()

	trees := owners.scopes[scope]
	require.Len(t, trees, 1,
		"%q is declared by %v; a privilege has to belong to exactly one tree for a screen's "+
			"reads to be held to it", scope, trees)

	return trees[0]
}

// TestEachScreenReadsOnlyWhatItsPrivilegesModuleOwns walks every screen and
// every write as an operator holding exactly the route's privilege.
//
// The refused write is the walk that caught the variant page reading the
// product module under the pricing and inventory writes.
func TestEachScreenReadsOnlyWhatItsPrivilegesModuleOwns(t *testing.T) {
	t.Parallel()

	owners := readOwnership(t)
	walk := newPanelWalk(t, owners)
	used := make([]bool, len(storefrontPublished))

	for _, key := range scopedRoutes() {
		scope := builtInScopes()[key]
		owner := ownerOf(t, owners, scope)

		for _, walkCase := range walkCases(t, key) {
			status, reads := walk.request(t, key, walkCase, walkScopes(key, scope)...)
			switch {
			case walkCase.form == nil:
				require.Equal(t, http.StatusOK, status,
					"%s answered %d to an operator holding %q; a screen that does not draw "+
						"has not been walked", key, status, scope)
			case walkCase.refuse:
				require.Equal(t, http.StatusUnprocessableEntity, status,
					"%s answered %d to a write its surface refused; the walk no longer "+
						"reaches the page a refusal draws", key, status)
			default:
				require.NotEmpty(t, walk.surfaces.reached,
					"%s answered %d with the form in walkForms and reached no surface; the "+
						"walk no longer reaches the write", key, status)
			}

			for _, read := range reads {
				if read.published >= 0 {
					used[read.published] = true
					continue
				}
				require.NotEmpty(t, read.owner,
					"%s reached %s, and no tree registers it. Either the source reader has gone "+
						"blind or the panel reads something nothing provides", key, read.what)
				assert.True(t, mayReach(t, owners, key, owner, read.owner),
					"%s asks for %q, which %s declares, and reaches %s, which %s owns.\n"+
						"The API keeps that data behind %s's own privilege; a screen that "+
						"shows it under another is a second door the first one does not know "+
						"about. Read it only for an operator holding the owner's privilege, "+
						"as the order page reads its payment (ADR 0251).",
					key, scope, owner, read.what, read.owner, read.owner)
			}
		}
	}

	for i, entry := range storefrontPublished {
		assert.True(t, used[i], "the storefront entry for %s (%s) excused no read; an entry "+
			"nothing needs is a door left open for the next one", entry.entity, entry.why)
	}
}

// TestAPrivilegeAddsOnlyItsOwnModulesData holds the reads a screen makes for
// an operator holding MORE than its privilege.
//
// The order page reads the payment only for one holding payment:read too, and
// the variant page the prices only for one holding pricing:read. Checking the
// guard's privilege by hand would assert my arithmetic. What is checked is that
// whatever a second privilege adds to a screen's reads belongs to that
// privilege's module, for every privilege any tree declares.
func TestAPrivilegeAddsOnlyItsOwnModulesData(t *testing.T) {
	t.Parallel()

	owners := readOwnership(t)
	walk := newPanelWalk(t, owners)

	declared := make([]string, 0, len(owners.scopes))
	for scope := range owners.scopes {
		declared = append(declared, scope)
	}
	sort.Strings(declared)
	require.GreaterOrEqual(t, len(declared), 20,
		"only %d privileges were read out of the trees; the reader has gone blind", len(declared))

	added := 0
	for _, key := range scopedRoutes() {
		scope := builtInScopes()[key]
		for _, walkCase := range walkCases(t, key) {
			_, alone := walk.request(t, key, walkCase, walkScopes(key, scope)...)
			base := map[string]bool{}
			for _, read := range alone {
				base[read.what] = true
			}

			for _, extra := range declared {
				if slices.Contains(walkScopes(key, scope), extra) || len(owners.scopes[extra]) != 1 {
					continue
				}
				_, with := walk.request(t, key, walkCase, walkScopes(key, scope, extra)...)
				for _, read := range with {
					if base[read.what] || read.published >= 0 {
						continue
					}
					added++
					assert.Equal(t, owners.scopes[extra][0], read.owner,
						"%s reaches %s, owned by %q, only when the operator also holds %q, which "+
							"%s declares. A section has to be opened by its owner's privilege; "+
							"opened by another's, it shows one module's data to an operator "+
							"granted a different one.",
						key, read.what, read.owner, extra, owners.scopes[extra][0])
				}
			}
		}
	}

	require.Positive(t, added,
		"no privilege added a read to any screen, and the order page adds two; the walk no "+
			"longer reaches the conditional sections")
}
