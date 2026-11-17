package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/catalog"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/events"
)

// The event form. It is a plain form that posts to itself and works without
// JavaScript: the cities depend on the state and the substyles on the style, so
// the person picks those and presses [↻] ATUALIZAR LISTAS, a second submit button
// (acao=atualizar) that posts the form back with the lists filled in and saves
// nothing. The widgets and cascades later tasks add only enhance this.

// eventFormPage is the Data of app/evento-form and the dot of its event-fields
// partial: where the form posts and cancels to, the event's collective (editing)
// or the collectives to choose from (creating), the options of its selects, and
// the alert a refusal puts above it. AlertHref, set when the edit raced another,
// links to the event as it is now.
type eventFormPage struct {
	Edit             bool
	Action           string
	CancelHref       string
	CollectiveName   string
	Alert, AlertHref string
	Collectives      []calendarOption
	Types            []calendarOption
	States           []calendarOption
	Cities           []calendarOption
	Styles           []calendarOption
	Substyles        []calendarOption
}

// eventFormRoutes registers the event form: GET and POST /eventos/novo (create),
// GET and POST /eventos/:id/editar (edit). They need a signed-in user; the rights
// are those of internal/events.
func (a *App) eventFormRoutes(public, authed *gin.RouterGroup) {
	authed.GET("/eventos/novo", a.eventNewShow)
	authed.POST("/eventos/novo", a.eventNewSave)
	authed.GET("/eventos/:id/editar", a.eventEditShow)
	authed.POST("/eventos/:id/editar", a.eventEditSave)
}

// eventNewShow is the empty form, for an Administrador of an approved collective
// whose events are not blocked. Anyone else gets the 403 of a refused action.
func (a *App) eventNewShow(c *gin.Context) {
	u, ok := a.signedInUser(c, "events")
	if !ok {
		return
	}
	options, ok := a.eventFormCollectives(c, u)
	if !ok {
		return
	}
	a.eventNewRender(c, http.StatusOK, options, events.Form{}, nil, "")
}

