package approvals

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
)

// The requests a person with an active account makes: to join an approved
// collective, to create a new one, and (for an Administrador) to rename theirs.

// pt-BR messages shown next to a form field.
const (
	msgRole         = "escolha membro ou administrador"
	msgKind         = "escolha coletivo ou produtora"
	msgControl      = "não use quebras de linha nem caracteres de controle"
	msgNoCollective = "coletivo não encontrado"
	msgAlreadyIn    = "você já faz parte deste coletivo ou já pediu para entrar"
	msgNameExists   = "já existe; peça para entrar"
	msgNameInUse    = "nome já em uso; escolha outro"
)

// RequestJoin asks, for the actor, to join the approved collective in the role
// (domain.RoleMember is Membro, domain.RoleOwner is Administrador). It creates a
// pending membership with both approvals open, emails the collective's approved
// Administradores (a link to the collective under origin) and the site admins (a
// link to the admin area), and writes an audit entry, in one transaction. It
// writes the collective back (with UpdatedAt set to now) so that it serializes
// with the decisions that read the collective.
//
// It returns ErrNotAllowed when the actor has no active account, ErrLocked when
// the collective is locked, and domain.FieldErrors for what the person can fix:
// the key "role" for a role that is not owner or member, and the key "collective"
// when the collective does not exist or is not approved, and when the actor
// already has a membership in it, in any status (a person asks once; a refused
// request is not asked again, and an Administrador decides it).
func RequestJoin(ctx context.Context, store domain.Store, origin string, actor domain.User, collectiveID string, role domain.Role, now time.Time) error {
	if err := requireActive(actor); err != nil {
		return err
	}
	if !role.Valid() {
		return domain.FieldErrors{"role": msgRole}
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		c, err := r.Collectives.ByID(ctx, collectiveID)
		if errors.Is(err, domain.ErrNotFound) {
			return domain.FieldErrors{"collective": msgNoCollective}
		}
		if err != nil {
			return wrap("request join: read collective", err)
		}
		if c.Status != domain.CollectiveStatusApproved {
			return domain.FieldErrors{"collective": msgNoCollective}
		}
		if c.Locked {
			return ErrLocked
		}
		switch _, err := r.Memberships.Get(ctx, actor.ID, c.ID); {
		case err == nil:
			return domain.FieldErrors{"collective": msgAlreadyIn}
		case !errors.Is(err, domain.ErrNotFound):
			return wrap("request join: read membership", err)
		}

		m := domain.Membership{UserID: actor.ID, CollectiveID: c.ID, Role: role, Status: domain.MembershipStatusPending, RequestedAt: now}
		switch err := r.Memberships.Create(ctx, m); {
		case errors.Is(err, domain.ErrAlreadyMember):
			// Another request of the same person won the race; the transaction may be
			// unusable now, so answer at once.
			return domain.FieldErrors{"collective": msgAlreadyIn}
		case err != nil:
			return wrap("request join: create membership", err)
		}
		c.UpdatedAt = now
		if err := r.Collectives.Update(ctx, c); err != nil {
			return wrap("request join: update collective", err)
		}

		label := RoleLabel(role)
		if err := notifyOwners(ctx, r, c.ID, mail.OwnerJoinRequestData{
			CollectiveName: c.Name, UserName: actor.Name, Role: label, Link: origin + collectivePath(c.ID),
		}, now); err != nil {
			return err
		}
		if err := notifyAdmins(ctx, r, mail.AdminRequestData{
			Kind: mail.AdminRequestJoin, UserName: actor.Name, CollectiveName: c.Name, Role: label, Link: origin + pathAdmin,
		}, now); err != nil {
			return err
		}
		e := auditBy(domain.AuditJoinRequested, actor, now)
		e.UserID, e.UserName, e.CollectiveID, e.CollectiveName, e.Detail = actor.ID, actor.Name, c.ID, c.Name, label
		return audit(ctx, r, e)
	})
}

