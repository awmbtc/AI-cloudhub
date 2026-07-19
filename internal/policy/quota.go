package policy

import "fmt"

// DefaultMaxConcurrentBindings is the MVP per-user binding cap.
const DefaultMaxConcurrentBindings = 10

// DefaultMaxDrives is the MVP per-user drive map cap.
const DefaultMaxDrives = 20

// DefaultMaxProviders is the MVP per-user provider binding cap.
const DefaultMaxProviders = 20

// Quota holds control-plane resource limits per user.
type Quota struct {
	// MaxConcurrentBindings caps how many bindings a user may own (default 10).
	MaxConcurrentBindings int
	// MaxDrives caps how many drive maps a user may own (default 20).
	MaxDrives int
	// MaxProviders caps how many storage providers a user may register (default 20).
	MaxProviders int
}

// DefaultQuota is the production default used when callers pass zero values.
var DefaultQuota = Quota{
	MaxConcurrentBindings: DefaultMaxConcurrentBindings,
	MaxDrives:             DefaultMaxDrives,
	MaxProviders:          DefaultMaxProviders,
}

// CheckBindings returns an error if creating one more binding would exceed the cap.
// current is the user's existing binding count before the new create.
func (q Quota) CheckBindings(current int) error {
	max := q.MaxConcurrentBindings
	if max <= 0 {
		max = DefaultMaxConcurrentBindings
	}
	if current >= max {
		return fmt.Errorf("binding quota exceeded: max %d concurrent bindings per user", max)
	}
	return nil
}

// CheckDrives returns an error if creating one more drive would exceed the cap.
func (q Quota) CheckDrives(current int) error {
	max := q.MaxDrives
	if max <= 0 {
		max = DefaultMaxDrives
	}
	if current >= max {
		return fmt.Errorf("drive quota exceeded: max %d drives per user", max)
	}
	return nil
}

// CheckProviders returns an error if creating one more provider would exceed the cap.
func (q Quota) CheckProviders(current int) error {
	max := q.MaxProviders
	if max <= 0 {
		max = DefaultMaxProviders
	}
	if current >= max {
		return fmt.Errorf("provider quota exceeded: max %d providers per user", max)
	}
	return nil
}
