package catalog

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// Git blob ids of the CircuitoNE files the embedded copies were taken from.
const (
	estilosBlob    = "6f42c354f85919f1f61d03b1129b09133b9bb3c8"
	municipiosBlob = "c9c093178c14e834362f0ee8a2edc3ef9b27d544"
)

// gitBlobID is `git hash-object` of data: SHA-1 of "blob <len>\x00" + data.
func gitBlobID(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func TestEmbeddedFilesMatchPinnedBlobs(t *testing.T) {
	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"estilos.json", estilosJSON, estilosBlob},
		{"municipios.json", municipiosJSON, municipiosBlob},
	} {
		if got := gitBlobID(c.data); got != c.want {
			t.Errorf("%s: git blob id = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestStylesCounts(t *testing.T) {
	styles := Styles()
	subs, empty := 0, 0
	for _, s := range styles {
		subs += len(s.Substyles)
		if len(s.Substyles) == 0 {
			empty++
		}
	}
	if len(styles) != 51 || subs != 179 || empty != 20 {
		t.Errorf("styles=%d substyles=%d without substyles=%d, want 51, 179, 20", len(styles), subs, empty)
	}
}

func TestStylesFileOrder(t *testing.T) {
	styles := Styles()
	if len(styles) < 3 {
		t.Fatalf("got %d styles", len(styles))
	}
	if styles[0].Name != "techno" || styles[1].Name != "house" {
		t.Errorf("first styles = %q, %q, want techno, house", styles[0].Name, styles[1].Name)
	}
	if got := styles[0].Substyles; len(got) == 0 || got[0] != "acid techno" || got[len(got)-1] != "raw techno" {
		t.Errorf("techno substyles = %v, want acid techno first and raw techno last", got)
	}
	if last := styles[len(styles)-1]; last.Name != "áudio não musical" || len(last.Substyles) != 0 {
		t.Errorf("last style = %+v, want áudio não musical without substyles", last)
	}
}

func TestStyleLabels(t *testing.T) {
	styleSeen := map[string]bool{}
	for _, s := range Styles() {
		if s.Name == "" || s.Name != strings.TrimSpace(s.Name) {
			t.Errorf("style %q is empty or has surrounding spaces", s.Name)
		}
		if styleSeen[s.Name] {
			t.Errorf("style %q is duplicated", s.Name)
		}
		styleSeen[s.Name] = true
		subSeen := map[string]bool{}
		for _, label := range append([]string{s.Name}, s.Substyles...) {
			if label == domain.StyleOther {
				t.Errorf("label %q under %q equals %q", label, s.Name, domain.StyleOther)
			}
			if strings.Contains(label, "|") {
				t.Errorf("label %q under %q contains |", label, s.Name)
			}
		}
		for _, sub := range s.Substyles {
			if sub == "" || sub != strings.TrimSpace(sub) {
				t.Errorf("substyle %q of %q is empty or has surrounding spaces", sub, s.Name)
			}
			if subSeen[sub] {
				t.Errorf("substyle %q of %q is duplicated", sub, s.Name)
			}
			subSeen[sub] = true
		}
	}
}

func TestValidStyle(t *testing.T) {
	for _, c := range []struct {
		name       string
		style, sub string
		want       bool
	}{
		{"catalog pair", "techno", "acid techno", true},
		{"catalog pair, last substyle", "techno", "raw techno", true},
		{"style alone", "techno", "", true},
		{"style with outros sub", "techno", domain.StyleOther, true},
		{"style without substyles alone", "pop", "", true},
		{"style without substyles with outros sub", "pop", domain.StyleOther, true},
		{"style without substyles with a sub", "pop", "acid techno", false},
		{"style outros alone", domain.StyleOther, "", true},
		{"style outros with a sub", domain.StyleOther, "acid techno", false},
		{"style outros with outros sub", domain.StyleOther, domain.StyleOther, false},
		{"unknown style", "polka", "", false},
		{"unknown style with a sub", "polka", "acid techno", false},
		{"empty style", "", "", false},
		{"empty style with a sub", "", "acid techno", false},
		{"substyle of another style", "house", "acid techno", false},
		{"unknown sub", "techno", "polka", false},
		{"style in upper case", "TECHNO", "", false},
		{"sub in upper case", "techno", "ACID TECHNO", false},
		{"style without accents", "musica religiosa", "", false},
		{"style with accents", "música religiosa", "", true},
		{"style with a trailing space", "techno ", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidStyle(c.style, c.sub); got != c.want {
				t.Errorf("ValidStyle(%q, %q) = %v, want %v", c.style, c.sub, got, c.want)
			}
		})
	}
}

func TestSubstyles(t *testing.T) {
	got := Substyles("techno")
	if len(got) != 11 || got[0] != "acid techno" || got[10] != "raw techno" {
		t.Errorf("Substyles(techno) = %v", got)
	}
	if got := Substyles("pop"); got == nil || len(got) != 0 {
		t.Errorf("Substyles(pop) = %#v, want an empty, non-nil slice", got)
	}
	for _, style := range []string{domain.StyleOther, "polka", ""} {
		if got := Substyles(style); got != nil {
			t.Errorf("Substyles(%q) = %v, want nil", style, got)
		}
	}
}

var wantStates = []State{
	{"AC", "Acre"}, {"AL", "Alagoas"}, {"AP", "Amapá"}, {"AM", "Amazonas"}, {"BA", "Bahia"},
	{"CE", "Ceará"}, {"DF", "Distrito Federal"}, {"ES", "Espírito Santo"}, {"GO", "Goiás"},
	{"MA", "Maranhão"}, {"MT", "Mato Grosso"}, {"MS", "Mato Grosso do Sul"}, {"MG", "Minas Gerais"},
	{"PA", "Pará"}, {"PB", "Paraíba"}, {"PR", "Paraná"}, {"PE", "Pernambuco"}, {"PI", "Piauí"},
	{"RJ", "Rio de Janeiro"}, {"RN", "Rio Grande do Norte"}, {"RS", "Rio Grande do Sul"},
	{"RO", "Rondônia"}, {"RR", "Roraima"}, {"SC", "Santa Catarina"}, {"SP", "São Paulo"},
	{"SE", "Sergipe"}, {"TO", "Tocantins"},
}

func TestStates(t *testing.T) {
	got := States()
	if len(got) != 27 {
		t.Fatalf("got %d states, want 27", len(got))
	}
	if !slices.Equal(got, wantStates) {
		t.Errorf("States() = %v, want %v", got, wantStates)
	}
}

func TestStateName(t *testing.T) {
	for _, c := range []struct {
		code, name string
		ok         bool
	}{
		{"CE", "Ceará", true},
		{"SP", "São Paulo", true},
		{"DF", "Distrito Federal", true},
		{"ce", "", false},
		{"XX", "", false},
		{"", "", false},
	} {
		name, ok := StateName(c.code)
		if name != c.name || ok != c.ok {
			t.Errorf("StateName(%q) = %q, %v, want %q, %v", c.code, name, ok, c.name, c.ok)
		}
	}
}

func TestCities(t *testing.T) {
	total := 0
	for _, s := range States() {
		cities := Cities(s.Code)
		if len(cities) == 0 {
			t.Errorf("Cities(%s) is empty", s.Code)
		}
		total += len(cities)
	}
	if total != 5571 {
		t.Errorf("total cities = %d, want 5571", total)
	}
	ce := Cities("CE")
	if len(ce) != 184 {
		t.Errorf("CE has %d cities, want 184", len(ce))
	}
	if !slices.Contains(ce, "Fortaleza") {
		t.Error("Fortaleza is not a CE city")
	}
	if len(ce) > 1 && (ce[0] != "Abaiara" || ce[1] != "Acarape") {
		t.Errorf("CE starts with %q, %q, want Abaiara, Acarape (file order)", ce[0], ce[1])
	}
	if ac := Cities("AC"); len(ac) == 0 || ac[0] != "Acrelândia" || ac[len(ac)-1] != "Xapuri" {
		t.Errorf("AC = %v, want Acrelândia first and Xapuri last", ac)
	}
	for _, uf := range []string{"XX", "ce", ""} {
		if got := Cities(uf); got != nil {
			t.Errorf("Cities(%q) = %v, want nil", uf, got)
		}
	}
}

func TestCitiesCoverExactlyTheStates(t *testing.T) {
	if len(cities) != len(States()) {
		t.Errorf("municipios.json has %d UFs, want %d", len(cities), len(States()))
	}
	for uf := range cities {
		if _, ok := StateName(uf); !ok {
			t.Errorf("municipios.json has the unknown UF %q", uf)
		}
	}
}

func TestValidCity(t *testing.T) {
	for _, c := range []struct {
		name, uf, city string
		want           bool
	}{
		{"exact", "CE", "Fortaleza", true},
		{"accents", "AC", "Acrelândia", true},
		{"same name in another UF", "SP", "Fortaleza", false},
		{"city of another UF", "CE", "São Paulo", false},
		{"wrong case", "CE", "fortaleza", false},
		{"upper case", "CE", "FORTALEZA", false},
		{"without accents", "AC", "Acrelandia", false},
		{"trailing space", "CE", "Fortaleza ", false},
		{"unknown UF", "XX", "Fortaleza", false},
		{"lower-case UF", "ce", "Fortaleza", false},
		{"empty city", "CE", "", false},
		{"empty UF", "", "Fortaleza", false},
		{"both empty", "", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidCity(c.uf, c.city); got != c.want {
				t.Errorf("ValidCity(%q, %q) = %v, want %v", c.uf, c.city, got, c.want)
			}
		})
	}
}

