package auth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
)

func TestRulesOrder(t *testing.T) {
	want := []Rule{RuleMinLength, RuleLower, RuleUpper, RuleDigit, RuleSymbol, RuleNotEmail, RuleMaxLength}
	got := Rules()
	if !slices.Equal(got, want) {
		t.Fatalf("Rules() = %v, want %v", got, want)
	}
	got[0] = RuleHistory // callers must not be able to change the shared order
	if again := Rules(); !slices.Equal(again, want) {
		t.Fatalf("Rules() after mutating a result = %v, want %v", again, want)
	}
}

func TestRuleLabels(t *testing.T) {
	for _, r := range append(Rules(), RuleHistory) {
		if RuleLabels[r] == "" {
			t.Errorf("RuleLabels[%q] is empty", r)
		}
	}
	want := map[Rule]string{
		RuleMinLength: "8 caracteres ou mais",
		RuleLower:     "uma letra minúscula",
		RuleUpper:     "uma letra maiúscula",
		RuleDigit:     "um número",
		RuleSymbol:    "um símbolo",
		RuleNotEmail:  "diferente do seu e-mail",
		RuleMaxLength: "até 128 caracteres",
		RuleHistory:   "diferente das 2 últimas senhas",
	}
	for r, label := range want {
		if RuleLabels[r] != label {
			t.Errorf("RuleLabels[%q] = %q, want %q", r, RuleLabels[r], label)
		}
	}
}

func TestCheckPolicy(t *testing.T) {
	const email = "user@example.com"
	tests := []struct {
		name     string
		password string
		email    string
		want     []Rule
	}{
		{"valid", "Abcdef1!", email, nil},
		{"min length 7 runes", "Abcde1!", email, []Rule{RuleMinLength}},
		{"min length 8 runes", "Abcdef1!", email, nil},
		{"short but complete", "Ab1!", email, []Rule{RuleMinLength}},
		{"no lowercase", "ABCDEF1!", email, []Rule{RuleLower}},
		{"no uppercase", "abcdef1!", email, []Rule{RuleUpper}},
		{"no digit", "Abcdefg!", email, []Rule{RuleDigit}},
		{"no symbol", "Abcdefg1", email, []Rule{RuleSymbol}},
		{"space is a symbol", "Abcdef 1", email, nil},
		{"punctuation is a symbol", "Abcdef1.", email, nil},
		{"whole email", "Abcdef1!@example.com", "Abcdef1!@example.com", []Rule{RuleNotEmail}},
		{"whole email other case", "Abcdef1!@Example.com", "abcdef1!@example.com", []Rule{RuleNotEmail}},
		{"whole email with spaces", " Abcdef1!@example.com ", "  abcdef1!@example.com", []Rule{RuleNotEmail}},
		{"local part", "Abcdef1!", "abcdef1!@example.com", []Rule{RuleNotEmail}},
		{"local part with spaces", "  Abcdef1!  ", "abcdef1!@example.com", []Rule{RuleNotEmail}},
		{"local part of 3 runes is checked", "Abc", "abc@x.com", []Rule{RuleMinLength, RuleDigit, RuleSymbol, RuleNotEmail}},
		{"local part under 3 runes is exempt", "Ab", "ab@example.com", []Rule{RuleMinLength, RuleDigit, RuleSymbol}},
		{"whole email still checked for a short local part", "A@B.co", "a@b.co", []Rule{RuleMinLength, RuleDigit, RuleNotEmail}},
		{"email without at sign", "Abcdef1!", "Abcdef1!", []Rule{RuleNotEmail}},
		{"password contains the email", "Abcdef1!user@example.com", email, nil},
		{"empty email skips the rule", "Abcdef1!", "", nil},
		{"blank email skips the rule", "Abcdef1!", "   ", nil},
		{"empty password and empty email", "", "", []Rule{RuleMinLength, RuleLower, RuleUpper, RuleDigit, RuleSymbol}},
		{"empty password", "", email, []Rule{RuleMinLength, RuleLower, RuleUpper, RuleDigit, RuleSymbol}},
		{"one letter", "a", email, []Rule{RuleMinLength, RuleUpper, RuleDigit, RuleSymbol}},
		{"128 runes", "Aa1!" + strings.Repeat("x", 124), email, nil},
		{"129 runes", "Aa1!" + strings.Repeat("x", 125), email, []Rule{RuleMaxLength}},
		{"129 lowercase letters", strings.Repeat("a", 129), email, []Rule{RuleUpper, RuleDigit, RuleSymbol, RuleMaxLength}},
		{"multibyte classes", "Çç1!Çç1!", email, nil},
		{"7 runes in more than 8 bytes", "Çç1!Çç1", email, []Rule{RuleMinLength}},
		{"4 multibyte runes", "Çç1!", email, []Rule{RuleMinLength}},
		{"128 multibyte runes", "Çç1!" + strings.Repeat("ç", 124), email, nil},
		{"129 multibyte runes", "Çç1!" + strings.Repeat("ç", 125), email, []Rule{RuleMaxLength}},
		{"multibyte uppercase only", "ÇÇÇÇÇÇ1!", email, []Rule{RuleLower}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckPolicy(tc.password, tc.email)
			if len(tc.want) == 0 {
				if got != nil {
					t.Fatalf("CheckPolicy = %v, want nil", got)
				}
				return
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("CheckPolicy = %v, want %v", got, tc.want)
			}
		})
	}
}

