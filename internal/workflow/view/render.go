package view

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed templates/*.md.tmpl
var tmplFS embed.FS

// templates is the lazily-parsed template set. Each phase has its own
// `<lower-phase>.md.tmpl` file under templates/.
var tmpls *template.Template

func loadTemplates() (*template.Template, error) {
	if tmpls != nil {
		return tmpls, nil
	}
	t := template.New("get-step").Funcs(template.FuncMap{
		"join":     func(s []string, sep string) string { return strings.Join(s, sep) },
		"asString": func(v any) string { return fmt.Sprintf("%v", v) },
		"truncate": func(n int, s string) string {
			if len(s) <= n {
				return s
			}
			return s[:n] + "…"
		},
		"json": MustMarshal,
		"str":  func(v any) string { s, _ := v.(string); return s },
	})
	parsed, err := t.ParseFS(tmplFS, "templates/*.md.tmpl")
	if err != nil {
		return nil, err
	}
	tmpls = parsed
	return tmpls, nil
}

// RenderMarkdown picks the right template per phase and renders the view.
func RenderMarkdown(v StepView) (string, error) {
	t, err := loadTemplates()
	if err != nil {
		return "", err
	}
	name := templateName(v.Phase)
	tmpl := t.Lookup(name)
	if tmpl == nil {
		// Fallback to a generic template so unknown phases still render
		// something useful.
		tmpl = t.Lookup("generic.md.tmpl")
		if tmpl == nil {
			return "", fmt.Errorf("template %q not found", name)
		}
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, v); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func templateName(phase string) string {
	switch phase {
	case "PRD":
		return "prd.md.tmpl"
	case "TASKS":
		return "tasks.md.tmpl"
	case "TASK":
		return "task.md.tmpl"
	case "BRAINSTORMING":
		return "brainstorming.md.tmpl"
	case "CODE_REVIEW":
		return "code_review.md.tmpl"
	case "RECEIVING_CODE_REVIEW":
		return "receiving_code_review.md.tmpl"
	case "WRITE_TESTS":
		return "write_tests.md.tmpl"
	case "UPDATE_DOCS":
		return "update_docs.md.tmpl"
	case "FEATURE_ACCEPTANCE":
		return "feature_acceptance.md.tmpl"
	case "COMMIT":
		return "commit.md.tmpl"
	case "ORIGINAL_REQUEST":
		return "original_request.md.tmpl"
	}
	return "generic.md.tmpl"
}
