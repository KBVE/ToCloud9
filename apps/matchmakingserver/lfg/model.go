package lfg

import "time"

type Role uint8

const (
	RoleTank Role = 1 << iota
	RoleHealer
	RoleDamage
)

type PlayerKey struct {
	RealmID uint32
	GUID    uint64
}

type Member struct {
	PlayerKey
	Name             string
	Roles            Role
	Level            uint8
	Class            uint8
	EligibleDungeons map[uint32]struct{}
}

type Entry struct {
	RequestID        string
	BattlegroupID    uint32
	Leader           PlayerKey
	Members          []Member
	SelectedDungeons map[uint32]struct{}
	QueuedAt         time.Time
}

type Assignment struct {
	Player PlayerKey
	Role   Role
}

// Match is what the policy produces. It is not a Blizzard-style proposal: no
// one is asked to accept it. The service turns it straight into a real cluster
// group, because the reason LFG moved here at all was to get a single group-id
// allocator, and a proposal that nobody converts leaves that unsolved.
type Match struct {
	DungeonID   uint32
	Entries     []*Entry
	Assignments []Assignment
}

type Status uint8

const (
	StatusNone Status = iota
	StatusQueued
	StatusGrouped
)

type PlayerStatus struct {
	Status       Status
	QueuedAt     time.Time
	GroupID      uint64
	DungeonID    uint32
	AssignedRole Role
}
