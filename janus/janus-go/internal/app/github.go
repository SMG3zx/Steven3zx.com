package app

// RepositoryList is the repositories an Account can import. Connected is false
// when the Account has not connected GitHub.
type RepositoryList struct {
	Repositories []Repository `json:"repositories"`
	Connected    bool         `json:"connected"`
}