// testPassword is a policy-compliant password reused across hash tests.
const testPassword = "Abcdef1!"

var testHash = sync.OnceValue(func() string {
	h, err := HashPassword(testPassword)
	if err != nil {
		panic(err)
	}
	return h
})

func TestHashPasswordFormat(t *testing.T) {
	h, err := HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`^\$argon2id\$v=19\$m=19456,t=2,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`)
	if !re.MatchString(h) {
		t.Fatalf("hash %q is not 16-byte salt and 32-byte key PHC argon2id", h)
	}
	if strings.Contains(h, testPassword) {
		t.Fatal("hash contains the password")
	}
}

func TestHashPasswordMatchesArgon2(t *testing.T) {
	h, err := HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(h, "$")
	if len(parts) != 6 {
		t.Fatalf("hash %q has %d parts, want 6", h, len(parts))
	}
	salt, err := b64Std(parts[4])
	if err != nil {
		t.Fatal(err)
	}
	key, err := b64Std(parts[5])
	if err != nil {
		t.Fatal(err)
	}
	if want := argon2.IDKey([]byte(testPassword), salt, 2, 19456, 1, 32); !slices.Equal(key, want) {
		t.Fatal("stored key is not argon2id(password, salt, t=2, m=19456, p=1)")
	}
}

func TestHashPasswordSalted(t *testing.T) {
	a, err := HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two hashes of one password are identical: the salt is not random")
	}
}

func TestVerifyPassword(t *testing.T) {
	h := testHash()
	ok, err := VerifyPassword(h, testPassword)
	if err != nil || !ok {
		t.Fatalf("VerifyPassword(right password) = %v, %v; want true, nil", ok, err)
	}
	for _, wrong := range []string{"abcdef1!", "Abcdef1!x", "", " Abcdef1!"} {
		ok, err := VerifyPassword(h, wrong)
		if err != nil || ok {
			t.Errorf("VerifyPassword(%q) = %v, %v; want false, nil", wrong, ok, err)
		}
	}
}

func TestVerifyPasswordMultibyte(t *testing.T) {
	const pw = "Çç1!Çç1!"
	h, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifyPassword(h, pw); err != nil || !ok {
		t.Fatalf("VerifyPassword = %v, %v; want true, nil", ok, err)
	}
}

