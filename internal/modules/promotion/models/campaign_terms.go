package models

import "time"

// CampaignTerms are what a merchant names a campaign by, the window it runs
// in and how far its budget goes: the fields an operator revises on a
// campaign (ADR 0331). The business identifier, the budget's unit and the
// counter are not among them.
type CampaignTerms struct {
	Name        string
	Description string
	StartsAt    *time.Time
	EndsAt      *time.Time
	BudgetLimit *int64
}

// Terms returns the campaign's name, description, window and budget limit.
func (c Campaign) Terms() CampaignTerms {
	return CampaignTerms{
		Name: c.Name, Description: c.Description, StartsAt: c.StartsAt, EndsAt: c.EndsAt, BudgetLimit: c.BudgetLimit,
	}
}

// Same reports whether the terms are o's: the text alike, each end of the
// window the same instant or open in both, and the same limit or none in
// both.
func (t CampaignTerms) Same(o CampaignTerms) bool {
	sameLimit := t.BudgetLimit == nil && o.BudgetLimit == nil ||
		t.BudgetLimit != nil && o.BudgetLimit != nil && *t.BudgetLimit == *o.BudgetLimit

	return t.Name == o.Name && t.Description == o.Description &&
		sameInstant(t.StartsAt, o.StartsAt) && sameInstant(t.EndsAt, o.EndsAt) && sameLimit
}

// sameInstant reports whether two optional moments are the same instant, or
// both absent.
func sameInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	return a.Equal(*b)
}
