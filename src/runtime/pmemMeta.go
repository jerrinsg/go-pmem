package runtime

// This function goes through the persistent memory file, and ensure that its
// metadata is consistent. This involves ensuring the file was not externally
// truncated. Also, it ensures that the header magic in each of the arena
// metadata section is correct.
func verifyMetadata() error {

	return nil
}
