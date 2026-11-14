// Package catalog holds the fixed lists events pick from: music styles, Brazilian
// states and cities, and event type labels. This is the RED stub; see the next
// commit for the implementation.
package catalog

import "github.com/magalz/agenda-clubber-lite/internal/domain"

var (
	estilosJSON    []byte
	municipiosJSON []byte
	cities         map[string][]string
)

type Style struct {
	Name      string
	Substyles []string
}

type State struct {
	Code, Name string
}

func parseStyles(data []byte) ([]Style, error) { return nil, nil }
func Styles() []Style                          { return nil }
func ValidStyle(style, sub string) bool        { return false }
func Substyles(style string) []string          { return nil }
func States() []State                          { return nil }
func StateName(code string) (string, bool)     { return "", false }
func Cities(uf string) []string                { return nil }
func ValidCity(uf, city string) bool           { return false }
func TypeLabel(t domain.EventType) string      { return "" }
func Types() []domain.EventType                { return nil }
