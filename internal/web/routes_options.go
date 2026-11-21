package web

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/catalog"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// optionsRoutes registers the <option> lists the dependent selects swap in: GET
// /opcoes/cidades (the cities of a state) and GET /opcoes/subestilos (the
// substyles of a style). They need a signed-in user. The event form builds its own
// city and substyle lists with the same builders and the same select-options
// partial, so what it renders and what these answer can never differ.
func (a *App) optionsRoutes(public, authed *gin.RouterGroup) {
	authed.GET("/opcoes/cidades", a.optionsCities)
	authed.GET("/opcoes/subestilos", a.optionsSubstyles)
}

// optionsCities answers the cities of ?uf=, with ?selecionada= marked.
func (a *App) optionsCities(c *gin.Context) {
	a.optionsAnswer(c, cityOptions(c.Query("uf"), c.Query("selecionada")))
}

// optionsSubstyles answers the substyles of ?estilo=, with ?selecionada= marked.
func (a *App) optionsSubstyles(c *gin.Context) {
	a.optionsAnswer(c, substyleOptions(c.Query("estilo"), c.Query("selecionada")))
}

// optionsAnswer answers the options with no layout, for an HTMX swap. An unknown or
// empty parent is not an error: the list is only its empty option.
func (a *App) optionsAnswer(c *gin.Context, options []calendarOption) {
	c.Header("Cache-Control", "no-store")
	a.Render.Partial(c, http.StatusOK, "select-options", options)
}

// cityOptions is the list of a city select: the cities of the state in catalog
// order, with the one equal to selected marked, after the empty option. With no
// state, or one that is not in the catalog, the list is only "escolha o estado
// primeiro". The values are compared with the catalog and printed by the template,
// never used as a key or in a query.
func cityOptions(state, selected string) []calendarOption {
	cities := catalog.Cities(state)
	if len(cities) == 0 {
		return []calendarOption{{Label: "escolha o estado primeiro"}}
	}
	out := make([]calendarOption, 0, len(cities)+1)
	out = append(out, calendarOption{Label: "escolha a cidade"})
	for _, city := range cities {
		out = append(out, calendarOption{Value: city, Label: city, Selected: city == selected})
	}
	return out
}

// substyleOptions is the list of a substyle select: "sem subestilo", then the
// substyles of the style in catalog order and domain.StyleOther, with the one equal
// to selected marked. A catalog style with no substyles of its own (pop) still
// takes outros; no style, an unknown one or outros is only the empty option.
func substyleOptions(style, selected string) []calendarOption {
	out := []calendarOption{{Label: "sem subestilo"}}
	// Substyles is empty but not nil for a catalog style with none; only an unknown
	// style or outros gives nil.
	subs := catalog.Substyles(style)
	if subs == nil {
		return out
	}
	for _, s := range append(subs, domain.StyleOther) {
		out = append(out, calendarOption{Value: s, Label: s, Selected: s == selected})
	}
	return out
}
