package mail

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/config"
)

// dropName is a file name written by a DropSender: a UTC timestamp, so a
// listing sorts by arrival, then a random suffix.
var dropName = regexp.MustCompile(`^(\d{8}T\d{6}\.\d{3}Z)-[0-9a-f]{16}\.eml$`)

// dirNames lists the entries of dir, sorted.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names
}

func readDrop(t *testing.T, dir, name string) parsedEML {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return parseEML(t, raw)
}

func TestDropSenderWritesAParsableMessage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mail", "outbox") // does not exist yet
	d, err := NewDropSender(dir)
	if err != nil {
		t.Fatalf("NewDropSender: %v", err)
	}
	before := time.Now().UTC()
	if err := d.Send(context.Background(), sampleMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	names := dirNames(t, dir)
	if len(names) != 1 {
		t.Fatalf("the folder holds %v, want one file", names)
	}
	m := dropName.FindStringSubmatch(names[0])
	if m == nil {
		t.Fatalf("file name %q does not match %s", names[0], dropName)
	}
	stamp, err := time.Parse("20060102T150405.000Z", m[1])
	if err != nil || !within(stamp, before, time.Minute) {
		t.Errorf("file name timestamp %q = %v (%v), want about %v", m[1], stamp, err, before)
	}
	checkMessage(t, readDrop(t, dir, names[0]), sampleMessage(), config.DefaultSMTPFrom)

	if runtime.GOOS != "windows" { // Windows has no Unix permission bits
		file, err := os.Stat(filepath.Join(dir, names[0]))
		if err != nil {
			t.Fatal(err)
		}
		if got := file.Mode().Perm(); got != 0o600 {
			t.Errorf("file mode = %o, want 600", got)
		}
		folder, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := folder.Mode().Perm(); got != 0o700 {
			t.Errorf("folder mode = %o, want 700", got)
		}
	}
}

func TestDropSenderWritesOneFilePerSend(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDropSender(dir)
	if err != nil {
		t.Fatalf("NewDropSender: %v", err)
	}
	// The same message twice still makes two files: nothing in a name repeats.
	for _, subject := range []string{"Confirme seu e-mail", "Redefinir sua senha", "Confirme seu e-mail"} {
		msg := sampleMessage()
		msg.Subject = subject
		if err := d.Send(context.Background(), msg); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	names := dirNames(t, dir)
	if len(names) != 3 {
		t.Fatalf("the folder holds %v, want three files", names)
	}
	subjects := map[string]int{}
	for _, name := range names {
		if !dropName.MatchString(name) { // also rules out a leftover temporary file
			t.Errorf("unexpected file %q", name)
			continue
		}
		p := readDrop(t, dir, name)
		subjects[p.Header.Get("Subject")]++
	}
	if len(subjects) != 2 {
		t.Errorf("the files carry %d distinct subjects, want 2: %v", len(subjects), subjects)
	}
}

func TestDropSenderWritesTextOnlyMessages(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDropSender(dir)
	if err != nil {
		t.Fatalf("NewDropSender: %v", err)
	}
	m := sampleMessage()
	m.HTML = ""
	if err := d.Send(context.Background(), m); err != nil {
		t.Fatalf("Send: %v", err)
	}
	names := dirNames(t, dir)
	if len(names) != 1 {
		t.Fatalf("the folder holds %v, want one file", names)
	}
	checkMessage(t, readDrop(t, dir, names[0]), m, config.DefaultSMTPFrom)
}

func TestDropSenderUsesItsFromAddress(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDropSender(dir)
	if err != nil {
		t.Fatalf("NewDropSender: %v", err)
	}
	d.From = "Teste <teste@example.org>"
	if err := d.Send(context.Background(), sampleMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	names := dirNames(t, dir)
	if len(names) != 1 {
		t.Fatalf("the folder holds %v, want one file", names)
	}
	checkMessage(t, readDrop(t, dir, names[0]), sampleMessage(), d.From)

	d.From = "not an address"
	if err := d.Send(context.Background(), sampleMessage()); err == nil {
		t.Error("Send with a From that is not an address succeeded, want an error")
	}
	if got := dirNames(t, dir); len(got) != 1 {
		t.Errorf("the folder holds %v, want the one file from before", got)
	}
}

func TestDropSenderReturnsContextErrors(t *testing.T) {
	dir := t.TempDir()
	d, err := NewDropSender(dir)
	if err != nil {
		t.Fatalf("NewDropSender: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.Send(ctx, sampleMessage()); err == nil {
		t.Error("Send with a canceled context succeeded, want an error")
	}
	if names := dirNames(t, dir); len(names) != 0 {
		t.Errorf("the folder holds %v, want it empty", names)
	}
}

func TestNewDropSenderRefusesAnUnusableFolder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plain-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{
		"empty":                  "",
		"blank":                  "   ",
		"an existing file":       file,
		"below an existing file": filepath.Join(file, "sub"),
	} {
		if _, err := NewDropSender(dir); err == nil {
			t.Errorf("%s: NewDropSender(%q) succeeded, want an error", name, dir)
		} else if strings.Contains(err.Error(), "\n") {
			t.Errorf("%s: error spans lines: %q", name, err)
		}
	}
}
