package lockfile

//go:generate go run ./internal/cmd/genschema

// Schema returns the embedded JSON Schema document for the supported lockfile
// version. Callers can surface it for editor integration or external
// validation. Parse checks the document's shape — known keys, required fields,
// version — and canonicalizes pin keys, but does not reject entries whose keys
// aren't canonical pins; they're preserved for consumer diagnostics. Note the
// schema's pin pattern constrains pin *values* (the workflow and uses arrays),
// not the dependencies map keys, so schema validation alone won't enforce that
// every dependency key is a canonical pin either.
func Schema() string {
	return schemaV002
}
