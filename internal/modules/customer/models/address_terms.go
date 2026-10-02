package models

// AddressTerms are what an operator corrects on a customer's address: the
// name, company and lines printed on a parcel and an invoice (ADR 0342). The
// default flags are not among them; they concern the customer's other
// addresses too.
type AddressTerms struct {
	FirstName   string
	LastName    string
	Company     string
	Address1    string
	Address2    string
	City        string
	CountryCode string
	PostalCode  string
	Phone       string
}

// Terms returns the address's printed fields.
func (a CustomerAddress) Terms() AddressTerms {
	return AddressTerms{
		FirstName: a.FirstName, LastName: a.LastName, Company: a.Company, Address1: a.Address1,
		Address2: a.Address2, City: a.City, CountryCode: a.CountryCode, PostalCode: a.PostalCode, Phone: a.Phone,
	}
}
