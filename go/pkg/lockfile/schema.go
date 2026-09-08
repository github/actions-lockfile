package lockfile

//go:generate go run ./internal/cmd/genschema

// Schema returns the embedded JSON Schema document for the latest lockfile
// version (v0.0.3). Callers can surface it for editor integration or external
// validation.
func Schema() string {
	return schemaV003
}

// SchemaForVersion returns the embedded JSON Schema for a specific lockfile
// version. Returns ("", false) for unknown versions.
func SchemaForVersion(version string) (string, bool) {
	switch version {
	case "v0.0.1":
		return schemaV001, true
	case "v0.0.2":
		return schemaV002, true
	case "v0.0.3":
		return schemaV003, true
	default:
		return "", false
	}
}
