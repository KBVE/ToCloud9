package lfg

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeGroupMaker struct {
	nextID  uint64
	calls   int
	leaders []PlayerKey
	sizes   []int
	err     error
}

func (f *fakeGroupMaker) CreateGroup(_ context.Context, leader PlayerKey, _ string, members []Member) (uint64, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	f.nextID++
	f.leaders = append(f.leaders, leader)
	f.sizes = append(f.sizes, len(members)+1)
	return f.nextID, nil
}

func dungeons(ids ...uint32) map[uint32]struct{} {
	set := make(map[uint32]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func solo(requestID string, guid uint64, roles Role, queuedAt time.Time, selected ...uint32) *Entry {
	key := PlayerKey{RealmID: 1, GUID: guid}
	return &Entry{
		RequestID:     requestID,
		BattlegroupID: 1,
		Leader:        key,
		Members: []Member{{
			PlayerKey:        key,
			Name:             requestID,
			Roles:            roles,
			Level:            80,
			EligibleDungeons: dungeons(selected...),
		}},
		SelectedDungeons: dungeons(selected...),
		QueuedAt:         queuedAt,
	}
}

func base() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) }

func fiveSolos(dungeonID uint32) []*Entry {
	start := base()
	return []*Entry{
		solo("tank", 1, RoleTank, start, dungeonID),
		solo("healer", 2, RoleHealer, start.Add(time.Second), dungeonID),
		solo("dps1", 3, RoleDamage, start.Add(2*time.Second), dungeonID),
		solo("dps2", 4, RoleDamage, start.Add(3*time.Second), dungeonID),
		solo("dps3", 5, RoleDamage, start.Add(4*time.Second), dungeonID),
	}
}

func TestJoinFormsGroupWhenCompositionIsSatisfied(t *testing.T) {
	groups := &fakeGroupMaker{}
	service := NewService("instance-1", nil, groups)

	var last PlayerStatus
	for _, entry := range fiveSolos(285) {
		status, err := service.Join(context.Background(), entry)
		if err != nil {
			t.Fatalf("join %s: %v", entry.RequestID, err)
		}
		last = status
	}

	if last.Status != StatusGrouped {
		t.Fatalf("expected the fifth join to form a group, got status %v", last.Status)
	}
	if last.GroupID == 0 {
		t.Fatal("expected a group id from the group service")
	}
	if last.DungeonID != 285 {
		t.Fatalf("expected dungeon 285, got %d", last.DungeonID)
	}
	if groups.calls != 1 {
		t.Fatalf("expected exactly one group creation, got %d", groups.calls)
	}
	if groups.sizes[0] != 5 {
		t.Fatalf("expected a 5-man group, got %d", groups.sizes[0])
	}
	// The longest waiter leads.
	if groups.leaders[0].GUID != 1 {
		t.Fatalf("expected the oldest entry's leader to lead, got %d", groups.leaders[0].GUID)
	}
}

func TestNoGroupWithoutATank(t *testing.T) {
	groups := &fakeGroupMaker{}
	service := NewService("instance-1", nil, groups)

	start := base()
	entries := []*Entry{
		solo("healer", 2, RoleHealer, start, 285),
		solo("dps1", 3, RoleDamage, start.Add(time.Second), 285),
		solo("dps2", 4, RoleDamage, start.Add(2*time.Second), 285),
		solo("dps3", 5, RoleDamage, start.Add(3*time.Second), 285),
		solo("dps4", 6, RoleDamage, start.Add(4*time.Second), 285),
	}
	for _, entry := range entries {
		status, err := service.Join(context.Background(), entry)
		if err != nil {
			t.Fatalf("join %s: %v", entry.RequestID, err)
		}
		if status.Status != StatusQueued {
			t.Fatalf("%s should still be queued, got %v", entry.RequestID, status.Status)
		}
	}
	if groups.calls != 0 {
		t.Fatalf("expected no group creation, got %d", groups.calls)
	}
}

