// Package emailtmpl renders the subject, plain-text, and HTML parts of every
// notification email BillPiggy sends.
package emailtmpl

import (
	"bytes"
	"fmt"
	htmltemplate "html/template"
	texttemplate "text/template"
)

// Rendered holds every part a multipart email needs.
type Rendered struct {
	Subject string
	Text    string
	HTML    string
}

var (
	parsedSubjects = map[string]*texttemplate.Template{}
	parsedText     = map[string]*texttemplate.Template{}
	parsedHTML     = map[string]*htmltemplate.Template{}
)

func init() {
	// missingkey=zero: a payload key a template references but the caller
	// didn't set renders as an empty string. The default behaviour prints
	// the literal "<no value>" for any absent map key, which would otherwise
	// leak into every email whose payload doesn't happen to set every field
	// every template might reference.
	for kind, source := range subjects {
		parsedSubjects[kind] = texttemplate.Must(texttemplate.New(kind + ".subject").Option("missingkey=zero").Parse(source))
	}
	for kind, source := range textBodies {
		parsedText[kind] = texttemplate.Must(texttemplate.New(kind + ".text").Option("missingkey=zero").Parse(source))
	}
	for kind, source := range htmlBodies {
		parsedHTML[kind] = htmltemplate.Must(htmltemplate.New(kind + ".html").Option("missingkey=zero").Parse(source))
	}
}

// Render produces the subject, text, and HTML parts for kind from payload.
// A payload key the template does not reference is ignored; a key the
// template references but payload lacks renders as an empty string, since
// map indexing has no concept of a required field in either template
// package.
func Render(kind string, payload map[string]string) (Rendered, error) {
	subjectTemplate, ok := parsedSubjects[kind]
	if !ok {
		return Rendered{}, fmt.Errorf("no email template registered for kind %q", kind)
	}
	textTemplate, htmlTemplate := parsedText[kind], parsedHTML[kind]

	var subjectBuf, textBuf, htmlBuf bytes.Buffer
	if err := subjectTemplate.Execute(&subjectBuf, payload); err != nil {
		return Rendered{}, fmt.Errorf("render %s subject: %w", kind, err)
	}
	if err := textTemplate.Execute(&textBuf, payload); err != nil {
		return Rendered{}, fmt.Errorf("render %s text body: %w", kind, err)
	}
	if err := htmlTemplate.Execute(&htmlBuf, payload); err != nil {
		return Rendered{}, fmt.Errorf("render %s html body: %w", kind, err)
	}
	return Rendered{Subject: subjectBuf.String(), Text: textBuf.String(), HTML: htmlBuf.String()}, nil
}
