# Third-Party Notices

MemXplore is licensed under Apache-2.0. The following unmodified Go modules are used by v0.1 builds. Related modules with the same version and license are grouped where practical:

| Package | Version | License |
| --- | --- | --- |
| go.opentelemetry.io/otel, metric, trace, sdk, sdk/metric | v1.44.0 | Apache-2.0 |
| go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp | v1.44.0 | Apache-2.0 |
| go.opentelemetry.io/otel/exporters/otlp/otlptrace, otlptracehttp | v1.44.0 | Apache-2.0 |
| go.opentelemetry.io/auto/sdk | v1.2.1 | Apache-2.0 |
| go.opentelemetry.io/proto/otlp | v1.10.0 | Apache-2.0 |
| github.com/cenkalti/backoff/v5 | v5.0.3 | MIT |
| github.com/cespare/xxhash/v2 | v2.3.0 | MIT |
| github.com/go-logr/logr | v1.4.3 | Apache-2.0 |
| github.com/go-logr/stdr | v1.2.2 | Apache-2.0 |
| github.com/grpc-ecosystem/grpc-gateway/v2 | v2.29.0 | BSD-3-Clause |
| google.golang.org/grpc | v1.81.1 | Apache-2.0 |
| google.golang.org/protobuf | v1.36.11 | BSD-3-Clause |
| google.golang.org/genproto/googleapis/api, rpc | 3dc84a4a5aaa | Apache-2.0 |
| golang.org/x/net | v0.55.0 | BSD-3-Clause |
| golang.org/x/text | v0.37.0 | BSD-3-Clause |
| modernc.org/sqlite | v1.56.0 | BSD-3-Clause |
| modernc.org/libc | v1.74.4 | BSD-3-Clause; upstream includes additional third-party notices |
| modernc.org/mathutil | v1.7.1 | BSD-3-Clause |
| modernc.org/memory | v1.11.0 | BSD-3-Clause; upstream includes mmap-go and Go notices |
| golang.org/x/sys | v0.47.0 | BSD-3-Clause |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause |
| github.com/remyoudompheng/bigfft | 24d4a6f8daec | BSD-3-Clause |
| github.com/mattn/go-isatty | v0.0.24 | MIT |
| github.com/ncruces/go-strftime | v1.0.0 | MIT |
| github.com/dustin/go-humanize | v1.0.1 | MIT |

Exact dependency checksums are recorded in `go.sum`; the complete license texts and any transitive notices remain in the upstream modules and source distributions.

No third-party model weights or benchmark datasets are distributed by this repository. The cleaned LongMemEval v1 data is MIT-licensed, and LongMemEval v2 is Apache-2.0-licensed at the revisions documented in `docs/evaluation.md`. Local Ollama models and external benchmark data remain subject to their own upstream terms and are not project artifacts.
