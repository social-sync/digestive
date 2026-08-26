package report

import (
	"fmt"
	"html/template"
	"strings"
)

// Format is an output format for a rendered report.
type Format int

const (
	// HTML is a self-contained, styled HTML document.
	HTML Format = iota
	// Markdown is GitHub-flavored Markdown with native pipe tables.
	Markdown
)

// ParseFormat resolves the --format flag value to a Format. It is called before
// any database work so a bad value fails fast.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "html":
		return HTML, nil
	case "markdown", "md":
		return Markdown, nil
	default:
		return 0, fmt.Errorf("unknown format %q: use \"html\" or \"markdown\"", s)
	}
}

// Render turns the report model into a document string in the chosen format.
func Render(rep Report, format Format) string {
	if format == Markdown {
		return renderMarkdown(rep)
	}
	return renderHTML(rep)
}

// htmlTmpl is the standalone HTML document. html/template escapes every field,
// so column names, methods, and constant values are safe to interpolate.
// Not-anonymised rows carry the "leak" class so untouched data stands out.
var htmlTmpl = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Anonymisation Report</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; margin: 2rem; color: #1a1a1a; line-height: 1.5; }
  h1 { font-size: 1.6rem; margin: 0 0 .25rem; }
  h2 { font-size: 1.15rem; margin: 2rem 0 .5rem; }
  .summary { color: #555; margin: 0 0 1rem; }
  table { border-collapse: collapse; width: 100%; max-width: 760px; }
  th, td { border: 1px solid #d0d0d0; padding: .4rem .65rem; text-align: left; }
  th { background: #f5f5f5; font-weight: 600; }
  tr.leak td { background: #fff4f4; }
  .yes { color: #137333; font-weight: 600; }
  .no { color: #c5221f; font-weight: 600; }
  .excluded { color: #8430ce; font-weight: 600; }
</style>
</head>
<body>
<h1>Anonymisation Report</h1>
<p class="summary">{{.Summary}}</p>
{{range .Tables}}<h2>{{.Name}}</h2>
<table>
<thead><tr><th>Column</th><th>Anonymised?</th><th>Method</th></tr></thead>
<tbody>
{{range .Columns}}<tr{{if .NotAnonymised}} class="leak"{{end}}><td>{{.Name}}</td><td class="{{.StateClass}}">{{.StateLabel}}</td><td>{{.MethodLabel}}</td></tr>
{{end}}</tbody>
</table>
{{end}}</body>
</html>
`))

// renderHTML executes the standalone document template against the model.
func renderHTML(rep Report) string {
	var b strings.Builder
	// The template is fixed and the model is plain strings, so execution cannot
	// fail here; a builder write never errors either.
	_ = htmlTmpl.Execute(&b, rep)
	return b.String()
}

// renderMarkdown emits GitHub-flavored Markdown with native pipe tables.
func renderMarkdown(rep Report) string {
	var b strings.Builder
	b.WriteString("# Anonymisation Report\n\n")
	b.WriteString(rep.Summary())
	b.WriteString("\n")
	for _, t := range rep.Tables {
		fmt.Fprintf(&b, "\n## %s\n\n", mdCell(t.Name))
		b.WriteString("| Column | Anonymised? | Method |\n")
		b.WriteString("| --- | --- | --- |\n")
		for _, c := range t.Columns {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", mdCell(c.Name), c.StateLabel(), mdCell(c.MethodLabel()))
		}
	}
	return b.String()
}

// mdCell escapes the one character that would break a Markdown pipe table.
func mdCell(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