func TestVerifyPasswordAcceptsBoundaryParams(t *testing.T) {
	tests := []struct {
		name          string
		m, t, p       uint32
		saltLen, kLen int
	}{
		{"minimum", 8, 1, 1, 8, 16},
		{"threads and rounds at the limit", 32, 10, 4, 64, 64},
		{"memory at the limit", 65536, 1, 1, 16, 32},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := phcFor(testPassword, tc.m, tc.t, tc.p, tc.saltLen, tc.kLen)
			ok, err := VerifyPassword(h, testPassword)
			if err != nil || !ok {
				t.Fatalf("VerifyPassword(%q) = %v, %v; want true, nil", h, ok, err)
			}
			ok, err = VerifyPassword(h, "wrong")
			if err != nil || ok {
				t.Fatalf("VerifyPassword(wrong) = %v, %v; want false, nil", ok, err)
			}
		})
	}
}

func TestVerifyPasswordRejectsBadHashes(t *testing.T) {
	good := testHash()
	parts := strings.Split(good, "$") // "", argon2id, v=19, params, salt, key
	if len(parts) != 6 {
		t.Fatalf("HashPassword returned %q, want six $-separated parts", good)
	}
	replace := func(i int, v string) string {
		c := slices.Clone(parts)
		c[i] = v
		return strings.Join(c, "$")
	}
	salt := func(n int) string { return b64StdEncode(make([]byte, n)) }
	// A canonical 16-byte salt ends in a character carrying 4 zero bits;
	// bump it so the trailing bits are not zero.
	nonCanonicalSalt := parts[4][:21] + string(b64Alphabet[strings.IndexByte(b64Alphabet, parts[4][21])+1])

	tests := []struct{ name, encoded string }{
		{"empty", ""},
		{"garbage", "garbage"},
		{"missing key", strings.Join(parts[:5], "$")},
		{"missing salt and key", strings.Join(parts[:4], "$")},
		{"missing version", strings.Join([]string{"", "argon2id", parts[3], parts[4], parts[5]}, "$")},
		{"extra part", good + "$x"},
		{"trailing dollar", good + "$"},
		{"leading text", "x" + good},
		{"argon2i", replace(1, "argon2i")},
		{"argon2d", replace(1, "argon2d")},
		{"bcrypt", replace(1, "bcrypt")},
		{"version 16", replace(2, "v=16")},
		{"version 18", replace(2, "v=18")},
		{"version without prefix", replace(2, "19")},
		{"params leading zero", replace(3, "m=019456,t=2,p=1")},
		{"params missing p", replace(3, "m=19456,t=2")},
		{"params wrong order", replace(3, "t=2,m=19456,p=1")},
		{"params extra field", replace(3, "m=19456,t=2,p=1,k=3")},
		{"params negative", replace(3, "m=-1,t=2,p=1")},
		{"params plus sign", replace(3, "m=+19456,t=2,p=1")},
		{"params space", replace(3, "m= 19456,t=2,p=1")},
		{"params not a number", replace(3, "m=abc,t=2,p=1")},
		{"params empty", replace(3, "")},
		{"salt not base64", replace(4, "@@@@@@@@@@@@@@@@@@@@@@")},
		{"salt with padding", replace(4, parts[4]+"==")},
		{"salt in url alphabet", replace(4, "ab-d_fghijklmnopqrstuv")},
		{"salt trailing bits set", replace(4, nonCanonicalSalt)},
		{"salt with a line break", replace(4, parts[4]+"\n")},
		{"key with a line break", replace(5, parts[5][:20]+"\r\n"+parts[5][20:])},
		{"key not base64", replace(5, "!!!!")},
		{"key with padding", replace(5, parts[5]+"=")},
		{"salt empty", replace(4, "")},
		{"key empty", replace(5, "")},
		{"salt 7 bytes", replace(4, salt(7))},
		{"salt 65 bytes", replace(4, salt(65))},
		{"key 15 bytes", replace(5, salt(15))},
		{"key 65 bytes", replace(5, salt(65))},
		// Out-of-bounds parameters must be refused before any work is done.
		{"memory above the limit", replace(3, "m=65537,t=2,p=1")},
		{"memory enormous", replace(3, "m=4294967295,t=2,p=1")},
		{"memory overflowing uint32", replace(3, "m=99999999999999999999,t=2,p=1")},
		{"memory below 8 per thread", replace(3, "m=31,t=2,p=4")},
		{"rounds above the limit", replace(3, "m=19456,t=11,p=1")},
		{"rounds overflowing uint32", replace(3, "m=19456,t=4294967296,p=1")},
		{"rounds zero", replace(3, "m=19456,t=0,p=1")},
		{"threads above the limit", replace(3, "m=19456,t=2,p=5")},
		{"threads zero", replace(3, "m=19456,t=2,p=0")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := VerifyPassword(tc.encoded, testPassword)
			if err == nil || ok {
				t.Fatalf("VerifyPassword(%q) = %v, %v; want false and an error", tc.encoded, ok, err)
			}
			if !errors.Is(err, ErrInvalidHash) {
				t.Errorf("error %v is not ErrInvalidHash", err)
			}
			if tc.encoded != "" && strings.Contains(err.Error(), tc.encoded) {
				t.Errorf("error %q repeats the hash", err)
			}
			if strings.Contains(err.Error(), testPassword) {
				t.Errorf("error %q repeats the password", err)
			}
		})
	}
}

