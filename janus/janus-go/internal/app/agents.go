package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	enrollmentCodeTTL  = 15 * time.Minute
	preAuthKeyTTL      = time.Hour
	maxAgentNameLength = 64
)

// Agent statuses, as List reports them.
const (
	// AgentOnline means the Agent's node is on the tailnet and reported online.
	AgentOnline = "online"
	// AgentOffline means the node joined but the tailnet reports it offline.
	AgentOffline = "offline"
	// AgentPending means the Agent enrolled but no node has joined with its key.
	AgentPending = "pending"
	// AgentUnknown means the tailnet could not be asked.
	AgentUnknown = "unknown"
)

// ErrEnrollmentCodeInvalid is returned for a code that is unknown, was used or
// has expired. They are not told apart, so a guess learns nothing.
var ErrEnrollmentCodeInvalid = errors.New("the enrollment code is invalid or has expired")

// ErrAgentName is returned for an Agent name that is empty or too long.
var ErrAgentName = errors.New("give the Agent a name of 1 to 64 characters")

// IssuedCode is a new Enrollment Code. Code is shown to the Owner once and is
// never stored; only its hash is.
type IssuedCode struct {
	Code      string
	ExpiresAt time.Time
}

// Enrollment is what an Agent needs to join the tailnet: a single-use key, the
// control server to join, and the tag its node will carry. AuthKey is a secret.
type Enrollment struct {
	AgentID, AuthKey, Tag, ControlURL string
	KeyExpires                        time.Time
}

// AgentStatus is an Agent with whether its node is on the tailnet. LastSeen is
// the tailnet's last report of the node, and is zero for a pending Agent.
type AgentStatus struct {
	AgentRecord
	Status   string
	LastSeen time.Time
}

// Agents enrolls, lists and removes the Janus Agents on an Owner's machines. An
// Agent joins one tailnet under a tag that names its Owner, with a policy that
// lets the control plane reach Agents and nothing else. Every call is scoped to
// the owning Account.
type Agents struct {
	store      Store
	tailnet    Tailnet
	audit      Audit
	controlURL string
	now        func() time.Time
}

// NewAgents returns an Agents that records in store, enrolls nodes through
// tailnet and tells each Agent to join the control server at controlURL. It
// reads the time from now, so a code's lifetime can be tested without waiting.
func NewAgents(store Store, tailnet Tailnet, audit Audit, controlURL string, now func() time.Time) *Agents {
	return &Agents{store: store, tailnet: tailnet, audit: audit, controlURL: controlURL, now: now}
}

// AgentTag is the tailnet tag every node of an Owner's Agents carries.
func AgentTag(ownerID string) string { return "tag:acct-" + ownerID }

// IssueCode makes a single-use Enrollment Code for the Owner, valid for 15 minutes.
func (a *Agents) IssueCode(ctx context.Context, ownerID string) (IssuedCode, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return IssuedCode{}, fmt.Errorf("make an enrollment code: %w", err)
	}
	code := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
	expires := a.now().Add(enrollmentCodeTTL).UTC()
	if err := a.store.CreateEnrollmentCode(ctx, CreateEnrollmentCode{CodeHash: hashEnrollmentCode(code), OwnerID: ownerID, ExpiresAt: expires}); err != nil {
		return IssuedCode{}, err
	}
	a.record(ctx, "agent.code.issued", map[string]string{"user_id": ownerID})
	return IssuedCode{Code: code, ExpiresAt: expires}, nil
}

// Enroll exchanges an Enrollment Code for a single-use key that lets one node
// join the tailnet, and records the Agent. The code is used up even when a later
// step fails, so a failed enrollment needs a new code.
func (a *Agents) Enroll(ctx context.Context, code, name string) (Enrollment, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxAgentNameLength {
		return Enrollment{}, ErrAgentName
	}
	now := a.now()
	ownerID, consumed, err := a.store.ConsumeEnrollmentCode(ctx, hashEnrollmentCode(code), now)
	if err != nil {
		return Enrollment{}, err
	}
	if !consumed {
		a.record(ctx, "agent.enroll.rejected", nil)
		return Enrollment{}, ErrEnrollmentCodeInvalid
	}
	tag := AgentTag(ownerID)
	key, err := a.tailnet.CreatePreAuthKey(ctx, tag, now.Add(preAuthKeyTTL))
	if err != nil {
		return Enrollment{}, fmt.Errorf("create a pre-auth key: %w", err)
	}
	agent := CreateAgent{ID: uuid.NewString(), OwnerID: ownerID, Name: name, Tag: tag, KeyID: key.ID, CreatedAt: now.UTC()}
	if err = a.store.CreateAgent(ctx, agent); err != nil {
		_ = a.tailnet.ExpirePreAuthKey(ctx, key.ID)
		return Enrollment{}, err
	}
	a.record(ctx, "agent.enrolled", map[string]string{"user_id": ownerID, "agent_id": agent.ID})
	return Enrollment{AgentID: agent.ID, AuthKey: key.Key, Tag: tag, ControlURL: a.controlURL, KeyExpires: key.Expires}, nil
}