// eventNewSave creates the event and redirects to its page. A form with problems
// is shown again with 422 and the person's values, the collective's block with
// 409, and a collective the person may not create in is a plain 403. The refresh
// button re-renders with 200, no errors and nothing stored.
func (a *App) eventNewSave(c *gin.Context) {
	u, ok := a.signedInUser(c, "events")
	if !ok {
		return
	}
	options, ok := a.eventFormCollectives(c, u)
	if !ok {
		return
	}
	in, err := a.parseForm(c)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	f := eventFormFromValues(in)
	if in.Get("acao") == "atualizar" {
		a.eventNewRender(c, http.StatusOK, options, f, nil, "")
		return
	}
	id, err := events.Create(c.Request.Context(), a.Store, u, f, a.Now(), a.Cfg.Location)
	var fe domain.FieldErrors
	status, msg, known := eventsAnswer(err)
	switch {
	case err == nil:
		a.redirect(c, eventPath(id)+"?msg=evento-criado")
	case errors.As(err, &fe):
		a.eventNewRender(c, http.StatusUnprocessableEntity, options, f, fe, "")
	case !known:
		a.Log.Error("create event", "user", u.ID, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
	case status == http.StatusForbidden:
		c.String(status, msg)
	case status == http.StatusNotFound:
		answerNotFound(c)
	default:
		a.eventNewRender(c, status, options, f, nil, msg)
	}
}

// eventEditShow is the form filled with the event, with the version it is
// edited from. Who may not edit the event gets a plain 403 (a 409 when the admin
// blocked the events of the collective they are an Administrador of), and an
// event that is not there the 404 of an unregistered path.
func (a *App) eventEditShow(c *gin.Context) {
	u, ok := a.signedInUser(c, "events")
	if !ok {
		return
	}
	view, ok := a.eventFormEvent(c, u, c.Param("id"))
	if !ok {
		return
	}
	a.eventEditRender(c, view, http.StatusOK, events.FormOf(view.Event, a.Cfg.Location), nil, "", "")
}

// eventEditSave saves the edit and redirects to the event. The answers are those
// of eventNewSave, plus 409 for an edit made from a stale version: the form comes
// back with the person's values, the current version in the hidden field and a
// link to the event as it is now. The refresh button re-renders with 200 for
// someone who may edit the event, and saves nothing.
func (a *App) eventEditSave(c *gin.Context) {
	u, ok := a.signedInUser(c, "events")
	if !ok {
		return
	}
	id := c.Param("id")
	in, err := a.parseForm(c)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	f := eventFormFromValues(in)
	if in.Get("acao") == "atualizar" {
		view, ok := a.eventFormEvent(c, u, id)
		if !ok {
			return
		}
		a.eventEditRender(c, view, http.StatusOK, f, nil, "", "")
		return
	}
	err = events.Update(c.Request.Context(), a.Store, u, id, f, a.Now(), a.Cfg.Location)
	var fe domain.FieldErrors
	status, msg, known := eventsAnswer(err)
	switch {
	case err == nil:
		a.redirect(c, eventPath(id)+"?msg=evento-salvo")
	case errors.As(err, &fe):
		a.eventEditAgain(c, u, id, http.StatusUnprocessableEntity, f, fe, "", false)
	case !known:
		a.Log.Error("update event", "user", u.ID, "event", id, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
	case status == http.StatusForbidden:
		c.String(status, msg)
	case status == http.StatusNotFound:
		answerNotFound(c)
	default:
		a.eventEditAgain(c, u, id, status, f, nil, msg, errors.Is(err, events.ErrChanged))
	}
}

// eventFormCollectives returns the collectives u may create an event in. When
// there are none, it answers 403 and returns false; a failed read is a logged 500.
func (a *App) eventFormCollectives(c *gin.Context, u domain.User) ([]events.CollectiveOption, bool) {
	options, err := events.FormCollectives(c.Request.Context(), a.Store, u)
	switch {
	case err != nil:
		a.Log.Error("event form: collectives", "user", u.ID, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return nil, false
	case len(options) == 0:
		c.String(http.StatusForbidden, approvalsMsgNotAllowed)
		return nil, false
	}
	return options, true
}

// eventFormView reads the event id with u's rights. An event that is not there is
// the 404 of an unregistered path and a failed read a logged 500; both answer and
// return false.
func (a *App) eventFormView(c *gin.Context, u domain.User, id string) (events.DetailView, bool) {
	view, err := events.Detail(c.Request.Context(), a.Store, u, id)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		answerNotFound(c)
		return view, false
	case err != nil:
		a.Log.Error("event form: read event", "user", u.ID, "event", id, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return view, false
	}
	return view, true
}

// eventFormEvent is eventFormView for someone who must be able to edit the event:
// a person who may not gets the plain refusal, and no form is rendered.
func (a *App) eventFormEvent(c *gin.Context, u domain.User, id string) (events.DetailView, bool) {
	view, ok := a.eventFormView(c, u, id)
	if !ok || view.CanEdit {
		return view, ok
	}
	// Only an approved Administrador of a collective whose events are blocked is told
	// why (409); a Membro or a stranger gets the same 403 as for any other event.
	if view.Blocked {
		switch m, err := a.Store.Repos().Memberships.Get(c.Request.Context(), u.ID, view.CollectiveID); {
		case err == nil:
			if m.Status == domain.MembershipStatusApproved && m.Role == domain.RoleOwner {
				c.String(http.StatusConflict, eventsMsgBlocked)
				return view, false
			}
		case !errors.Is(err, domain.ErrNotFound):
			a.Log.Error("event form: read membership", "user", u.ID, "event", id, "err", err)
			c.String(http.StatusInternalServerError, "erro interno")
			return view, false
		}
	}
	c.String(http.StatusForbidden, approvalsMsgNotAllowed)
	return view, false
}

// eventEditAgain shows the edit form again after a refused save, with the event
// read afresh. For an edit that raced another (stale) the hidden version becomes
// the stored one, so saving again is a deliberate choice, and the alert links to
// the event as it is now.
func (a *App) eventEditAgain(c *gin.Context, u domain.User, id string, status int, f events.Form, errs domain.FieldErrors, alert string, stale bool) {
	view, ok := a.eventFormView(c, u, id)
	if !ok {
		return
	}
	href := ""
	if stale {
		f.Version = strconv.FormatInt(view.Version, 10)
		href = eventPath(view.ID)
	}
	a.eventEditRender(c, view, status, f, errs, alert, href)
}

// eventNewRender renders the create form: the collectives to choose from (the
// only one is preselected), f's values, the field errors and the alert.
func (a *App) eventNewRender(c *gin.Context, status int, options []events.CollectiveOption, f events.Form, errs domain.FieldErrors, alert string) {
	page := eventFormPage{Action: "/eventos/novo", CancelHref: "/", Alert: alert}
	for _, o := range options {
		selected := o.ID == f.CollectiveID || (f.CollectiveID == "" && len(options) == 1)
		page.Collectives = append(page.Collectives, calendarOption{Value: o.ID, Label: o.Name, Selected: selected})
	}
	a.eventFormRender(c, status, "Criar Evento", "criar-evento", page, f, errs)
}

// eventEditRender renders the edit form of view with f's values. The collective
// is shown, not chosen: an event never changes it.
func (a *App) eventEditRender(c *gin.Context, view events.DetailView, status int, f events.Form, errs domain.FieldErrors, alert, alertHref string) {
	page := eventFormPage{
		Edit:           true,
		Action:         eventURL(view.ID, "editar"),
		CancelHref:     eventPath(view.ID),
		CollectiveName: view.CollectiveName,
		Alert:          alert,
		AlertHref:      alertHref,
	}
	a.eventFormRender(c, status, "Editar Evento", "calendario", page, f, errs)
}

// eventFormRender fills the lists of page for f's state and style and renders it.
func (a *App) eventFormRender(c *gin.Context, status int, title, nav string, page eventFormPage, f events.Form, errs domain.FieldErrors) {
	page.fillLists(f)
	a.page(c, status, "app/evento-form", PageData{Title: title, Nav: nav, Form: eventFormValues(f), Errors: errs, Data: page})
}

// fillLists sets the options of the selects, with the one f holds selected. The
// cities are those of f's state and the substyles those of its style; with none
// chosen (or an unknown one) each list has only its empty option.
func (p *eventFormPage) fillLists(f events.Form) {
	p.Types = []calendarOption{{Label: "escolha o tipo"}}
	for _, t := range catalog.Types() {
		p.Types = append(p.Types, calendarOption{Value: string(t), Label: catalog.TypeLabel(t), Selected: string(t) == f.Type})
	}

	p.States = []calendarOption{{Label: "escolha o estado"}}
	for _, s := range catalog.States() {
		p.States = append(p.States, calendarOption{Value: s.Code, Label: s.Name + " (" + s.Code + ")", Selected: s.Code == f.State})
	}

	if cities := catalog.Cities(f.State); len(cities) > 0 {
		p.Cities = []calendarOption{{Label: "escolha a cidade"}}
		for _, city := range cities {
			p.Cities = append(p.Cities, calendarOption{Value: city, Label: city, Selected: city == f.City})
		}
	} else {
		p.Cities = []calendarOption{{Label: "escolha o estado primeiro"}}
	}

	p.Styles = []calendarOption{{Label: "escolha o estilo"}}
	for _, s := range catalog.Styles() {
		p.Styles = append(p.Styles, calendarOption{Value: s.Name, Label: s.Name, Selected: s.Name == f.Style})
	}
	p.Styles = append(p.Styles, calendarOption{Value: domain.StyleOther, Label: "Outros", Selected: f.Style == domain.StyleOther})

	p.Substyles = []calendarOption{{Label: "sem subestilo"}}
	// A catalog style with no substyles of its own (pop) still takes outros, and its
	// Substyles is empty but not nil; only an unknown style or outros gives nil.
	if subs := catalog.Substyles(f.Style); subs != nil {
		for _, s := range append(subs, domain.StyleOther) {
			p.Substyles = append(p.Substyles, calendarOption{Value: s, Label: s, Selected: s == f.Substyle})
		}
	}
}

// eventFormFields maps the form's field names to the fields of f that hold their
// text. "free" is a checkbox and is not here.
func eventFormFields(f *events.Form) map[string]*string {
	return map[string]*string{
		"collective_id":    &f.CollectiveID,
		"name":             &f.Name,
		"type":             &f.Type,
		"type_other":       &f.TypeOther,
		"date":             &f.Date,
		"time":             &f.Time,
		"duration_hours":   &f.DurHours,
		"duration_minutes": &f.DurMinutes,
		"price":            &f.Price,
		"ticket_url":       &f.TicketURL,
		"style":            &f.Style,
		"substyle":         &f.Substyle,
		"style_other":      &f.StyleOther,
		"state":            &f.State,
		"city":             &f.City,
		"venue":            &f.Venue,
		"artists":          &f.Artists,
		"description":      &f.Description,
		"version":          &f.Version,
	}
}

// eventFormFromValues reads the posted fields into a form. A checked "free" box
// posts any value, so any non-empty one counts.
func eventFormFromValues(v url.Values) events.Form {
	var f events.Form
	for name, p := range eventFormFields(&f) {
		*p = v.Get(name)
	}
	f.Free = v.Get("free") != ""
	return f
}

// eventFormValues is the inverse of eventFormFromValues: the values the
// templates read the form from.
func eventFormValues(f events.Form) url.Values {
	v := url.Values{}
	for name, p := range eventFormFields(&f) {
		v.Set(name, *p)
	}
	if f.Free {
		v.Set("free", "on")
	}
	return v
}

// eventPath is the path of an event's page. The ID is opaque, so it goes in as one
// escaped path segment.
func eventPath(id string) string {
	return "/eventos/" + url.PathEscape(id)
}