func TestMatchesAny(t *testing.T) {
	h1, err := HashPassword("Abcdef1!")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashPassword("Uvwxyz2@")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		password string
		hashes   []string
		want     bool
	}{
		{"nil history", "Abcdef1!", nil, false},
		{"empty history", "Abcdef1!", []string{}, false},
		{"one hash that matches", "Abcdef1!", []string{h1}, true},
		{"one hash that does not match", "Other3#x", []string{h1}, false},
		{"two hashes, first matches", "Abcdef1!", []string{h1, h2}, true},
		{"two hashes, second matches", "Uvwxyz2@", []string{h1, h2}, true},
		{"two hashes, none matches", "Other3#x", []string{h1, h2}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MatchesAny(tc.password, tc.hashes)
			if err != nil || got != tc.want {
				t.Fatalf("MatchesAny = %v, %v; want %v, nil", got, err, tc.want)
			}
		})
	}
	t.Run("malformed hash", func(t *testing.T) {
		got, err := MatchesAny("Other3#x", []string{h1, "garbage"})
		if !errors.Is(err, ErrInvalidHash) || got {
			t.Fatalf("MatchesAny = %v, %v; want false, ErrInvalidHash", got, err)
		}
	})
}

func TestDummyVerifyDoesTheWorkOfAVerify(t *testing.T) {
	DummyVerify("first call builds the shared hash")
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	DummyVerify("Abcdef1!")
	DummyVerify("")
	runtime.ReadMemStats(&after)
	// Each argon2id run allocates its 19456 KiB working memory.
	if got, want := after.TotalAlloc-before.TotalAlloc, uint64(2*19456*1024); got < want {
		t.Fatalf("two DummyVerify calls allocated %d bytes, want at least %d: no hashing was done", got, want)
	}
}

func TestPasswordExpired(t *testing.T) {
	changed := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	deadline := changed.Add(MaxAge)
	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"just changed", changed, false},
		{"one nanosecond before 90 days", deadline.Add(-time.Nanosecond), false},
		{"exactly 90 days", deadline, true},
		{"one nanosecond after 90 days", deadline.Add(time.Nanosecond), true},
		{"a year later", changed.Add(365 * 24 * time.Hour), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PasswordExpired(changed, tc.now); got != tc.want {
				t.Fatalf("PasswordExpired = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDaysUntilExpiry(t *testing.T) {
	changed := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	deadline := changed.Add(MaxAge)
	const day = 24 * time.Hour
	tests := []struct {
		name string
		now  time.Time
		want int
	}{
		{"just changed", changed, 90},
		{"one nanosecond later rounds up", changed.Add(time.Nanosecond), 90},
		{"one day later", changed.Add(day), 89},
		{"one day and a nanosecond later", changed.Add(day + time.Nanosecond), 89},
		{"89 days later", changed.Add(89 * day), 1},
		{"89 days and an hour later", changed.Add(89*day + time.Hour), 1},
		{"one nanosecond before the deadline", deadline.Add(-time.Nanosecond), 1},
		{"at the deadline", deadline, 0},
		{"after the deadline", deadline.Add(30 * day), 0},
		{"changed a day in the future", changed.Add(-day), 91},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DaysUntilExpiry(changed, tc.now); got != tc.want {
				t.Fatalf("DaysUntilExpiry = %d, want %d", got, tc.want)
			}
		})
	}
}

