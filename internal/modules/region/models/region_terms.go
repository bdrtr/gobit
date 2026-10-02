package models

// RegionTerms are what the panel corrects on a region from the ones it read:
// its name, whether taxes are computed for it and its tax rate (ADR 0362).
// The currency is not among them.
type RegionTerms struct {
	Name           string
	AutomaticTaxes bool
	TaxRate        int32
}

// Terms are the region's terms.
func (r Region) Terms() RegionTerms {
	return RegionTerms{Name: r.Name, AutomaticTaxes: r.AutomaticTaxes, TaxRate: r.TaxRate}
}
