package adminui

import (
	"context"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// EntityCustomerGroup is the customer module's group entity in the read layer
// (ADR 0321), spelled by hand and pinned against the module's constant in
// internal/arch.
const EntityCustomerGroup = "customer_group"

// groupsOffered is how many customer groups a form offers: one under the
// customer module's page ceiling of a hundred, which refuses a larger page
// rather than trimming it, so that the one more read to learn there are more
// still fits in one page.
const groupsOffered = 99

// groupOption is one customer group a form offers.
type groupOption struct {
	ID   string
	Name string
}

// groupList is the customer groups a form offers: Unavailable when they could
// not be read, Truncated when there are more than it offers.
type groupList struct {
	Options     []groupOption
	Unavailable bool
	Truncated   bool
	Offered     int
}

// groupList reads the customer groups a form offers, newest first; one more
// than it offers is read, so whether there are more comes out of this read.
func (u *UI) groupList(ctx context.Context) groupList {
	records, err := u.catalog.Graph(ctx, query.GraphSpec{
		Entity: EntityCustomerGroup,
		Fields: []string{fieldID, fieldName},
		Limit:  groupsOffered + 1,
	})
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the customer groups; the group form will be absent", "error", err)

		return groupList{Unavailable: true, Offered: groupsOffered}
	}

	list := groupList{Truncated: len(records) > groupsOffered, Offered: groupsOffered}
	if list.Truncated {
		records = records[:groupsOffered]
	}
	for _, rec := range records {
		if id := recordString(rec, fieldID); id != "" {
			list.Options = append(list.Options, groupOption{ID: id, Name: recordString(rec, fieldName)})
		}
	}

	return list
}