func TestEveryCityIsValid(t *testing.T) {
	for _, s := range States() {
		for _, city := range Cities(s.Code) {
			if !ValidCity(s.Code, city) {
				t.Fatalf("ValidCity(%q, %q) = false for a catalog city", s.Code, city)
			}
		}
	}
}

func TestEveryStylePairIsValid(t *testing.T) {
	for _, s := range Styles() {
		if !ValidStyle(s.Name, "") || !ValidStyle(s.Name, domain.StyleOther) {
			t.Errorf("style %q is not valid alone or with %q", s.Name, domain.StyleOther)
		}
		for _, sub := range s.Substyles {
			if !ValidStyle(s.Name, sub) {
				t.Errorf("ValidStyle(%q, %q) = false for a catalog pair", s.Name, sub)
			}
		}
	}
}

func TestReturnedSlicesAreCopies(t *testing.T) {
	t.Run("Styles", func(t *testing.T) {
		got := Styles()
		if len(got) < 2 || len(got[0].Substyles) == 0 {
			t.Fatalf("Styles() = %v", got)
		}
		got[0].Name = "changed"
		got[0].Substyles[0] = "changed"
		got[1] = Style{}
		again := Styles()
		if again[0].Name != "techno" || again[0].Substyles[0] != "acid techno" || again[1].Name != "house" {
			t.Errorf("Styles() kept a caller's change: %q, %q, %q", again[0].Name, again[0].Substyles[0], again[1].Name)
		}
		if !ValidStyle("techno", "acid techno") || ValidStyle("changed", "") {
			t.Error("a caller's change reached ValidStyle")
		}
	})
	t.Run("Substyles", func(t *testing.T) {
		got := Substyles("techno")
		if len(got) == 0 {
			t.Fatal("Substyles(techno) is empty")
		}
		got[0] = "changed"
		if again := Substyles("techno"); again[0] != "acid techno" {
			t.Errorf("Substyles kept a caller's change: %q", again[0])
		}
		if !ValidStyle("techno", "acid techno") || ValidStyle("techno", "changed") {
			t.Error("a caller's change reached ValidStyle")
		}
	})
	t.Run("States", func(t *testing.T) {
		got := States()
		if len(got) == 0 {
			t.Fatal("States() is empty")
		}
		got[0] = State{"ZZ", "changed"}
		if again := States(); again[0] != (State{"AC", "Acre"}) {
			t.Errorf("States kept a caller's change: %v", again[0])
		}
		if name, ok := StateName("AC"); name != "Acre" || !ok {
			t.Errorf("StateName(AC) = %q, %v after a caller's change", name, ok)
		}
	})
	t.Run("Cities", func(t *testing.T) {
		got := Cities("CE")
		if len(got) == 0 {
			t.Fatal("Cities(CE) is empty")
		}
		got[0] = "changed"
		if again := Cities("CE"); again[0] != "Abaiara" {
			t.Errorf("Cities kept a caller's change: %q", again[0])
		}
		if !ValidCity("CE", "Abaiara") || ValidCity("CE", "changed") {
			t.Error("a caller's change reached ValidCity")
		}
	})
	t.Run("Types", func(t *testing.T) {
		got := Types()
		if len(got) == 0 {
			t.Fatal("Types() is empty")
		}
		got[0] = domain.EventType("changed")
		if again := Types(); again[0] != domain.EventTypeFesta {
			t.Errorf("Types kept a caller's change: %q", again[0])
		}
		if TypeLabel(domain.EventTypeFesta) != "Festa" {
			t.Error("a caller's change reached TypeLabel")
		}
	})
}

