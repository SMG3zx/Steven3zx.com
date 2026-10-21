package fleet

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	contractfleet "janus/internal/contract/fleet"
	"janus/internal/services/fleet/headscale"
)

const (
	enrollmentCodeTTL  = 15 * time.Minute
	preAuthKeyTTL      = time.Hour
	maxAgentNameLength = 64
)

// The Agent statuses, as a listing reports them.
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

// AgentTag is the tailnet tag every node of an Owner's Agents carries.
func AgentTag(ownerID string) string { return "tag:acct-" + ownerID }

// hashEnrollmentCode is what fleet.db keeps of a code. The code is lower-cased
// and trimmed first, so how it was typed does not matter.
func hashEnrollmentCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

// issueCode makes a single-use Enrollment Code for the Owner, valid for 15
// minutes. The code is returned once; only its hash is stored.
func (s *Service) issueCode(ctx context.Context, request contractfleet.Owner) (contractfleet.IssuedCode, error) {
	if err := requireOwner(request.OwnerID); err != nil {
		return contractfleet.IssuedCode{}, err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return contractfleet.IssuedCode{}, fmt.Errorf("make an enrollment code: %w", err)
	}
	code := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
	expires := s.cfg.Now().Add(enrollmentCodeTTL).UTC()
	if err := s.insertCode(ctx, hashEnrollmentCode(code), request.OwnerID, expires); err != nil {
		return contractfleet.IssuedCode{}, err
	}
	s.audit(ctx, "info", "agent.code.issued", map[string]string{"user_id": request.OwnerID})
	return contractfleet.IssuedCode{Code: code, ExpiresAt: expires}, nil
}

// enroll exchanges an Enrollment Code for a single-use key that lets one node
// join the tailnet, and records the Agent. The code is used up even when a later
// step fails, so a failed enrollment needs a new code.
func (s *Service) enroll(ctx context.Context, request contractfleet.EnrollRequest) (contractfleet.Enrollment, error) {
	name := strings.TrimSpace(request.Name)
	if name == "" || utf8.RuneCountInString(name) > maxAgentNameLength {
		return contractfleet.Enrollment{}, invalid("%s", contractfleet.MessageAgentName)
	}
	now := s.cfg.Now()
	ownerID, consumed, err := s.useCode(ctx, hashEnrollmentCode(request.Code), now)
	if err != nil {
		return contractfleet.Enrollment{}, err
	}
	if !consumed {
		s.audit(ctx, "info", "agent.enroll.rejected", nil)
		return contractfleet.Enrollment{}, invalid("%s", contractfleet.MessageEnrollmentCodeInvalid)
	}
	tag := AgentTag(ownerID)
	agentID := uuid.NewString()
	leafCreds, leafUserPub, err := s.leafCredentials(agentID, ownerID)
	if err != nil {
		return contractfleet.Enrollment{}, err
	}
	key, err := s.cfg.Tailnet.CreatePreAuthKey(ctx, tag, now.Add(preAuthKeyTTL))
	if err != nil {
		return contractfleet.Enrollment{}, fmt.Errorf("create a pre-auth key: %w", err)
	}
	agent := contractfleet.AgentRecord{ID: agentID, OwnerID: ownerID, Name: name, Tag: tag, KeyID: key.ID, CreatedAt: now.UTC(), LeafUserPub: leafUserPub}
	if err = s.insertAgent(ctx, agent); err != nil {
		_ = s.cfg.Tailnet.ExpirePreAuthKey(ctx, key.ID)
		return contractfleet.Enrollment{}, err
	}
	s.audit(ctx, "info", "agent.enrolled", map[string]string{"user_id": ownerID, "agent_id": agent.ID})
	return contractfleet.Enrollment{AgentID: agent.ID, AuthKey: key.Key, Tag: tag, ControlURL: s.cfg.ControlURL, KeyExpires: key.Expires, LeafCreds: leafCreds}, nil
}

// leafCredentials makes the .creds file of an Agent's leaf connection and the
// public key of its user. Both are empty when the service has no AGENTS account
// to sign with.
func (s *Service) leafCredentials(agentID, ownerID string) ([]byte, string, error) {
	if s.cfg.Agents == nil {
		return nil, "", nil
	}
	creds, err := s.cfg.Agents.IssueLeafUser(agentID, ownerID)
	if err != nil {
		return nil, "", fmt.Errorf("issue the leaf credentials: %w", err)
	}
	body, err := creds.CredsFile()
	if err != nil {
		return nil, "", fmt.Errorf("encode the leaf credentials: %w", err)
	}
	return body, creds.UserPub, nil
}

