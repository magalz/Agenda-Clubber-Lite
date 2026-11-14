// Package catalog holds the fixed lists events pick from: music styles with
// their substyles, Brazilian states with their cities, and the event type
// labels. The style and city lists come from CircuitoNE and are embedded once,
// here, as the JSON files next to this one: estilos.json (style label to its
// substyle labels, in editorial order) and municipios.json (UF code to its IBGE
// cities, in pt-BR alphabetical order). Both are parsed at start-up; a malformed
// embedded file is a programming error and panics. Every function that returns
// a slice returns a copy, so a caller cannot change the catalog.
package catalog

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

//go:embed estilos.json
var estilosJSON []byte

//go:embed municipios.json
var municipiosJSON []byte

// Style is a music style and its substyles, in file order. A style without
// substyles has an empty, non-nil slice.
type Style struct {
	Name      string
	Substyles []string
}

// State is a Brazilian state: its UF code and its name.
type State struct {
	Code, Name string
}

// styleEntry is a style's substyles in file order, plus the same labels as a
// set so ValidStyle does not scan them.
type styleEntry struct {
	subs []string
	set  map[string]struct{}
}

var (
	styles      []Style
	styleByName map[string]styleEntry
	cities      map[string][]string
	citySet     map[string]map[string]struct{}
	stateNames  map[string]string
)

// states is CircuitoNE's ESTADOS, in its order.
var states = []State{
	{"AC", "Acre"}, {"AL", "Alagoas"}, {"AP", "Amapá"}, {"AM", "Amazonas"}, {"BA", "Bahia"},
	{"CE", "Ceará"}, {"DF", "Distrito Federal"}, {"ES", "Espírito Santo"}, {"GO", "Goiás"},
	{"MA", "Maranhão"}, {"MT", "Mato Grosso"}, {"MS", "Mato Grosso do Sul"}, {"MG", "Minas Gerais"},
	{"PA", "Pará"}, {"PB", "Paraíba"}, {"PR", "Paraná"}, {"PE", "Pernambuco"}, {"PI", "Piauí"},
	{"RJ", "Rio de Janeiro"}, {"RN", "Rio Grande do Norte"}, {"RS", "Rio Grande do Sul"},
	{"RO", "Rondônia"}, {"RR", "Roraima"}, {"SC", "Santa Catarina"}, {"SP", "São Paulo"},
	{"SE", "Sergipe"}, {"TO", "Tocantins"},
}

var eventTypes = []struct {
	t     domain.EventType
	label string
}{
	{domain.EventTypeFesta, "Festa"},
	{domain.EventTypeRave, "Rave"},
	{domain.EventTypeFestival, "Festival"},
	{domain.EventTypeWorkshop, "Workshop"},
	{domain.EventTypeFeira, "Feira"},
	{domain.EventTypeOutros, "Outros"},
}

func init() {
	var err error
	if styles, err = parseStyles(estilosJSON); err != nil {
		panic("catalog: estilos.json: " + err.Error())
	}
	styleByName = make(map[string]styleEntry, len(styles))
	for _, s := range styles {
		set := make(map[string]struct{}, len(s.Substyles))
		for _, sub := range s.Substyles {
			set[sub] = struct{}{}
		}
		styleByName[s.Name] = styleEntry{subs: s.Substyles, set: set}
	}

	if err := json.Unmarshal(municipiosJSON, &cities); err != nil {
		panic("catalog: municipios.json: " + err.Error())
	}
	citySet = make(map[string]map[string]struct{}, len(cities))
	for uf, list := range cities {
		set := make(map[string]struct{}, len(list))
		for _, city := range list {
			set[city] = struct{}{}
		}
		citySet[uf] = set
	}

	stateNames = make(map[string]string, len(states))
	for _, s := range states {
		stateNames[s.Code] = s.Name
	}
}

// parseStyles decodes estilos.json: one object whose keys are the style labels
// and whose values are arrays of substyle labels. A Go map would lose the key
// order, so the keys are read as tokens.
func parseStyles(data []byte) ([]Style, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	var out []Style
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name := tok.(string) // the decoder yields only string keys inside an object
		var subs []string
		if err := dec.Decode(&subs); err != nil {
			return nil, fmt.Errorf("style %q: %w", name, err)
		}
		if subs == nil { // JSON null
			return nil, fmt.Errorf("style %q: not an array", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("style %q: duplicated", name)
		}
		seen[name] = true
		out = append(out, Style{Name: name, Substyles: subs})
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("data after the JSON object")
	}
	return out, nil
}

// Styles returns the music styles in file order, each with its substyles.
func Styles() []Style {
	out := make([]Style, len(styles))
	for i, s := range styles {
		out[i] = Style{Name: s.Name, Substyles: slices.Clone(s.Substyles)}
	}
	return out
}

// ValidStyle reports whether style and sub are an acceptable pair. A catalog
// style takes an empty sub, domain.StyleOther, or one of its own substyles.
// domain.StyleOther as the style takes only an empty sub (the organizer names
// the style in Event.StyleOther). Anything else, including a substyle of another
// style, is false. Matching is exact.
func ValidStyle(style, sub string) bool {
	if style == domain.StyleOther {
		return sub == ""
	}
	entry, ok := styleByName[style]
	if !ok {
		return false
	}
	if sub == "" || sub == domain.StyleOther {
		return true
	}
	_, ok = entry.set[sub]
	return ok
}

// Substyles returns the substyles of a catalog style in file order: empty and
// non-nil when it has none, nil for an unknown style or domain.StyleOther.
func Substyles(style string) []string {
	entry, ok := styleByName[style]
	if !ok {
		return nil
	}
	return slices.Clone(entry.subs)
}

// States returns the 27 UFs in CircuitoNE's order.
func States() []State {
	return slices.Clone(states)
}

// StateName returns the name of the state with that UF code.
func StateName(code string) (string, bool) {
	name, ok := stateNames[code]
	return name, ok
}

// Cities returns the cities of a UF in file order, nil for an unknown UF.
func Cities(uf string) []string {
	return slices.Clone(cities[uf])
}

// ValidCity reports whether city is one of the UF's cities. Matching is exact:
// case and accents matter.
func ValidCity(uf, city string) bool {
	_, ok := citySet[uf][city]
	return ok
}

// Types returns the event types in the order selects show them.
func Types() []domain.EventType {
	out := make([]domain.EventType, len(eventTypes))
	for i, e := range eventTypes {
		out[i] = e.t
	}
	return out
}

// TypeLabel returns the pt-BR label of an event type, empty for an unknown type.
func TypeLabel(t domain.EventType) string {
	for _, e := range eventTypes {
		if e.t == t {
			return e.label
		}
	}
	return ""
}
