# Contributing

MemXplore accepts focused changes that improve research coverage, correctness, reproducibility, security, or learning value.

## Development

Go 1.24 or newer is required.

```sh
make check
```

Keep domain and application packages independent of SQLite, transports, MCP, Ollama, and vendor SDKs. Add tests with schema, implementation, or behavior changes in the same logical commit where practical.

## Research claims

New strategies must update the machine-readable research catalog and select the fidelity label defined by [ADR 0003](docs/architecture/adr/0003-research-fidelity-labels.md). Do not use `reproduction` unless protocol and result evidence are both present. Include fixed fixtures, seeds, metrics, failure examples, and known deviations.

Do not commit model weights, large datasets, secrets, third-party run directories, or restricted data.

## Git discipline

- Use Conventional Commits, for example `feat(storage): add atomic observation enqueue`.
- Keep each verified logical increment in its own commit.
- Do not rewrite pushed `main` history or force-push it.
- Run the relevant unit, integration, contract, security, or benchmark checks before pushing.