// listAgents returns the Owner's Agents, oldest first, with whether each is
// online. When the tailnet cannot be asked, every Agent is reported as unknown.
func (s *Service) listAgents(ctx context.Context, request contractfleet.Owner) (contractfleet.AgentList, error) {
	if err := requireOwner(request.OwnerID); err != nil {
		return contractfleet.AgentList{}, err
	}
	records, err := s.loadAgents(ctx, request.OwnerID)
	if err != nil {
		return contractfleet.AgentList{}, err
	}
	nodes, nodesErr := s.cfg.Tailnet.Nodes(ctx)
	byKey := make(map[string]headscale.Node, len(nodes))
	for _, node := range nodes {
		byKey[node.KeyID] = node
	}
	statuses := make([]contractfleet.AgentStatus, 0, len(records))
	for _, record := range records {
		status := contractfleet.AgentStatus{AgentRecord: record, Status: AgentUnknown}
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
	return contractfleet.AgentList{Agents: statuses}, nil
}

// removeAgent deletes the Owner's Agent and revokes its node, so it can no longer
// be reached, and its key, so it cannot join. The Agent is kept if the tailnet
// fails, so removing again finishes the job.
func (s *Service) removeAgent(ctx context.Context, request contractfleet.AgentKey) (contractfleet.Removed, error) {
	if err := requireOwner(request.OwnerID); err != nil {
		return contractfleet.Removed{}, err
	}
	record, found, err := s.loadAgent(ctx, request.AgentID, request.OwnerID)
	if err != nil || !found {
		return contractfleet.Removed{}, err
	}
	if err = s.revoke(ctx, []contractfleet.AgentRecord{record}); err != nil {
		return contractfleet.Removed{}, err
	}
	removed, err := s.removeAgentRow(ctx, request.AgentID, request.OwnerID)
	if removed {
		s.audit(ctx, "info", "agent.removed", map[string]string{"user_id": request.OwnerID, "agent_id": request.AgentID})
	}
	return contractfleet.Removed{Removed: removed}, err
}

// revokeAgents removes every tailnet node and key of the Owner's Agents and
// leaves their records, which go with the Account. Accounts calls it before
// deleting an Account, so a tailnet failure stops the deletion and a retry
// finishes it.
func (s *Service) revokeAgents(ctx context.Context, request contractfleet.Owner) (contractfleet.Empty, error) {
	if err := requireOwner(request.OwnerID); err != nil {
		return contractfleet.Empty{}, err
	}
	records, err := s.loadAgents(ctx, request.OwnerID)
	if err != nil {
		return contractfleet.Empty{}, err
	}
	if err = s.revoke(ctx, records); err != nil {
		return contractfleet.Empty{}, err
	}
	for _, record := range records {
		s.audit(ctx, "info", "agent.revoked", map[string]string{"user_id": request.OwnerID, "agent_id": record.ID})
	}
	return contractfleet.Empty{}, nil
}

// revoke removes the tailnet nodes and keys of agents, and revokes their leaf
// credentials. The leaf credentials go first, so a failure on the tailnet leaves
// no Agent able to reach the hub.
func (s *Service) revoke(ctx context.Context, agents []contractfleet.AgentRecord) error {
	if len(agents) == 0 {
		return nil
	}
	for _, agent := range agents {
		if agent.LeafUserPub == "" {
			continue
		}
		if err := s.revokeLeaf(ctx, agent.LeafUserPub); err != nil {
			return err
		}
	}
	nodes, err := s.cfg.Tailnet.Nodes(ctx)
	if err != nil {
		return fmt.Errorf("list tailnet nodes: %w", err)
	}
	for _, agent := range agents {
		for _, node := range nodes {
			if node.KeyID == agent.KeyID {
				if err = s.cfg.Tailnet.DeleteNode(ctx, node.ID); err != nil {
					return fmt.Errorf("remove tailnet node: %w", err)
				}
			}
		}
		if err = s.cfg.Tailnet.ExpirePreAuthKey(ctx, agent.KeyID); err != nil {
			return fmt.Errorf("expire pre-auth key: %w", err)
		}
	}
	return nil
}

// ControlPlaneKey makes the single-use key, valid for an hour, with which the
// control plane's own node joins the tailnet under ControlTag, so it can reach
// Agents. The control plane asks for one only when its node has not joined. The
// control plane needs it before the Agent link exists, so it is a method the
// caller uses before Start, not a subject.
func (s *Service) ControlPlaneKey(ctx context.Context) (headscale.Key, error) {
	key, err := s.cfg.Tailnet.CreatePreAuthKey(ctx, contractfleet.ControlTag, s.cfg.Now().Add(preAuthKeyTTL))
	if err != nil {
		return headscale.Key{}, fmt.Errorf("create the control plane's pre-auth key: %w", err)
	}
	return key, nil
}

// deleteOwner removes everything the Owner has in fleet.db. The tailnet nodes
// must already be revoked, which revokeAgents does.
func (s *Service) deleteOwner(ctx context.Context, request contractfleet.Owner) (contractfleet.Empty, error) {
	if err := requireOwner(request.OwnerID); err != nil {
		return contractfleet.Empty{}, err
	}
	return contractfleet.Empty{}, s.deleteOwnerRows(ctx, request.OwnerID)
}