// RequestCollective asks, for the actor, to create a collective or produtora
// called name. It creates the collective, pending, with the actor as its creator
// (the Master) and as its Administrador: a pending owner membership whose owner
// side is the actor's own approval and whose admin side is open. It emails the
// site admins (a link to the admin area under origin) and writes an audit entry,
// in one transaction. The name is cleaned by cleanCollectiveName.
//
// It returns ErrNotAllowed when the actor has no active account (checked before
// the input), and domain.FieldErrors for what the person can fix: the keys
// "name" and "kind" (both are reported at once), and "name" when another
// collective has the name's key: an approved one is told to ask to join it, any
// other (a pending one included) that the name is taken.
func RequestCollective(ctx context.Context, store domain.Store, origin string, actor domain.User, name string, kind domain.CollectiveKind, now time.Time) error {
	if err := requireActive(actor); err != nil {
		return err
	}
	var fe domain.FieldErrors
	name, problem := cleanCollectiveName(name)
	if problem != "" {
		fe.Add("name", problem)
	}
	if !kind.Valid() {
		fe.Add("kind", msgKind)
	}
	if err := fe.Err(); err != nil {
		return err
	}
	key := domain.NameKey(name)
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		switch other, err := r.Collectives.ByNameKey(ctx, key); {
		case err == nil && other.Status == domain.CollectiveStatusApproved:
			return domain.FieldErrors{"name": msgNameExists}
		case err == nil:
			return domain.FieldErrors{"name": msgNameInUse}
		case !errors.Is(err, domain.ErrNotFound):
			return wrap("request collective: look up the name", err)
		}

		// A new collective needs no write-back: its creation already conflicts with
		// anything that races for the same name.
		c := domain.Collective{
			Name: name, NameKey: key, Kind: kind, Status: domain.CollectiveStatusPending,
			CreatedBy: actor.ID, CreatedAt: now, UpdatedAt: now,
		}
		switch err := r.Collectives.Create(ctx, &c); {
		case errors.Is(err, domain.ErrNameTaken):
			// Another request for the same name won the race; the transaction may be
			// unusable now, so answer at once.
			return domain.FieldErrors{"name": msgNameInUse}
		case err != nil:
			return wrap("request collective: create collective", err)
		}
		at := now
		m := domain.Membership{
			UserID: actor.ID, CollectiveID: c.ID, Role: domain.RoleOwner, Status: domain.MembershipStatusPending,
			OwnerApprovedBy: actor.ID, OwnerApprovedAt: &at, RequestedAt: now,
		}
		if err := r.Memberships.Create(ctx, m); err != nil {
			return wrap("request collective: create membership", err)
		}

		if err := notifyAdmins(ctx, r, mail.AdminRequestData{
			Kind: mail.AdminRequestNewCollective, UserName: actor.Name, CollectiveName: c.Name, Link: origin + pathAdmin,
		}, now); err != nil {
			return err
		}
		e := auditBy(domain.AuditCollectiveRequested, actor, now)
		e.UserID, e.UserName, e.CollectiveID, e.CollectiveName = actor.ID, actor.Name, c.ID, c.Name
		return audit(ctx, r, e)
	})
}

// RenameCollective renames the approved collective for the actor, who must be one
// of its approved Administradores. The new name applies at once. It emails the
// site admins with both names (a link to the collectives under origin) and writes
// an audit entry whose detail is "<old> → <new>", in one transaction. The name is
// cleaned by cleanCollectiveName. A name that only differs in case or spacing is
// a rename like any other; one exactly equal to the current name changes and
// records nothing.
//
// It returns ErrNotAllowed when the actor has no active account, is not an
// approved Administrador of the collective (an unknown collective included: the
// caller learns no more than "not yours") or the collective is not approved,
// ErrLocked when the collective is locked, and domain.FieldErrors with the key
// "name" for a bad name (checked before the collective is read) and for a name
// another collective has.
func RenameCollective(ctx context.Context, store domain.Store, origin string, actor domain.User, collectiveID, name string, now time.Time) error {
	if err := requireActive(actor); err != nil {
		return err
	}
	name, problem := cleanCollectiveName(name)
	if problem != "" {
		return domain.FieldErrors{"name": problem}
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		if _, err := requireOwner(ctx, r, actor, collectiveID); err != nil {
			return err
		}
		c, err := r.Collectives.ByID(ctx, collectiveID)
		if err != nil {
			return wrap("rename collective: read collective", err)
		}
		if c.Status != domain.CollectiveStatusApproved {
			return ErrNotAllowed
		}
		if c.Locked {
			return ErrLocked
		}
		if c.Name == name {
			return nil
		}

		oldName := c.Name
		c.Name, c.NameKey, c.UpdatedAt = name, domain.NameKey(name), now
		switch err := r.Collectives.Update(ctx, c); {
		case errors.Is(err, domain.ErrNameTaken):
			return domain.FieldErrors{"name": msgNameInUse}
		case err != nil:
			return wrap("rename collective: update collective", err)
		}

		if err := notifyAdmins(ctx, r, mail.AdminRequestData{
			Kind: mail.AdminRequestRename, UserName: actor.Name, CollectiveName: c.Name, OldName: oldName, Link: origin + pathAdminCollectives,
		}, now); err != nil {
			return err
		}
		e := auditBy(domain.AuditCollectiveRenamed, actor, now)
		e.CollectiveID, e.CollectiveName, e.Detail = c.ID, c.Name, oldName+" → "+c.Name
		return audit(ctx, r, e)
	})
}

// requireActive returns ErrNotAllowed unless actor has an ID and an active
// account. The caller passes the user it loaded for the request, so an account
// disabled since the session started is refused here.
func requireActive(actor domain.User) error {
	if actor.ID == "" || actor.Status != domain.UserStatusActive {
		return ErrNotAllowed
	}
	return nil
}

// cleanCollectiveName trims name and checks it the way signup checks the name of
// a new collective: valid UTF-8 without control characters (line breaks
// included), domain.CollectiveNameMin to domain.CollectiveNameMax runes, and a
// comparison key that is not empty (a name of only combining marks has none). It
// returns the cleaned name and "", or "" and the pt-BR message for the field.
// What is inside the name is kept as typed.
//
// internal/accounts has the same check for signup; this package does not import
// it, so the few lines are repeated.
func cleanCollectiveName(name string) (cleaned, problem string) {
	name = strings.TrimSpace(name)
	switch n := utf8.RuneCountInString(name); {
	case !utf8.ValidString(name):
		return "", domain.MsgInvalid
	case strings.ContainsFunc(name, unicode.IsControl):
		return "", msgControl
	case n == 0:
		return "", domain.MsgRequired
	case n < domain.CollectiveNameMin || n > domain.CollectiveNameMax:
		return "", fmt.Sprintf("use de %d a %d caracteres", domain.CollectiveNameMin, domain.CollectiveNameMax)
	case domain.NameKey(name) == "":
		return "", domain.MsgInvalid
	}
	return name, ""
}
