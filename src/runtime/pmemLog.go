package runtime

// logEntry is the structure used to store one log entry.
type logEntry struct {
	// Offset of the address to be logged from the arena map address
	off uintptr
	// The value to be logged
	val int
}

