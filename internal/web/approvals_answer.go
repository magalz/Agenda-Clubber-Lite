package web

import (
	"errors"
	"net/http"

	"github.com/magalz/agenda-clubber-lite/internal/approvals"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The texts a page shows for the outcomes of internal/approvals. They are fixed:
// nothing the person typed ends up in them.
const (
	approvalsMsgNotAllowed = "você não pode fazer isso."
	approvalsMsgDecided    = "isso já foi decidido; a lista foi atualizada."
	approvalsMsgLocked     = "este coletivo está congelado pelo admin."
	approvalsMsgMaster     = "o master do coletivo não pode ser rebaixado."
	approvalsMsgLastOwner  = "o coletivo precisa de pelo menos um administrador."
	approvalsMsgSelf       = "você não pode bloquear a própria conta."
	approvalsMsgNotFound   = "não encontrado."
)

// approvalsAnswer maps an error from an internal/approvals flow to the status
// and the pt-BR text the page answers with. ok is false for any other error,
// domain.FieldErrors included: the caller shows field errors with formPage
// (422), and logs anything else and answers 500.
func approvalsAnswer(err error) (status int, msg string, ok bool) {
	switch {
	case errors.Is(err, approvals.ErrNotAllowed):
		return http.StatusForbidden, approvalsMsgNotAllowed, true
	case errors.Is(err, approvals.ErrDecided):
		return http.StatusConflict, approvalsMsgDecided, true
	case errors.Is(err, approvals.ErrLocked):
		return http.StatusConflict, approvalsMsgLocked, true
	case errors.Is(err, approvals.ErrMaster):
		return http.StatusConflict, approvalsMsgMaster, true
	case errors.Is(err, approvals.ErrLastOwner):
		return http.StatusConflict, approvalsMsgLastOwner, true
	case errors.Is(err, approvals.ErrSelf):
		return http.StatusConflict, approvalsMsgSelf, true
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, approvalsMsgNotFound, true
	}
	return 0, "", false
}
