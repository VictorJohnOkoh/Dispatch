package hostset

// UseAdminKeys points the administrators file at a test directory, because the
// real one is a Windows system path.
func UseAdminKeys(path string) func() {
	was := adminKeys
	adminKeys = path
	return func() { adminKeys = was }
}
