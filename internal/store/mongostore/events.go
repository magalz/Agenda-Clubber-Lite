package mongostore

import (
	"context"
	"errors"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

type events struct{ s *Store }

var _ domain.Events = events{}

// errNotImplemented is what every method of events returns until the task that
// writes the repository replaces the stub. The collection, its validator and its
// indexes already exist (schema.go).
var errNotImplemented = errors.New("mongostore: events: not implemented")

func (events) Create(context.Context, *domain.Event) error { return errNotImplemented }

func (events) ByID(context.Context, string) (domain.EventView, error) {
	return domain.EventView{}, errNotImplemented
}

func (events) Update(context.Context, domain.Event) error { return errNotImplemented }

func (events) Delete(context.Context, string) error { return errNotImplemented }

func (events) ListBetween(context.Context, domain.EventFilter, int) ([]domain.EventView, error) {
	return nil, errNotImplemented
}

func (events) ListByCollectives(context.Context, []string, int) ([]domain.EventView, error) {
	return nil, errNotImplemented
}

func (events) ListPlaces(context.Context) ([]domain.Place, error) {
	return nil, errNotImplemented
}
