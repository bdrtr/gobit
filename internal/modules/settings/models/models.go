// Package models defines the domain models of the settings module.
//
// There is one of them and it is a single record: who the shop IS. The types
// here are STRIPPED of database types, the way every other module's are — pgtype
// does not enter this package and the conversion is done in the repository.
package models

import "time"

// ProfileID is the identifier of the ONE store profile.
//
// The row is a singleton and the schema holds it with a CHECK. The name is
// written here rather than in every query so that the two cannot drift: a query
// with a different literal would silently read a row nothing writes.
const ProfileID = "default"

// StoreProfile is the shop's own identity, as it is printed on a document.
//
// # What it is not
//
// It is not a settings BAG. Every field here has a reader today — the invoice's
// seller party — and a field nobody prints would be a column this module could
// not answer a question about. The bag is what a "settings" table becomes when
// the first unread key is added to it.
type StoreProfile struct {
	// LegalName is the name documents are issued under; it is never empty.
	LegalName string
	// TaxNumber is the VKN/TCKN or its equivalent; it may be empty.
	TaxNumber string
	// TaxOffice is the office that number belongs to (Turkey); it may be empty.
	TaxOffice string
	// Email is the shop's own address, printed on the document; it may be empty.
	Email string
	// Address is the printed address, already formatted into lines.
	//
	// One string and not six fields: it is PRINTED and never queried, so a
	// structure would be one nobody reads and everybody has to fill in.
	Address string
	// CountryCode is the ISO 3166-1 alpha-2 country the shop sells from.
	CountryCode string
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}