// List returns the Owner's Agents, oldest first, with whether each is online.
// When the tailnet cannot be asked, every Agent is reported as unknown.
func (a *Agents) List(ctx context.Context, ownerID string) ([]AgentStatus, error) {
	records, err := a.store.ListAgents(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	nodes, nodesErr := a.tailnet.Nodes(ctx)
	byKey := make(map[string]TailnetNode, len(nodes))
	for _, node := range nodes {
		byKey[node.KeyID] = node
	}
	statuses := make([]AgentStatus, 0, len(records))
	for _, record := range records {
		status := AgentStatus{AgentRecord: record, Status: AgentUnknown}
		if nodesErr == nil {
			node, joined := byKey[record.KeyID]
			switch {
			case !joined:
				status.Status = AgentPending
			case node.Online:
				status.Status, status.LastSeen = AgentOnline, node.LastSeen
			default:
				status.Status, status.LastSeen = AgentOffline, node.LastSeen
			}
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

// Remove deletes the Owner's Agent and revokes its node, so it can no longer
// be reached, and its key, so it cannot join. The Agent is kept if the tailnet
// fails, so removing again finishes the job. Removed is false for an Agent the
// Owner does not have.
func (a *Agents) Remove(ctx context.Context, ownerID, agentID string) (removed bool, err error) {
	record, found, err := a.store.FindAgent(ctx, agentID, ownerID)
	if err != nil || !found {
		return false, err
	}
	if err = a.revoke(ctx, []AgentRecord{record}); err != nil {
		return false, err
	}
	removed, err = a.store.DeleteAgent(ctx, agentID, ownerID)
	if removed {
		a.record(ctx, "agent.removed", map[string]string{"user_id": ownerID, "agent_id": agentID})
	}
	return removed, err
}

// RevokeAll removes every tailnet node and key of the Owner's Agents and leaves
// their records, which go with the Account. Accounts calls it before deleting an
// Account, so a tailnet failure stops the deletion and a retry finishes it.
func (a *Agents) RevokeAll(ctx context.Context, ownerID string) error {
	records, err := a.store.ListAgents(ctx, ownerID)
	if err != nil {
		return err
	}
	if err = a.revoke(ctx, records); err != nil {
		return err
	}
	for _, record := range records {
		a.record(ctx, "agent.revoked", map[string]string{"user_id": ownerID, "agent_id": record.ID})
	}
	return nil
}

// revoke removes the tailnet nodes and keys of agents. It does not touch the Store.
func (a *Agents) revoke(ctx context.Context, agents []AgentRecord) error {
	if len(agents) == 0 {
		return nil
	}
	nodes, err := a.tailnet.Nodes(ctx)
	if err != nil {
		return fmt.Errorf("list tailnet nodes: %w", err)
	}
	for _, agent := range agents {
		for _, node := range nodes {
			if node.KeyID == agent.KeyID {
				if err = a.tailnet.DeleteNode(ctx, node.ID); err != nil {
					return fmt.Errorf("remove tailnet node: %w", err)
				}
			}
		}
		if err = a.tailnet.ExpirePreAuthKey(ctx, agent.KeyID); err != nil {
			return fmt.Errorf("expire pre-auth key: %w", err)
		}
	}
	return nil
}

func (a *Agents) record(ctx context.Context, name string, fields map[string]string) {
	a.audit.Record(ctx, AuditEvent{Level: "info", Name: name, Fields: fields})
}

// hashEnrollmentCode is what the Store keeps of a code. The code is lower-cased
// and trimmed first, so how it was typed does not matter.
func hashEnrollmentCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

// Find returns the Owner's Agent. found is false for an Agent the Owner does
// not have.
func (a *Agents) Find(ctx context.Context, ownerID, agentID string) (agent AgentRecord, found bool, err error) {
	return a.store.FindAgent(ctx, agentID, ownerID)
}

// ControlPlaneKey makes the single-use key, valid for an hour, with which the
// control plane's own node joins the tailnet under ControlTag, so it can reach
// Agents. The control plane asks for one only when its node has not joined.
func (a *Agents) ControlPlaneKey(ctx context.Context) (TailnetKey, error) {
	key, err := a.tailnet.CreatePreAuthKey(ctx, ControlTag, a.now().Add(preAuthKeyTTL))
	if err != nil {
		return TailnetKey{}, fmt.Errorf("create the control plane's pre-auth key: %w", err)
	}
	a.record(ctx, "tailnet.control.key.issued", nil)
	return key, nil
}
