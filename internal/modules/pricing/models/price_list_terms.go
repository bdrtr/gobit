package models

import "time"

// PriceListTerms are what a merchant names a price list by and the window it
// applies in: the fields an operator revises on a list (ADR 0330). The type,
// the status and the metadata are not among them.
type PriceListTerms struct {
	Title       string
	Description string
	StartsAt    *time.Time
	EndsAt      *time.Time
}

// Terms returns the list's title, description and window.
func (l PriceList) Terms() PriceListTerms {
	return PriceListTerms{Title: l.Title, Description: l.Description, StartsAt: l.StartsAt, EndsAt: l.EndsAt}
}

// Same reports whether the terms are o's: the text alike, and each end of the
// window the same instant, or open in both.
func (t PriceListTerms) Same(o PriceListTerms) bool {
	return t.Title == o.Title && t.Description == o.Description &&
		sameMoment(t.StartsAt, o.StartsAt) && sameMoment(t.EndsAt, o.EndsAt)
}

// sameMoment reports whether two optional moments are the same instant, or
// both absent.
func sameMoment(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	return a.Equal(*b)
}
