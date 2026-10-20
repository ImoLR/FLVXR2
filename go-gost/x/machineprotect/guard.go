package machineprotect

// guard is the kernel side of the machine cap (nft on Linux).
type guard interface {
	// Ensure installs the business port sets and, while blocking, the drop chain.
	Ensure(ports PortSet, blocking bool) error
	// Remove deletes everything the guard installed.
	Remove() error
	// Dropped returns the dropped NEW-connection packets since the agent started.
	Dropped() (int64, error)
	// Blocking reports whether the drop chain is installed.
	Blocking() bool
	// Supported reports whether the kernel side is usable at all.
	Supported() bool
}
