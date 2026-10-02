package models

// OptionTerms are what a merchant names a shipping option by, the fee it
// charges and whether the storefront offers it: the fields an operator revises
// on an option (ADR 0333). The provider, the profile, the price type and the
// region are not among them.
type OptionTerms struct {
	Name      string
	Amount    int64
	AdminOnly bool
}

// Terms returns the option's name, fee and storefront visibility.
func (o ShippingOption) Terms() OptionTerms {
	return OptionTerms{Name: o.Name, Amount: o.Amount, AdminOnly: o.AdminOnly}
}
