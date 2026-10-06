package models

// OptionTerms are what a merchant names a shipping option by, the fee it
// charges, whether the storefront offers it and how many business days its
// delivery takes: the fields an operator revises on an option (ADR 0333, ADR
// 0421). The provider, the profile, the price type and the region are not
// among them.
type OptionTerms struct {
	Name         string
	Amount       int64
	AdminOnly    bool
	DeliveryDays *DeliveryDays
}

// Terms returns the option's name, fee, storefront visibility and delivery
// days.
func (o ShippingOption) Terms() OptionTerms {
	return OptionTerms{Name: o.Name, Amount: o.Amount, AdminOnly: o.AdminOnly, DeliveryDays: o.DeliveryDays}
}
