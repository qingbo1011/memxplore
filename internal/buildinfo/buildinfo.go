// Package buildinfo exposes the independently versioned MemXplore surfaces.
package buildinfo

const (
	// ProtocolVersion versions the public REST, MCP, CLI contract, and Go SDK.
	ProtocolVersion = "v1"

	// StorageSchemaVersion versions the daemon-owned SQLite schema.
	StorageSchemaVersion = 1

	// ExportSchemaVersion versions portable export and import documents.
	ExportSchemaVersion = 1
)

// Version is replaced at release build time with -ldflags.
var Version = "0.1.0-dev"
