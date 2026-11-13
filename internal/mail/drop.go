package mail

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// DropSender writes each message as an .eml file in a folder instead of sending
// it, for development: open the files in a mail client.
type DropSender struct {
	// From is the sender address written to each file. Set it before the first
	// Send; it starts as config.DefaultSMTPFrom.
	From string

	dir string
}

// NewDropSender returns a sender that writes into dir, creating it (mode 0700)
// when it does not exist.
func NewDropSender(dir string) (*DropSender, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("mail: the drop folder is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mail: the drop folder: %w", err)
	}
	return &DropSender{From: config.DefaultSMTPFrom, dir: dir}, nil
}

// Send writes m to a new file named <UTC time>-<random>.eml, so a listing sorts
// by arrival and two messages never share a name. The file appears whole or
// not at all: it is written under a temporary name and then renamed.
func (d *DropSender) Send(ctx context.Context, m domain.OutboxMessage) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("mail: send: %w", err)
	}
	from, err := parseFrom(d.From)
	if err != nil {
		return err
	}
	msg, err := buildMessage(from, m)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(d.dir, ".tmp-*") // mode 0600
	if err != nil {
		return fmt.Errorf("mail: write message: %w", err)
	}
	_, writeErr := msg.WriteTo(tmp)
	if err := errors.Join(writeErr, tmp.Close()); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("mail: write message: %w", err)
	}
	name := time.Now().UTC().Format("20060102T150405.000Z") + "-" + randomHex(8) + ".eml"
	if err := os.Rename(tmp.Name(), filepath.Join(d.dir, name)); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("mail: write message: %w", err)
	}
	return nil
}
