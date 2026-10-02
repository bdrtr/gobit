package models

// ContactTerms are what an operator corrects on a customer's record: the
// name and the phone the shop reaches them by (ADR 0337). The e-mail is not
// among them; it is what an account signs in with.
type ContactTerms struct {
	FirstName string
	LastName  string
	Phone     string
}

// Contact returns the customer's name and phone.
func (c Customer) Contact() ContactTerms {
	return ContactTerms{FirstName: c.FirstName, LastName: c.LastName, Phone: c.Phone}
}
