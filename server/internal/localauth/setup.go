package localauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"custodia/server/internal/lab"
)

// First administrator by setup code (see docs/ACCESS.md). The host prints a
// short one-use code; the person types it in the browser with the account
// they want. Only the code's SHA-256 is stored.
const (
	// SetupCodeAlphabet has 32 symbols without I, O, 0 and 1, which are easy
	// to confuse when copied from a terminal.
	SetupCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	// SetupCodeLength symbols of the alphabet give 40 bits.
	SetupCodeLength = 8
	// SetupCodeLifetime is how long a printed code can be used.
	SetupCodeLifetime = 15 * time.Minute
)

var (
	// SetupIPAttempts bounds wrong codes from one address.
	SetupIPAttempts = lab.AttemptPolicy{Limit: 5, Window: 15 * time.Minute, Lock: 15 * time.Minute}
	// SetupAllAttempts bounds wrong codes in total; reaching it pauses every
	// claim for a few minutes without destroying the valid code. The pause is
	// shorter than a code's lifetime so its owner can still use it, and a new
	// code from the host clears every counter.
	SetupAllAttempts = lab.AttemptPolicy{Limit: 30, Window: 15 * time.Minute, Lock: 5 * time.Minute}
)

const setupInvalid = "El código no es válido o venció. Pedí uno nuevo en el servidor con custodia setup-code."

// NewSetupCode returns a fresh code formatted as XXXX-XXXX. Each symbol takes
// five bits of one random byte; 256 is a multiple of 32, so it is uniform.
func NewSetupCode() (string, error) {
	random := make([]byte, SetupCodeLength)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	symbols := make([]byte, SetupCodeLength)
	for i, value := range random {
		symbols[i] = SetupCodeAlphabet[value&31]
	}
	return string(symbols[:4]) + "-" + string(symbols[4:]), nil
}

// NormalizeSetupCode accepts the code with or without its dash, spaces and
// lower case, and reports whether it has the right shape.
func NormalizeSetupCode(value string) (string, bool) {
	code := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(value)))
	if len(code) != SetupCodeLength {
		return "", false
	}
	for _, symbol := range code {
		if !strings.ContainsRune(SetupCodeAlphabet, symbol) {
			return "", false
		}
	}
	return code, true
}

// SetupCodeHash is the stored form of a normalized code.
func SetupCodeHash(normalized string) string {
	sum := sha256.Sum256([]byte("custodia-setup-code:" + normalized))
	return hex.EncodeToString(sum[:])
}

func (h *handler) setup(w http.ResponseWriter, r *http.Request, current time.Time) {
	if h.config.mode() != "local" {
		detail(w, 404, "La configuración inicial sólo existe en modo local.")
		return
	}
	var input struct {
		Code        string `json:"code"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if !decode(w, r, &input) || len(input.Code) > 64 || len(input.Username) > 256 {
		detail(w, 400, "Completá el código, el usuario, el nombre y la contraseña.")
		return
	}
	ipKey := "setup-ip:" + clientIP(r)
	keys := []lab.LoginAttemptKey{{Key: ipKey, Policy: SetupIPAttempts}, {Key: "setup:all", Policy: SetupAllAttempts}}
	reservation, locked, err := h.store.ReserveLoginAttempt(r.Context(), keys, current)
	if errors.Is(err, lab.ErrAttemptCapacity) || (err == nil && locked) {
		detail(w, 429, "Demasiados intentos con códigos equivocados. Esperá unos minutos.")
		return
	}
	if err != nil {
		detail(w, 503, "No se pudo verificar el código.")
		return
	}
	defer func() {
		_ = h.store.ReleaseLoginAttempt(context.WithoutCancel(r.Context()), reservation)
	}()
	fail := func() {
		if _, err := h.store.RecordLoginFailure(r.Context(), ipKey, SetupIPAttempts, "anonymous", current); err != nil {
			detail(w, 503, "No se pudo verificar el código.")
			return
		}
		if _, err := h.store.RecordLoginFailure(r.Context(), "setup:all", SetupAllAttempts, "anonymous", current); err != nil {
			detail(w, 503, "No se pudo verificar el código.")
			return
		}
		detail(w, 400, setupInvalid)
	}
	code, shaped := NormalizeSetupCode(input.Code)
	if !shaped {
		fail()
		return
	}
	hash := SetupCodeHash(code)
	matches, err := h.store.SetupCodeMatches(r.Context(), hash, current)
	if err != nil {
		detail(w, 503, "No se pudo verificar el código.")
		return
	}
	if !matches {
		fail()
		return
	}
	username, valid := lab.NormalizeLocalUsername(input.Username)
	if !valid || strings.TrimSpace(input.DisplayName) == "" || len(input.DisplayName) > 200 || strings.ContainsAny(input.DisplayName, "\x00\r\n") {
		detail(w, 400, "Usá un usuario en minúsculas (letras, números, punto, guion o @) y un nombre visible.")
		return
	}
	if CheckPolicy(username, input.Password) != nil {
		detail(w, 400, "La contraseña debe tener entre 15 y 256 caracteres y ser distinta del usuario.")
		return
	}
	passwordHash, err := HashPassword(r.Context(), input.Password)
	if err != nil {
		detail(w, 503, "No se pudo crear la cuenta.")
		return
	}
	principal, err := h.store.ClaimSetupCode(r.Context(), hash, username, input.DisplayName, passwordHash, current)
	if errors.Is(err, lab.ErrSetupCode) {
		fail()
		return
	}
	if err != nil {
		detail(w, 503, "No se pudo crear la cuenta.")
		return
	}
	token, err := newToken()
	if err != nil {
		detail(w, 503, "La cuenta se creó; iniciá sesión con ella.")
		return
	}
	if err = h.store.StartLocalSession(r.Context(), principal, passwordHash, TokenHash(token), current, current.Add(AbsoluteLifetime)); err != nil {
		detail(w, 503, "La cuenta se creó; iniciá sesión con ella.")
		return
	}
	http.SetCookie(w, h.sessionCookie(token))
	send(w, 201, map[string]any{"authenticated": true, "csrf_token": CSRFToken(token), "must_change_password": false})
}
