package models

// ChannelTerms are what an operator names a sales channel by and whether it
// is in use: the fields the panel corrects from the ones it read (ADR 0352).
type ChannelTerms struct {
	Name        string
	Description string
	IsDisabled  bool
}

// Terms are the channel's terms.
func (c SalesChannel) Terms() ChannelTerms {
	return ChannelTerms{Name: c.Name, Description: c.Description, IsDisabled: c.IsDisabled}
}
