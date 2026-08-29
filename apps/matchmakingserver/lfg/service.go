package lfg

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	ErrInvalidEntry        = errors.New("invalid lfg entry")
	ErrPlayerAlreadyQueued = errors.New("player already queued for lfg")
	ErrPlayerNotQueued     = errors.New("player is not queued for lfg")
)

// candidateWindow bounds the combination search. The matcher tries subsets of
// the queue head, which is exponential in the window, not in queue length.
const candidateWindow = 16

// GroupMaker turns a match into a real cluster group. It is the group service
// in production; the point of the interface is that the matching logic can be
// tested without one.
type GroupMaker interface {
	CreateGroup(ctx context.Context, leader PlayerKey, leaderName string, members []Member) (uint64, error)
}

type Service struct {
	mut sync.Mutex

	instanceID string
	policy     MatchPolicy
	groups     GroupMaker
	now        func() time.Time

	queues   map[uint32][]*Entry
	entries  map[string]*Entry
	players  map[PlayerKey]*Entry
	grouped  map[PlayerKey]PlayerStatus
	requests map[string]formedGroup
}

// requestRetention bounds how long a request id keeps returning the group it
// formed. It exists to absorb an RPC retry, nothing longer. Keeping entries
// forever made a caller whose request ids restart -- AC's proposal counter
// resets when a worldserver restarts -- collide with a stale entry and get
// handed a group formed for entirely different players.
const requestRetention = 2 * time.Minute

type formedGroup struct {
	groupID uint64
	formed  time.Time
}

func NewService(instanceID string, policy MatchPolicy, groups GroupMaker) *Service {
	if policy == nil {
		policy = BlizzlikePolicy{}
	}
	return &Service{
		instanceID: instanceID,
		policy:     policy,
		groups:     groups,
		now:        time.Now,
		queues:     make(map[uint32][]*Entry),
		entries:    make(map[string]*Entry),
		players:    make(map[PlayerKey]*Entry),
		grouped:    make(map[PlayerKey]PlayerStatus),
		requests:   make(map[string]formedGroup),
	}
}

func (s *Service) InstanceID() string { return s.instanceID }

// Join is idempotent by request id so a caller replaying after this service
// restarted, or retrying a timed-out RPC, cannot double-queue a party.
func (s *Service) Join(ctx context.Context, entry *Entry) (PlayerStatus, error) {
	if err := validateEntry(entry); err != nil {
		return PlayerStatus{}, err
	}

	s.mut.Lock()

	if formed, found := s.requests[entry.RequestID]; found {
		if s.now().Sub(formed.formed) > requestRetention {
			delete(s.requests, entry.RequestID)
		} else {
			// Only answer from the cache when this really is the same party.
			// A reused request id whose members have moved on must form a new
			// group, not inherit one built for other players.
			status, stillGrouped := s.grouped[entry.Leader]
			if stillGrouped && status.GroupID == formed.groupID {
				s.mut.Unlock()
				return status, nil
			}
			delete(s.requests, entry.RequestID)
		}
	}
	if _, found := s.entries[entry.RequestID]; found {
		leader := entry.Leader
		s.mut.Unlock()
		return PlayerStatus{Status: StatusQueued, QueuedAt: s.queuedAt(leader)}, nil
	}
	for _, member := range entry.Members {
		if s.players[member.PlayerKey] != nil {
			s.mut.Unlock()
			return PlayerStatus{}, ErrPlayerAlreadyQueued
		}
		if _, found := s.grouped[member.PlayerKey]; found {
			s.mut.Unlock()
			return PlayerStatus{}, ErrPlayerAlreadyQueued
		}
	}

	entry = cloneEntry(entry)
	if entry.QueuedAt.IsZero() || entry.QueuedAt.After(s.now()) {
		entry.QueuedAt = s.now()
	}
	s.entries[entry.RequestID] = entry
	for _, member := range entry.Members {
		s.players[member.PlayerKey] = entry
	}
	s.queues[entry.BattlegroupID] = append(s.queues[entry.BattlegroupID], entry)
	s.sortQueue(entry.BattlegroupID)

	match := s.findMatch(entry.BattlegroupID)
	if match == nil {
		s.mut.Unlock()
		return PlayerStatus{Status: StatusQueued, QueuedAt: entry.QueuedAt}, nil
	}

	// Reserve the matched entries before releasing the lock, so a concurrent
	// Join cannot match the same players into a second group while the group
	// service call is in flight.
	for _, matched := range match.Entries {
		s.removeFromQueue(matched)
	}
	s.mut.Unlock()

	groupID, err := s.createGroup(ctx, match)
	if err != nil {
		// Drop the reservation entirely rather than putting the entries back.
		// The caller reports the failure to the shard, which abandons the
		// proposal and re-queues every player under a fresh request id, so a
		// retained entry can never be refreshed -- it only holds those players
		// in s.players and answers every later join with ErrPlayerAlreadyQueued.
		// Seen in production: one formation failed on a stranded group_member
		// row and the player could not enter a dungeon again for the lifetime
		// of the process, the client popping a role check each time the shard
		// retried.
		s.mut.Lock()
		for _, matched := range match.Entries {
			delete(s.entries, matched.RequestID)
		}
		s.mut.Unlock()
		return PlayerStatus{}, err
	}

	s.mut.Lock()
	defer s.mut.Unlock()

	roles := make(map[PlayerKey]Role, len(match.Assignments))
	for _, assignment := range match.Assignments {
		roles[assignment.Player] = assignment.Role
	}
	for _, matched := range match.Entries {
		// The entry is no longer queued, so it must not keep occupying the
		// request index: leaving it there made a later join reusing the same
		// request id report "already queued" and never form its own group.
		// Idempotency for a retry is s.requests' job, and it is time-bounded.
		delete(s.entries, matched.RequestID)
		s.requests[matched.RequestID] = formedGroup{groupID: groupID, formed: s.now()}
		for _, member := range matched.Members {
			s.grouped[member.PlayerKey] = PlayerStatus{
				Status:       StatusGrouped,
				QueuedAt:     matched.QueuedAt,
				GroupID:      groupID,
				DungeonID:    match.DungeonID,
				AssignedRole: roles[member.PlayerKey],
			}
		}
	}

	// The join that completes a match is not necessarily part of it: anchoring
	// on the oldest entry can match five earlier waiters and leave the caller
	// exactly where they were.
	if status, found := s.grouped[entry.Leader]; found {
		return status, nil
	}
	return PlayerStatus{Status: StatusQueued, QueuedAt: entry.QueuedAt}, nil
}