func TestHybridTakesTheOpenRole(t *testing.T) {
	groups := &fakeGroupMaker{}
	service := NewService("instance-1", nil, groups)

	start := base()
	// The hybrid queues first as tank-or-damage. A greedy pass would hand it
	// damage and then never find a tank.
	entries := []*Entry{
		solo("hybrid", 1, RoleTank|RoleDamage, start, 285),
		solo("healer", 2, RoleHealer, start.Add(time.Second), 285),
		solo("dps1", 3, RoleDamage, start.Add(2*time.Second), 285),
		solo("dps2", 4, RoleDamage, start.Add(3*time.Second), 285),
		solo("dps3", 5, RoleDamage, start.Add(4*time.Second), 285),
	}
	var last PlayerStatus
	for _, entry := range entries {
		status, err := service.Join(context.Background(), entry)
		if err != nil {
			t.Fatalf("join %s: %v", entry.RequestID, err)
		}
		last = status
	}
	if last.Status != StatusGrouped {
		t.Fatal("hybrid should have been assigned tank so the group forms")
	}
}

func TestNoGroupWithoutACommonDungeon(t *testing.T) {
	groups := &fakeGroupMaker{}
	service := NewService("instance-1", nil, groups)

	start := base()
	entries := []*Entry{
		solo("tank", 1, RoleTank, start, 285),
		solo("healer", 2, RoleHealer, start.Add(time.Second), 285),
		solo("dps1", 3, RoleDamage, start.Add(2*time.Second), 285),
		solo("dps2", 4, RoleDamage, start.Add(3*time.Second), 285),
		solo("dps3", 5, RoleDamage, start.Add(4*time.Second), 601),
	}
	for _, entry := range entries {
		if _, err := service.Join(context.Background(), entry); err != nil {
			t.Fatalf("join %s: %v", entry.RequestID, err)
		}
	}
	if groups.calls != 0 {
		t.Fatalf("expected no group creation, got %d", groups.calls)
	}
}

func TestEligibilityIsNeverWidened(t *testing.T) {
	groups := &fakeGroupMaker{}
	service := NewService("instance-1", nil, groups)

	start := base()
	entries := fiveSolos(285)
	// Selected it, but the core said they are not eligible -- a lockout, say.
	entries[4].Members[0].EligibleDungeons = dungeons(601)
	entries[4].QueuedAt = start.Add(4 * time.Second)

	for _, entry := range entries {
		if _, err := service.Join(context.Background(), entry); err != nil {
			t.Fatalf("join %s: %v", entry.RequestID, err)
		}
	}
	if groups.calls != 0 {
		t.Fatal("a member ineligible for the only common dungeon must block the match")
	}
}

func TestJoinIsIdempotentByRequestID(t *testing.T) {
	groups := &fakeGroupMaker{}
	service := NewService("instance-1", nil, groups)

	entries := fiveSolos(285)
	for _, entry := range entries {
		if _, err := service.Join(context.Background(), entry); err != nil {
			t.Fatalf("join %s: %v", entry.RequestID, err)
		}
	}

	// A caller replaying after this service restarted resends every request.
	for _, entry := range entries {
		status, err := service.Join(context.Background(), entry)
		if err != nil {
			t.Fatalf("replay %s: %v", entry.RequestID, err)
		}
		if status.Status != StatusGrouped || status.GroupID == 0 {
			t.Fatalf("replay of %s should return the existing group, got %+v", entry.RequestID, status)
		}
	}
	if groups.calls != 1 {
		t.Fatalf("replay must not create a second group, got %d creations", groups.calls)
	}
}

