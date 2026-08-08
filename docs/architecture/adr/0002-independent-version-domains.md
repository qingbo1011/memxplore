# ADR 0002: Independent version domains

- Status: Accepted
- Date: 2026-08-08

## Context

The program release, public API, private database layout, and portable exports evolve at different rates. Treating them as one version would either break clients unnecessarily or hide incompatible persistence changes.

## Decision

MemXplore versions four domains independently:

1. Program releases use semantic versions such as `v0.1.0`.
2. Public REST, MCP, CLI contracts, AgentEvent, and Go SDK use protocol `v1`.
3. SQLite migrations use a monotonically increasing storage schema integer.
4. Portable exports use a monotonically increasing export schema integer.

The binary reports all four. Imports validate the export schema before mutation; storage migrations create and verify a backup before applying changes.

## Consequences

Compatibility policies can be stated precisely. Release tooling must record and test every version domain rather than deriving them from a single constant.

