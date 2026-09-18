// Package webbotauth implements the request-signing profile used by
// Cloudflare Web Bot Auth.
package webbotauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/joshternet/joshbot/internal/origin"
)

const (
	// SignatureAgentURL is JoshBot's HTTP Message Signature directory.
	SignatureAgentURL = "https://joshternet.org/.well-known/http-message-signatures-directory"

	signatureLabel    = "sig1"
	signatureLifetime = time.Minute
	nonceSize         = 32
)

var (
	// ErrSignerUnavailable reports use of a nil signer.
	ErrSignerUnavailable = errors.New("web bot auth: signer unavailable")

	// ErrInvalidSigningKey reports an Ed25519 private key with the wrong size.
	ErrInvalidSigningKey = errors.New("web bot auth: invalid Ed25519 signing key")

	// ErrInvalidKeyID reports a key identifier that is not a canonical
	// base64url-encoded SHA-256 JWK thumbprint.
	ErrInvalidKeyID = errors.New("web bot auth: invalid JWK thumbprint keyid")

	// ErrInvalidRequest reports a request that cannot supply a valid HTTP
	// authority for the signature base.
	ErrInvalidRequest = errors.New("web bot auth: invalid HTTP request")

	// ErrNonceGeneration reports failure to generate the per-request nonce.
	ErrNonceGeneration = errors.New("web bot auth: nonce generation failed")

	errNonceSourceUnavailable = errors.New(
		"web bot auth: nonce source unavailable",
	)
	errClockUnavailable = errors.New(
		"web bot auth: clock unavailable",
	)
)

// Signer attaches the Cloudflare Web Bot Auth HTTP Message Signature headers
// to requests.
//
// Private signing material is deliberately unexported. Formatting and
// structured logging expose only the public key identifier.
type Signer struct {
	privateKey ed25519.PrivateKey
	keyID      string
	nonce      io.Reader
	now        func() time.Time
}

// NewSigner constructs a Web Bot Auth signer from an Ed25519 private key and
// its already-derived JWK thumbprint key identifier.
//
// Key loading, public JWK derivation, and private/public consistency checks are
// configuration concerns handled outside this protocol primitive.
func NewSigner(
	privateKey ed25519.PrivateKey,
	keyID string,
) (*Signer, error) {
	return newSigner(
		privateKey,
		keyID,
		rand.Reader,
		time.Now,
	)
}

func newSigner(
	privateKey ed25519.PrivateKey,
	keyID string,
	nonce io.Reader,
	now func() time.Time,
) (*Signer, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, ErrInvalidSigningKey
	}

	if !validKeyID(keyID) {
		return nil, ErrInvalidKeyID
	}

	if nonce == nil {
		return nil, errNonceSourceUnavailable
	}

	if now == nil {
		return nil, errClockUnavailable
	}

	keyCopy := append(
		ed25519.PrivateKey(nil),
		privateKey...,
	)

	return &Signer{
		privateKey: keyCopy,
		keyID:      keyID,
		nonce:      nonce,
		now:        now,
	}, nil
}

// Sign attaches Signature-Agent, Signature-Input, and Signature to request.
//
// The signature covers @authority and signature-agent. It uses Ed25519, the
// configured JWK thumbprint keyid, a fresh nonce, and a one-minute validity
// window tagged for web-bot-auth.
func (signer *Signer) Sign(
	request *http.Request,
) error {
	if signer == nil {
		return ErrSignerUnavailable
	}

	authority, err := requestAuthority(request)
	if err != nil {
		return err
	}

	nonceBytes := make([]byte, nonceSize)
	if _, err := io.ReadFull(
		signer.nonce,
		nonceBytes,
	); err != nil {
		return fmt.Errorf(
			"%w: %w",
			ErrNonceGeneration,
			err,
		)
	}

	created := signer.now().Unix()
	expires := created + int64(
		signatureLifetime/time.Second,
	)
	nonce := base64.StdEncoding.EncodeToString(
		nonceBytes,
	)

	agentValue := `"` + SignatureAgentURL + `"`
	parameters := signatureParameters(
		created,
		expires,
		signer.keyID,
		nonce,
	)
	base := signatureBase(
		authority,
		agentValue,
		parameters,
	)
	signature := ed25519.Sign(
		signer.privateKey,
		[]byte(base),
	)

	if request.Header == nil {
		request.Header = make(http.Header)
	}

	request.Header.Set(
		"Signature-Agent",
		agentValue,
	)
	request.Header.Set(
		"Signature-Input",
		signatureLabel+"="+parameters,
	)
	request.Header.Set(
		"Signature",
		signatureLabel+"=:"+
			base64.StdEncoding.EncodeToString(signature)+
			":",
	)

	return nil
}

// LogValue deliberately exposes only public signer metadata.
func (signer *Signer) LogValue() slog.Value {
	if signer == nil {
		return slog.StringValue("<nil>")
	}

	return slog.GroupValue(
		slog.String(
			"key_id",
			signer.keyID,
		),
	)
}

// Format prevents fmt-based debug and configuration dumps from exposing
// private signing-key bytes or internal signing dependencies.
func (signer *Signer) Format(
	state fmt.State,
	_ rune,
) {
	if signer == nil {
		_, _ = io.WriteString(
			state,
			"<nil>",
		)

		return
	}

	_, _ = io.WriteString(
		state,
		"webbotauth.Signer{keyid:"+
			signer.keyID+
			",private_key:<redacted>}",
	)
}

func requestAuthority(
	request *http.Request,
) (string, error) {
	if request == nil || request.URL == nil {
		return "", ErrInvalidRequest
	}

	rawTarget := request.URL.String()
	if request.Host != "" {
		rawTarget = request.URL.Scheme + "://" + request.Host
	}

	canonical, err := origin.Parse(rawTarget)
	if err != nil {
		return "", ErrInvalidRequest
	}

	_, authority, _ := strings.Cut(
		canonical.String(),
		"://",
	)

	return authority, nil
}

func validKeyID(keyID string) bool {
	if len(keyID) != 43 {
		return false
	}

	decoded, err := base64.RawURLEncoding.DecodeString(
		keyID,
	)
	return err == nil && len(decoded) == sha256.Size &&
		base64.RawURLEncoding.EncodeToString(decoded) == keyID
}

func signatureParameters(
	created int64,
	expires int64,
	keyID string,
	nonce string,
) string {
	return fmt.Sprintf(
		`("@authority" "signature-agent");created=%d;keyid="%s";alg="ed25519";expires=%d;nonce="%s";tag="web-bot-auth"`,
		created,
		keyID,
		expires,
		nonce,
	)
}

func signatureBase(
	authority string,
	agentValue string,
	parameters string,
) string {
	return `"@authority": ` + authority + "\n" +
		`"signature-agent": ` + agentValue + "\n" +
		`"@signature-params": ` + parameters
}
