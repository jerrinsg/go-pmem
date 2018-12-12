package runtime

// logEntry is the structure used to store one log entry.
type logEntry struct {
	ptr uintptr
	val uintptr
}
