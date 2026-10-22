// Package account is the contract of deleting an Account across services. The
// identity service announces the deletion on DeletingSubject. Each service that
// keeps records of an Account removes them and confirms with ClearedSubject,
// and identity deletes the User once every service in Services has confirmed.
package account

// DeletingSubject is the event identity publishes when an Account starts
// deleting.
const DeletingSubject = "janus.events.identity.account_deleting"

// ClearedFilter matches the confirmations of every service.
const ClearedFilter = "janus.events.*.account_cleared"

// Services are the services that confirm an Account's deletion, in no order.
var Services = []string{"fleet", "projects", "builds"}

// ClearedSubject is the subject service confirms its deletion of an Account on.
func ClearedSubject(service string) string {
	return "janus.events." + service + ".account_cleared"
}

// ClearedMsgID identifies one service's confirmation for one Account, so that a
// confirmation published twice is stored once.
func ClearedMsgID(service, ownerID string) string {
	return "account_cleared:" + service + ":" + ownerID
}

// Deleting is the payload of DeletingSubject.
type Deleting struct {
	UserID string `json:"user_id"`
}

// Cleared is the payload of a service's ClearedSubject: the service has no
// records left for the Owner.
type Cleared struct {
	OwnerID string `json:"owner_id"`
	Service string `json:"service"`
}
