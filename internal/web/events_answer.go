package web

import (
	"errors"
	"net/http"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/events"
)

// The texts a page shows for the outcomes of internal/events. They are fixed:
// nothing the person typed ends up in them. A refusal for lack of rights uses
// the same words as the approval flows (approvalsMsgNotAllowed).
const (
	eventsMsgBlocked = "os eventos deste coletivo estão bloqueados pelo admin."
	eventsMsgChanged = "este evento foi alterado por outra pessoa; confira e salve de novo."
)

// eventsAnswer maps an error from an internal/events flow to the status and the
// pt-BR text the page answers with. An unknown event is 404 with no text: the
// caller answers with answerNotFound, like a path no route serves. ok is false
// for any other error, domain.FieldErrors included: the caller shows field
// errors with formPage (422), and logs anything else and answers 500.
func eventsAnswer(err error) (status int, msg string, ok bool) {
	switch {
	case errors.Is(err, events.ErrNotAllowed):
		return http.StatusForbidden, approvalsMsgNotAllowed, true
	case errors.Is(err, events.ErrBlocked):
		return http.StatusConflict, eventsMsgBlocked, true
	case errors.Is(err, events.ErrChanged):
		return http.StatusConflict, eventsMsgChanged, true
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "", true
	}
	return 0, "", false
}