func (s *Service) Leave(player PlayerKey) error {
	s.mut.Lock()
	defer s.mut.Unlock()

	entry := s.players[player]
	if entry == nil {
		return ErrPlayerNotQueued
	}
	s.removeFromQueue(entry)
	delete(s.entries, entry.RequestID)
	return nil
}

func (s *Service) Status(player PlayerKey) PlayerStatus {
	s.mut.Lock()
	defer s.mut.Unlock()

	if status, found := s.grouped[player]; found {
		return status
	}
	if entry := s.players[player]; entry != nil {
		return PlayerStatus{Status: StatusQueued, QueuedAt: entry.QueuedAt}
	}
	return PlayerStatus{}
}

func (s *Service) createGroup(ctx context.Context, match *Match) (uint64, error) {
	// The oldest entry's leader leads: it is the only choice that does not
	// punish a party for having waited.
	leaderEntry := match.Entries[0]
	for _, entry := range match.Entries[1:] {
		if entry.QueuedAt.Before(leaderEntry.QueuedAt) {
			leaderEntry = entry
		}
	}

	var leaderName string
	others := make([]Member, 0, 4)
	for _, entry := range match.Entries {
		for _, member := range entry.Members {
			if member.PlayerKey == leaderEntry.Leader {
				leaderName = member.Name
				continue
			}
			others = append(others, member)
		}
	}

	return s.groups.CreateGroup(ctx, leaderEntry.Leader, leaderName, others)
}

func (s *Service) findMatch(battlegroupID uint32) *Match {
	queue := s.queues[battlegroupID]
	if len(queue) == 0 {
		return nil
	}

	window := len(queue)
	if window > candidateWindow {
		window = candidateWindow
	}
	candidates := queue[:window]

	var found *Match
	var search func(index, players int, selected []*Entry)
	search = func(index, players int, selected []*Entry) {
		if found != nil || players > 5 {
			return
		}
		if players == 5 {
			if match, err := s.policy.Match(selected); err == nil {
				found = match
			}
			return
		}
		for i := index; i < len(candidates) && found == nil; i++ {
			search(i+1, players+len(candidates[i].Members), append(selected, candidates[i]))
		}
	}

	// Anchor on the oldest entry first, then walk forward. This gives the
	// longest waiter priority without letting one unmatchable party -- a lone
	// tank who selected a dungeon nobody else wants -- block the whole queue.
	for anchor := 0; anchor < len(candidates) && found == nil; anchor++ {
		search(anchor+1, len(candidates[anchor].Members), []*Entry{candidates[anchor]})
	}
	return found
}

func (s *Service) removeFromQueue(entry *Entry) {
	for _, member := range entry.Members {
		delete(s.players, member.PlayerKey)
	}
	queue := s.queues[entry.BattlegroupID]
	for i, queued := range queue {
		if queued == entry {
			s.queues[entry.BattlegroupID] = append(queue[:i], queue[i+1:]...)
			break
		}
	}
}

func (s *Service) queuedAt(player PlayerKey) time.Time {
	if entry := s.players[player]; entry != nil {
		return entry.QueuedAt
	}
	return time.Time{}
}

func (s *Service) sortQueue(battlegroupID uint32) {
	queue := s.queues[battlegroupID]
	sort.SliceStable(queue, func(i, j int) bool {
		return queue[i].QueuedAt.Before(queue[j].QueuedAt)
	})
}

func validateEntry(entry *Entry) error {
	if entry == nil || entry.RequestID == "" || entry.Leader.GUID == 0 ||
		len(entry.Members) == 0 || len(entry.Members) > 5 || len(entry.SelectedDungeons) == 0 {
		return ErrInvalidEntry
	}

	foundLeader := false
	seen := make(map[PlayerKey]struct{}, len(entry.Members))
	for _, member := range entry.Members {
		if member.RealmID == 0 || member.GUID == 0 || member.Roles == 0 || len(member.EligibleDungeons) == 0 {
			return ErrInvalidEntry
		}
		if _, duplicate := seen[member.PlayerKey]; duplicate {
			return ErrInvalidEntry
		}
		seen[member.PlayerKey] = struct{}{}
		foundLeader = foundLeader || member.PlayerKey == entry.Leader
	}
	if !foundLeader {
		return ErrInvalidEntry
	}
	return nil
}

func cloneEntry(entry *Entry) *Entry {
	clone := *entry
	clone.Members = append([]Member(nil), entry.Members...)
	for i := range clone.Members {
		clone.Members[i].EligibleDungeons = cloneSet(entry.Members[i].EligibleDungeons)
	}
	clone.SelectedDungeons = cloneSet(entry.SelectedDungeons)
	return &clone
}

func cloneSet(values map[uint32]struct{}) map[uint32]struct{} {
	result := make(map[uint32]struct{}, len(values))
	for value := range values {
		result[value] = struct{}{}
	}
	return result
}