// A formation failure must release everyone it reserved. The shard abandons the
// proposal and re-queues its players under a new request id, so entries kept
// here can never be refreshed: they just hold those players in the queue index
// and reject every later join with ErrPlayerAlreadyQueued. In production one
// failure locked a player out of the dungeon finder until the process
// restarted, the client popping a role check on each retry.
func TestFormationFailureReleasesEveryReservedPlayer(t *testing.T) {
	groups := &fakeGroupMaker{err: errors.New("group service unavailable")}
	service := NewService("instance-1", nil, groups)

	entries := fiveSolos(285)
	for _, entry := range entries[:4] {
		if _, err := service.Join(context.Background(), entry); err != nil {
			t.Fatalf("join %s: %v", entry.RequestID, err)
		}
	}
	if _, err := service.Join(context.Background(), entries[4]); err == nil {
		t.Fatal("expected the group service error to surface")
	}

	for _, entry := range entries {
		if status := service.Status(entry.Leader); status.Status != StatusNone {
			t.Fatalf("%s should be released after the failure, got %v", entry.RequestID, status.Status)
		}
	}

	// The shard re-queues them, which it does under new request ids.
	groups.err = nil
	requeued := fiveSolos(285)
	for i, entry := range requeued {
		entry.RequestID += "-retry"
		status, err := service.Join(context.Background(), entry)
		if err != nil {
			t.Fatalf("requeue %s: %v", entry.RequestID, err)
		}
		if i == len(requeued)-1 && status.Status != StatusGrouped {
			t.Fatalf("the requeued party should form a group, got %v", status.Status)
		}
	}
	if groups.calls != 2 {
		t.Fatalf("expected exactly one retry of the formation, got calls=%d", groups.calls)
	}
}

// The players a failed formation released must be able to queue again with the
// same request ids too -- a shard that restarts resets its proposal counter.
func TestFormationFailureDoesNotStickToRequestIDs(t *testing.T) {
	groups := &fakeGroupMaker{err: errors.New("group service unavailable")}
	service := NewService("instance-1", nil, groups)

	for _, entry := range fiveSolos(285)[:4] {
		if _, err := service.Join(context.Background(), entry); err != nil {
			t.Fatalf("join %s: %v", entry.RequestID, err)
		}
	}
	if _, err := service.Join(context.Background(), fiveSolos(285)[4]); err == nil {
		t.Fatal("expected the group service error to surface")
	}

	groups.err = nil
	for _, entry := range fiveSolos(285) {
		if _, err := service.Join(context.Background(), entry); err != nil {
			t.Fatalf("rejoin %s: %v", entry.RequestID, err)
		}
	}
	if groups.calls != 2 || groups.sizes[len(groups.sizes)-1] != 5 {
		t.Fatalf("expected the five to reform, got calls=%d sizes=%v", groups.calls, groups.sizes)
	}
}

func TestLeaveRemovesFromQueue(t *testing.T) {
	groups := &fakeGroupMaker{}
	service := NewService("instance-1", nil, groups)

	entry := solo("tank", 1, RoleTank, base(), 285)
	if _, err := service.Join(context.Background(), entry); err != nil {
		t.Fatalf("join: %v", err)
	}
	if err := service.Leave(entry.Leader); err != nil {
		t.Fatalf("leave: %v", err)
	}
	if status := service.Status(entry.Leader); status.Status != StatusNone {
		t.Fatalf("expected no status after leaving, got %v", status.Status)
	}
	if err := service.Leave(entry.Leader); !errors.Is(err, ErrPlayerNotQueued) {
		t.Fatalf("expected ErrPlayerNotQueued, got %v", err)
	}
}

func TestPartiesAreNotSplit(t *testing.T) {
	groups := &fakeGroupMaker{}
	service := NewService("instance-1", nil, groups)

	start := base()
	leader := PlayerKey{RealmID: 1, GUID: 10}
	party := &Entry{
		RequestID:     "party",
		BattlegroupID: 1,
		Leader:        leader,
		Members: []Member{
			{PlayerKey: leader, Name: "lead", Roles: RoleTank, Level: 80, EligibleDungeons: dungeons(285)},
			{PlayerKey: PlayerKey{RealmID: 1, GUID: 11}, Name: "friend", Roles: RoleHealer, Level: 80, EligibleDungeons: dungeons(285)},
		},
		SelectedDungeons: dungeons(285),
		QueuedAt:         start,
	}

	if _, err := service.Join(context.Background(), party); err != nil {
		t.Fatalf("join party: %v", err)
	}
	for i, entry := range []*Entry{
		solo("dps1", 3, RoleDamage, start.Add(time.Second), 285),
		solo("dps2", 4, RoleDamage, start.Add(2*time.Second), 285),
	} {
		if _, err := service.Join(context.Background(), entry); err != nil {
			t.Fatalf("join %d: %v", i, err)
		}
	}
	if groups.calls != 0 {
		t.Fatal("four players is not a group")
	}

	status, err := service.Join(context.Background(), solo("dps3", 5, RoleDamage, start.Add(3*time.Second), 285))
	if err != nil {
		t.Fatalf("join fifth: %v", err)
	}
	if status.Status != StatusGrouped {
		t.Fatalf("expected a group, got %v", status.Status)
	}
	if groups.sizes[0] != 5 {
		t.Fatalf("expected the party to be taken whole into a 5-man, got %d", groups.sizes[0])
	}
}

