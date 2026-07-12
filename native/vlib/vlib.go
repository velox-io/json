package vlib

import "unsafe"

// Available reports whether the native lookup syso is linked on this
// platform. False on unsupported platforms or when a build tag disables it.
var Available bool

// Key mirrors C ndec_lookup_key. Callers keep the underlying string alive
// until Init returns (Init copies the bytes into storage).
type Key struct {
	Str *byte
	Len uintptr
}

// Tier bit flags. Match the C ndec_lookup_tier enum.
const (
	TierNone   uint32 = 0
	TierWindow uint32 = 1 << 0
	TierGperf  uint32 = 1 << 1
	TierHand   uint32 = 1 << 2
	TierTable  uint32 = 1 << 3

	TiersAll     = TierWindow | TierGperf | TierHand | TierTable
	TiersPerfect = TierWindow | TierGperf | TierHand
)

// Error codes returned by Init as negative values.
const (
	ErrNullArg         = -1
	ErrKeysEmpty       = -2
	ErrKeysTooMany     = -3
	ErrKeyEmpty        = -4
	ErrKeyTooLong      = -5
	ErrKeyInvalidByte  = -6
	ErrKeyDuplicate    = -7
	ErrStorageTooSmall = -8
	ErrNoTierMatches   = -9
)

// Config mirrors C ndec_lookup_config.
//
// Scratch/ScratchSize point at a caller-owned build workspace required by the
// gperf/hand tiers (see ScratchSize). It must be non-nil and >= ScratchSize()
// bytes whenever Tiers includes Gperf or Hand; WINDOW/TABLE-only builds may
// leave it nil. The buffer is only used during Init and may be reused across
// calls (never retained by the built lookup).
type Config struct {
	Keys        *Key
	N           uintptr
	Tiers       uint32
	_           [4]byte
	Scratch     unsafe.Pointer
	ScratchSize uintptr
}

// SizeFor returns the storage upper bound for a config, or 0 if invalid.
func SizeFor(cfg *Config) uintptr { return sizeFor(cfg) }

// ScratchSize returns the required size of the build scratch buffer
// (Config.Scratch). Constant for the process; allocate once and reuse.
func ScratchSize() uintptr { return scratchSize() }

// Init builds a lookup into caller-provided storage. On success returns the
// selected tier flag (positive Tier* value); on failure returns a negative
// error code.
func Init(storage unsafe.Pointer, storageSize uintptr, cfg *Config) int32 {
	return lookupInit(storage, storageSize, cfg)
}

// GetTier returns the tier flag stored in an initialized lookup.
func GetTier(storage unsafe.Pointer) uint32 { return getTier(storage) }

// Footprint returns the actual bytes used by an initialized lookup (may be
// less than the SizeFor upper bound).
func Footprint(storage unsafe.Pointer) uintptr { return footprint(storage) }

// TierName resolves a tier flag to its ASCII name (window/gperf/hand/
// table/none). Callers rarely need it; kept for parity with the C API.
func TierName(t uint32) string {
	switch t {
	case TierWindow:
		return "window"
	case TierGperf:
		return "gperf"
	case TierHand:
		return "hand"
	case TierTable:
		return "table"
	default:
		return "none"
	}
}

// Trampolines. Set by per-platform init files.
var (
	sizeFor     func(cfg *Config) uintptr
	scratchSize func() uintptr
	lookupInit  func(storage unsafe.Pointer, storageSize uintptr, cfg *Config) int32
	getTier     func(storage unsafe.Pointer) uint32
	footprint   func(storage unsafe.Pointer) uintptr
)
