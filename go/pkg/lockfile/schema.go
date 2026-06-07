package lockfile

//go:generate go run ./internal/cmd/genschema

// Schema returns the embedded JSON Schema document for the supported lockfile
// version. Callers can surface it for editor integration or external
// validation; Parse already enforces it.
func Schema() string {
	return schemaV001
}
