package evaluation

import (
	"html/template"
	"io"
)

var reportTemplate = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>MemXplore evaluation {{.Manifest.RunID}}</title>
<style>
body{font-family:ui-sans-serif,system-ui,sans-serif;max-width:1100px;margin:0 auto;padding:32px;color:#17202a;background:#fff}h1{font-size:28px;margin:0 0 8px}h2{font-size:20px;margin-top:32px}p,li{line-height:1.5}.meta{color:#59636e}table{border-collapse:collapse;width:100%;font-variant-numeric:tabular-nums}th,td{text-align:right;border-bottom:1px solid #dfe3e7;padding:10px 8px}th:first-child,td:first-child{text-align:left}code{font-family:ui-monospace,monospace;font-size:13px}.pass{color:#08783e}.fail{color:#b42318}</style>
</head>
<body>
<h1>MemXplore evaluation report</h1>
<p class="meta"><code>{{.Manifest.RunID}}</code> · {{.Manifest.Benchmark}} · adapter {{.Manifest.Adapter}}</p>
<p>Dataset <strong>{{.Manifest.Dataset.Name}}</strong> at <code>{{.Manifest.Dataset.Revision}}</code>, SHA-256 <code>{{.Manifest.Dataset.SHA256}}</code>. This report makes no leaderboard claim.</p>
<h2>Retrieval and system metrics</h2>
<table><thead><tr><th>Variant</th><th>Cases</th><th>Hit@K</th><th>Recall@K</th><th>MRR</th><th>Failures</th><th>Mean ms</th><th>P95 ms</th><th>Retrieved tokens</th><th>Calls</th><th>Cost USD</th></tr></thead><tbody>
{{range .Rows}}<tr><td>{{.ID}}</td><td>{{.Metrics.Cases}}</td><td>{{printf "%.4f" .Metrics.HitAtK}}</td><td>{{printf "%.4f" .Metrics.RecallAtK}}</td><td>{{printf "%.4f" .Metrics.MRR}}</td><td>{{.Metrics.Failures}}</td><td>{{printf "%.2f" .Metrics.LatencyMeanMS}}</td><td>{{printf "%.2f" .Metrics.LatencyP95MS}}</td><td>{{.Metrics.RetrievedTokens}}</td><td>{{.Metrics.ProviderCalls}}</td><td>{{printf "%.6f" .Metrics.CostUSD}}</td></tr>{{end}}
</tbody></table>
{{if .Metrics.Ablations}}<h2>Paired ablations</h2><table><thead><tr><th>Pair</th><th>Recall@K delta</th><th>MRR delta</th><th>Mean latency delta ms</th></tr></thead><tbody>{{range .Metrics.Ablations}}<tr><td>{{.Variant}} vs {{.Baseline}}</td><td>{{printf "%+.4f" .RecallAtKDelta}}</td><td>{{printf "%+.4f" .MRRDelta}}</td><td>{{printf "%+.2f" .LatencyMSDelta}}</td></tr>{{end}}</tbody></table>{{end}}
{{if .Checks}}<h2>Lifecycle checks</h2><ul>{{range .Checks}}<li class="{{if .Passed}}pass{{else}}fail{{end}}">{{.Name}}: {{if .Passed}}passed{{else}}failed{{end}}</li>{{end}}</ul>{{end}}
<h2>Run facts</h2><ul><li>Started: {{.Manifest.StartedAt}}</li><li>Completed: {{.Manifest.CompletedAt}}</li><li>Go: {{.Manifest.Runtime.GoVersion}} {{.Manifest.Runtime.GOOS}}/{{.Manifest.Runtime.GOARCH}}</li><li>Indexed units: {{.Metrics.IndexedUnits}}</li><li>Estimated ingest tokens: {{.Metrics.IngestTokens}}</li><li>Ingest latency: {{printf "%.2f" .Metrics.IngestLatencyMS}} ms</li></ul>
{{if .Manifest.Limitations}}<h2>Limitations</h2><ul>{{range .Manifest.Limitations}}<li>{{.}}</li>{{end}}</ul>{{end}}
</body></html>`))

type reportRow struct {
	ID      string
	Metrics VariantMetrics
}

type reportCheck struct {
	Name   string
	Passed bool
}

// RenderReport writes a standalone escaped HTML report.
func RenderReport(writer io.Writer, run Run) error {
	rows := make([]reportRow, 0, len(run.Metrics.Variants))
	for _, id := range sortedMetricVariants(run.Metrics.Variants) {
		rows = append(rows, reportRow{ID: id, Metrics: run.Metrics.Variants[id]})
	}
	checkNames := make([]string, 0, len(run.Metrics.LifecycleChecks))
	for name := range run.Metrics.LifecycleChecks {
		checkNames = append(checkNames, name)
	}
	// The report is rendered from deterministic ordering even though JSON map ordering is already stable.
	sortStrings(checkNames)
	checks := make([]reportCheck, len(checkNames))
	for index, name := range checkNames {
		checks[index] = reportCheck{Name: name, Passed: run.Metrics.LifecycleChecks[name]}
	}
	return reportTemplate.Execute(writer, struct {
		Manifest Manifest
		Metrics  Metrics
		Rows     []reportRow
		Checks   []reportCheck
	}{run.Manifest, run.Metrics, rows, checks})
}

func sortStrings(values []string) {
	for index := 1; index < len(values); index++ {
		for current := index; current > 0 && values[current] < values[current-1]; current-- {
			values[current], values[current-1] = values[current-1], values[current]
		}
	}
}
