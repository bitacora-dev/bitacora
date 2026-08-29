// Package blackbox implements ADR-0011's high-frequency recorder: a
// preallocated ring buffer, sampled at 1 Hz, backed by a memory-mapped
// file flushed every 5 s — the only data source fine-grained enough to
// have caught the hard-hang incident that motivated this ADR, since a
// journal-based system loses exactly the seconds that matter.
package blackbox

// Bounds on the fixed-size arrays inside Sample. Chosen generously above
// real hardware (the i9-13900K that motivated this ADR has 32 logical
// CPUs) while keeping Sample a fixed size — no allocation on the sampling
// hot path, no slices, no strings.
const (
	MaxCPUs         = 64
	MaxSensors      = 24
	MaxBlockDevices = 16
)

// Sample is one second of the metrics ADR-0011 lists as "lo que se puede
// leer barato". Every field is fixed-size so the whole struct encodes to
// a constant number of bytes via encoding/binary — no reflection tricks,
// no variable-length records, and thus no risk of a partially-written
// record corrupting the ring's layout.
type Sample struct {
	TimestampUnixMilli int64

	NumCPUs       uint16
	_             [6]byte          // padding for explicit, stable field alignment
	CPUBusyPct    [MaxCPUs]float32 // 0-100, busy = not idle, not iowait
	CPUFreqMHz    [MaxCPUs]uint32
	ThrottledMask uint64 // bit i set = logical CPU i reporting thermal throttling

	NumSensors       uint16
	_                [6]byte
	SensorTempMilliC [MaxSensors]int32 // hwmon convention: millidegrees C

	MemTotalKB     uint64
	MemAvailableKB uint64
	MemCachedKB    uint64
	MemSwapUsedKB  uint64
	MemDirtyKB     uint64
	MemWritebackKB uint64

	LoadAvg1      float32
	ProcsRunnable uint32
	ProcsBlockedD uint32 // processes in D state (uninterruptible I/O sleep)

	InterruptsPerCPU [MaxCPUs]uint64 // delta since the previous sample

	// PSI (Pressure Stall Information, /proc/pressure/*): the avg10 field
	// from each resource's "some"/"full" line — ADR-0011 calls this out
	// specifically as "el indicador más temprano de degradación
	// disponible en Linux".
	PSICPUSome10 float32
	PSIMemSome10 float32
	PSIMemFull10 float32
	PSIIOSome10  float32
	PSIIOFull10  float32

	NumBlockDevices uint16
	_               [6]byte
	BlockQueueDepth [MaxBlockDevices]uint32 // in-flight I/O count
	BlockLatencyUs  [MaxBlockDevices]uint32 // average I/O completion latency

	EDACCorrectableDelta   uint64 // ECC corrected-error count, delta since previous sample
	EDACUncorrectableDelta uint64
}
