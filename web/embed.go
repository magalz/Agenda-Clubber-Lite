// Package web embeds the files the application serves as they are: the
// static assets (CSS, JavaScript, fonts, favicon) and the HTML templates.
package web

import "embed"

// Static holds web/static: css/, js/ (app.js and vendor/), fonts/ and
// favicon.svg. Paths inside it start with "static/".
//
//go:embed static
var Static embed.FS

// Templates holds web/templates: layouts/, partials/ and pages/. Paths inside
// it start with "templates/".
//
//go:embed templates
var Templates embed.FS