// Time.Sub saturates at the largest Duration, so a far-future changedAt must
// not overflow the rounding.
func TestDaysUntilExpiryFarFuture(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	farFuture := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := DaysUntilExpiry(farFuture, now); got <= 0 {
		t.Fatalf("DaysUntilExpiry(year 9999) = %d, want a positive number", got)
	}
	if got := DaysUntilExpiry(now, farFuture); got != 0 {
		t.Fatalf("DaysUntilExpiry(now in year 9999) = %d, want 0", got)
	}
	if days, warn := ExpiryWarning(farFuture, now); days <= 0 || warn {
		t.Fatalf("ExpiryWarning(year 9999) = %d, %v; want a positive number, false", days, warn)
	}
}

func TestExpiryWarning(t *testing.T) {
	changed := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	deadline := changed.Add(MaxAge)
	tests := []struct {
		name     string
		now      time.Time
		wantDays int
		wantWarn bool
	}{
		{"fresh password", changed, 90, false},
		{"7 days and a second left", deadline.Add(-WarnBefore - time.Second), 8, false},
		{"exactly 7 days left", deadline.Add(-WarnBefore), 7, true},
		{"3 days and a bit left", deadline.Add(-3*24*time.Hour - time.Hour), 4, true},
		{"one nanosecond left", deadline.Add(-time.Nanosecond), 1, true},
		{"expired exactly now", deadline, 0, false},
		{"long expired", deadline.Add(10 * 24 * time.Hour), 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			days, warn := ExpiryWarning(changed, tc.now)
			if days != tc.wantDays || warn != tc.wantWarn {
				t.Fatalf("ExpiryWarning = %d, %v; want %d, %v", days, warn, tc.wantDays, tc.wantWarn)
			}
		})
	}
}

func TestPolicyConstants(t *testing.T) {
	if MinLength != 8 || MaxLength != 128 || HistorySize != 2 {
		t.Errorf("MinLength, MaxLength, HistorySize = %d, %d, %d", MinLength, MaxLength, HistorySize)
	}
	if MaxAge != 90*24*time.Hour || WarnBefore != 7*24*time.Hour {
		t.Errorf("MaxAge, WarnBefore = %v, %v", MaxAge, WarnBefore)
	}
	want := map[Rule]string{
		RuleMinLength: "min_length", RuleMaxLength: "max_length", RuleLower: "lower", RuleUpper: "upper",
		RuleDigit: "digit", RuleSymbol: "symbol", RuleNotEmail: "not_email", RuleHistory: "history",
	}
	for r, s := range want {
		if string(r) != s {
			t.Errorf("rule %q, want %q", r, s)
		}
	}
}

const b64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

func b64StdEncode(b []byte) string { return base64.RawStdEncoding.EncodeToString(b) }

func b64Std(s string) ([]byte, error) { return base64.RawStdEncoding.DecodeString(s) }

// phcFor builds a PHC argon2id hash of password with the given parameters,
// independently of HashPassword.
func phcFor(password string, m, t, p uint32, saltLen, keyLen int) string {
	salt := make([]byte, saltLen)
	for i := range salt {
		salt[i] = byte(i + 1)
	}
	key := argon2.IDKey([]byte(password), salt, t, m, uint8(p), uint32(keyLen))
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", m, t, p, b64StdEncode(salt), b64StdEncode(key))
}