func TestAlreadyQueuedIsRejected(t *testing.T) {
	service := NewService("instance-1", nil, &fakeGroupMaker{})

	if _, err := service.Join(context.Background(), solo("first", 1, RoleTank, base(), 285)); err != nil {
		t.Fatalf("join: %v", err)
	}
	_, err := service.Join(context.Background(), solo("second", 1, RoleDamage, base(), 285))
	if !errors.Is(err, ErrPlayerAlreadyQueued) {
		t.Fatalf("expected ErrPlayerAlreadyQueued, got %v", err)
	}
}

func TestInvalidEntriesAreRejected(t *testing.T) {
	service := NewService("instance-1", nil, &fakeGroupMaker{})

	noRoles := solo("noroles", 1, 0, base(), 285)
	if _, err := service.Join(context.Background(), noRoles); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("expected ErrInvalidEntry for a member with no roles, got %v", err)
	}

	noEligible := solo("noeligible", 2, RoleTank, base(), 285)
	noEligible.Members[0].EligibleDungeons = dungeons()
	if _, err := service.Join(context.Background(), noEligible); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("expected ErrInvalidEntry when the core sent no eligible dungeons, got %v", err)
	}

	notAMember := solo("stranger", 3, RoleTank, base(), 285)
	notAMember.Leader = PlayerKey{RealmID: 1, GUID: 999}
	if _, err := service.Join(context.Background(), notAMember); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("expected ErrInvalidEntry when the leader is not in the member list, got %v", err)
	}
}

// AC builds its request id from a per-worldserver proposal counter, and that
// counter restarts when the worldserver does. This service does not restart
// with it, so a fresh proposal 2 arrived carrying the id of a proposal 2 that
// had formed a group half an hour earlier -- and got handed that group. The
// live symptom was four newly matched bots appearing in a stale group from the
// previous run, with no invites issued and the call returning in microseconds.
func TestReusedRequestIDDoesNotInheritAStaleGroup(t *testing.T) {
	groups := &fakeGroupMaker{}
	service := NewService("instance-1", nil, groups)

	for _, entry := range fiveSolos(285) {
		if _, err := service.Join(context.Background(), entry); err != nil {
			t.Fatalf("join %s: %v", entry.RequestID, err)
		}
	}
	if groups.calls != 1 {
		t.Fatalf("expected the first party to form one group, got %d", groups.calls)
	}
	firstGroup := groups.nextID

	// A different five, reusing the same request ids after a worldserver restart.
	start := base().Add(time.Hour)
	second := []*Entry{
		solo("tank", 21, RoleTank, start, 285),
		solo("healer", 22, RoleHealer, start.Add(time.Second), 285),
		solo("dps1", 23, RoleDamage, start.Add(2*time.Second), 285),
		solo("dps2", 24, RoleDamage, start.Add(3*time.Second), 285),
		solo("dps3", 25, RoleDamage, start.Add(4*time.Second), 285),
	}

	var last PlayerStatus
	for _, entry := range second {
		status, err := service.Join(context.Background(), entry)
		if err != nil {
			t.Fatalf("join %s: %v", entry.RequestID, err)
		}
		last = status
	}

	if groups.calls != 2 {
		t.Fatalf("the second party must form its own group, got %d creations", groups.calls)
	}
	if last.GroupID == firstGroup {
		t.Fatalf("second party inherited the first party's group %d", firstGroup)
	}
	if got := groups.sizes[1]; got != 5 {
		t.Fatalf("expected a fresh 5-man group, got %d", got)
	}
}
