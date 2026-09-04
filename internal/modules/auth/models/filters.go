package models

// The shared rule of the types in this file is this: every field is a pointer,
// nil means "do not touch / do not filter", and a non-nil pointer is a REAL
// request even when the value it carries is the zero value (empty string,
// false). In a design that does not separate the two cases, a "clear the
// avatar" request would silently turn into "do not touch the avatar".

// UserFilter is the filter applied to a user listing.
type UserFilter struct {
	// Email, when given, returns only the user holding that email address.
	// The value must already have been normalized by the caller.
	Email *string
	// Scope, when given, returns only the users holding that scope.
	Scope *string
}

// UserPatch is the partial update of a user.
//
// For Metadata and Scopes nil carries the same meaning: when a slice/map is
// given the WHOLE column is replaced, no merging is done.
type UserPatch struct {
	// Email is the new email address; it must already have been normalized by
	// the caller.
	Email *string
	// FirstName is the new first name.
	FirstName *string
	// LastName is the new last name.
	LastName *string
	// AvatarURL is the new avatar address.
	AvatarURL *string
	// Scopes is the new scope list; it replaces the whole column.
	Scopes []string
	// Metadata is the new metadata map; it replaces the whole column.
	Metadata map[string]any
}

// APIKeyFilter is the filter applied to an API key listing.
type APIKeyFilter struct {
	// Type, when given, returns only the keys of that type.
	Type *APIKeyType
	// Revoked, when given, filters by the revoked/not-revoked distinction.
	Revoked *bool
}

// SalesChannelFilter is the filter applied to a sales channel listing.
type SalesChannelFilter struct {
	// Name, when given, returns only the channel holding that name.
	Name *string
	// IsDisabled, when given, filters by the disabled/enabled distinction.
	IsDisabled *bool
}

// SalesChannelPatch is the partial update of a sales channel.
type SalesChannelPatch struct {
	// Name is the channel's new name; it is unique among live channels.
	Name *string
	// Description is the channel's new description.
	Description *string
	// IsDisabled is the channel's new enablement state.
	IsDisabled *bool
	// Metadata is the new metadata map; it replaces the whole column.
	Metadata map[string]any
}
