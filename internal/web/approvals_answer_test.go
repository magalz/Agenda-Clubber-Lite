package web

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/magalz/agenda-clubber-lite/internal/approvals"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

func TestApprovalsAnswer(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		msg    string
	}{
		{"not allowed", approvals.ErrNotAllowed, http.StatusForbidden, approvalsMsgNotAllowed},
		{"decided", approvals.ErrDecided, http.StatusConflict, approvalsMsgDecided},
		{"locked", approvals.ErrLocked, http.StatusConflict, approvalsMsgLocked},
		{"master", approvals.ErrMaster, http.StatusConflict, approvalsMsgMaster},
		{"last administrador", approvals.ErrLastOwner, http.StatusConflict, approvalsMsgLastOwner},
		{"self", approvals.ErrSelf, http.StatusConflict, approvalsMsgSelf},
		{"not found", domain.ErrNotFound, http.StatusNotFound, approvalsMsgNotFound},
		{"wrapped not found", fmt.Errorf("approvals: read collective: %w", domain.ErrNotFound), http.StatusNotFound, approvalsMsgNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, msg, ok := approvalsAnswer(tt.err)
			if !ok || status != tt.status || msg != tt.msg {
				t.Errorf("approvalsAnswer = (%d, %q, %v), want (%d, %q, true)", status, msg, ok, tt.status, tt.msg)
			}
			if msg == "" {
				t.Error("empty message")
			}
		})
	}

	for _, err := range []error{errors.New("boom"), domain.FieldErrors{"reason": "x"}, nil} {
		if status, msg, ok := approvalsAnswer(err); ok {
			t.Errorf("approvalsAnswer(%v) = (%d, %q, true), want ok false", err, status, msg)
		}
	}
}