func TestTypeLabelAndTypes(t *testing.T) {
	want := []struct {
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
	types := Types()
	if len(types) != len(want) {
		t.Fatalf("Types() has %d entries, want %d", len(types), len(want))
	}
	for i, w := range want {
		if types[i] != w.t {
			t.Errorf("Types()[%d] = %q, want %q", i, types[i], w.t)
		}
		if got := TypeLabel(w.t); got != w.label {
			t.Errorf("TypeLabel(%q) = %q, want %q", w.t, got, w.label)
		}
	}
	for _, unknown := range []domain.EventType{"", "show", "FESTA"} {
		if got := TypeLabel(unknown); got != "" {
			t.Errorf("TypeLabel(%q) = %q, want an empty label", unknown, got)
		}
	}
}

func TestParseStylesRejectsMalformedData(t *testing.T) {
	good := `{"a":["x","y"],"b":[]}`
	styles, err := parseStyles([]byte(good))
	if err != nil {
		t.Fatalf("parseStyles(%s): %v", good, err)
	}
	if len(styles) != 2 || styles[0].Name != "a" || !slices.Equal(styles[0].Substyles, []string{"x", "y"}) ||
		styles[1].Name != "b" || styles[1].Substyles == nil || len(styles[1].Substyles) != 0 {
		t.Errorf("parseStyles(%s) = %#v", good, styles)
	}
	for _, bad := range []string{
		``,
		`[]`,
		`{`,
		`{"a":["x"]`,
		`{"a":"x"}`,
		`{"a":null}`,
		`{"a":[1]}`,
		`{"a":["x"],"a":["y"]}`,
		`{"a":[]} {}`,
		`{"a":[]}x`,
	} {
		if styles, err := parseStyles([]byte(bad)); err == nil {
			t.Errorf("parseStyles(%q) = %v, want an error", bad, styles)
		}
	}
}
