// Shared fact model for the codetospec SCIP converter (protocol facts v1).
package main

// Ref pins a fact to a file location, relative to the index root.
type Ref struct {
	Path  string `json:"path"`
	Lines string `json:"lines"`
}

// Fact is one mechanically provable statement about the codebase.
type Fact struct {
	Kind      string            `json:"kind"`
	ID        string            `json:"id"`
	Attrs     map[string]string `json:"attrs"`
	Source    Ref               `json:"source"`
	Origin    string            `json:"origin"`
	Certainty string            `json:"certainty"`
}

// Envelope is the JSON payload written to stdout.
type Envelope struct {
	Schema string `json:"schema"`
	Facts  []Fact `json:"facts"`
}

const (
	schemaID = "codetospec/facts/v1"
	origin   = "scip"
	certain  = "proved" // an indexer resolved these locations exactly
)
