package file

import "github.com/bdrtr/gobit/core/personaldata"

// PersonalData declares what an upload may hold about a person (ADR 0278).
//
// gobit never reads a file, and a file can show a person: a claim's
// photograph of a parcel carries the label with the addressee's name. So the
// file's address is declared as open data beside the name the uploader's
// client gave it. Neither can be attributed to a person by this module, which
// offers no disclosure and no erasure; the sweep reports it as keeping what is
// listed here.
func (m *Module) PersonalData() personaldata.Declaration {
	return personaldata.Declaration{
		Holder: ModuleName,
		Holdings: []personaldata.Holding{
			{
				Table: tableUploads, Column: "original_name", Kind: personaldata.Open,
				Why:       "the name the uploader's client gave the file, which can name a person",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableUploads, Column: "url", Kind: personaldata.Open,
				Why:       "the address of the file's content, which gobit stores and never reads; a photograph can show a person or the label on their parcel",
				OnErasure: personaldata.Kept,
			},
		},
	}
}

// tableUploads is the one table the declaration names.
const tableUploads = "file_uploads"

var _ personaldata.Declarer = (*Module)(nil)
