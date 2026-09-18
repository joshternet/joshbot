package webbotauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
)

const maxPrivateKeyPEMSize = 4096

var (
	// ErrIdentityUnavailable reports use of a nil signing identity.
	ErrIdentityUnavailable = errors.New(
		"web bot auth: signing identity unavailable",
	)

	// ErrInvalidPrivateKey reports malformed or unsupported PKCS#8 input.
	ErrInvalidPrivateKey = errors.New(
		"web bot auth: invalid Ed25519 private key",
	)

	// ErrUnsupportedPrivateKey reports valid PKCS#8 containing another
	// private-key algorithm.
	ErrUnsupportedPrivateKey = errors.New(
		"web bot auth: private key is not Ed25519",
	)

	// ErrPrivateKeyMismatch reports inconsistent Ed25519 private/public
	// material.
	ErrPrivateKeyMismatch = errors.New(
		"web bot auth: Ed25519 private key is inconsistent",
	)

	// ErrReadPrivateKey reports failure to read private-key input.
	ErrReadPrivateKey = errors.New(
		"web bot auth: cannot read private key",
	)

	// ErrPrivateKeyTooLarge reports private-key input beyond the bounded
	// configuration size.
	ErrPrivateKeyTooLarge = errors.New(
		"web bot auth: private key file is too large",
	)
)

// PublicJWK is the public OKP representation of JoshBot's Ed25519 signing key.
//
// It intentionally contains no private d member.
type PublicJWK struct {
	Crv string `json:"crv"`
	Kty string `json:"kty"`
	X   string `json:"x"`
}

// Identity contains JoshBot's validated Web Bot Auth signing identity.
//
// Private key material is deliberately unexported. Formatting and structured
// logging expose only the public key identifier.
type Identity struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	publicJWK  PublicJWK
	keyID      string
}

// NewIdentity validates an Ed25519 private key and derives its public identity.
//
// The public half embedded in the Go Ed25519 private-key representation must
// match the public key derived from the private seed.
func NewIdentity(
	privateKey ed25519.PrivateKey,
) (*Identity, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, ErrInvalidPrivateKey
	}

	canonical := ed25519.NewKeyFromSeed(
		privateKey[:ed25519.SeedSize],
	)

	if subtle.ConstantTimeCompare(
		privateKey,
		canonical,
	) != 1 {
		return nil, ErrPrivateKeyMismatch
	}

	publicKey := append(
		ed25519.PublicKey(nil),
		canonical[ed25519.SeedSize:]...,
	)

	x := base64.RawURLEncoding.EncodeToString(
		publicKey,
	)

	jwk := PublicJWK{
		Crv: "Ed25519",
		Kty: "OKP",
		X:   x,
	}

	return &Identity{
		privateKey: append(
			ed25519.PrivateKey(nil),
			canonical...,
		),
		publicKey: publicKey,
		publicJWK: jwk,
		keyID:     jwkThumbprint(jwk),
	}, nil
}

// ReadIdentity reads and validates one bounded PKCS#8 PEM Ed25519 private key.
func ReadIdentity(
	reader io.Reader,
) (*Identity, error) {
	if reader == nil {
		return nil, ErrReadPrivateKey
	}

	data, err := io.ReadAll(
		io.LimitReader(
			reader,
			int64(maxPrivateKeyPEMSize)+1,
		),
	)
	if err != nil {
		return nil, ErrReadPrivateKey
	}

	if len(data) > maxPrivateKeyPEMSize {
		return nil, ErrPrivateKeyTooLarge
	}

	return ParsePrivateKeyPEM(data)
}

// ParsePrivateKeyPEM parses one unencrypted PKCS#8 PRIVATE KEY PEM block.
//
// Additional non-whitespace content and PEM metadata are rejected.
func ParsePrivateKeyPEM(
	data []byte,
) (*Identity, error) {
	block, rest := pem.Decode(data)

	if block == nil ||
		block.Type != "PRIVATE KEY" ||
		len(block.Headers) != 0 ||
		len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrInvalidPrivateKey
	}

	parsed, err := x509.ParsePKCS8PrivateKey(
		block.Bytes,
	)
	if err != nil {
		return nil, ErrInvalidPrivateKey
	}

	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, ErrUnsupportedPrivateKey
	}

	return NewIdentity(privateKey)
}

// PublicKey returns a copy of the derived Ed25519 public key.
func (identity *Identity) PublicKey() ed25519.PublicKey {
	if identity == nil {
		return nil
	}

	return append(
		ed25519.PublicKey(nil),
		identity.publicKey...,
	)
}

// PublicJWK returns the public OKP JWK.
func (identity *Identity) PublicJWK() PublicJWK {
	if identity == nil {
		return PublicJWK{}
	}

	return identity.publicJWK
}

// KeyID returns the base64url SHA-256 JWK thumbprint used by Web Bot Auth.
func (identity *Identity) KeyID() string {
	if identity == nil {
		return ""
	}

	return identity.keyID
}

// Signer constructs a request signer from the validated identity.
func (identity *Identity) Signer() (*Signer, error) {
	if identity == nil {
		return nil, ErrIdentityUnavailable
	}

	return NewSigner(
		identity.privateKey,
		identity.keyID,
	)
}

// LogValue deliberately exposes only public identity metadata.
func (identity *Identity) LogValue() slog.Value {
	if identity == nil {
		return slog.StringValue("<nil>")
	}

	return slog.GroupValue(
		slog.String(
			"key_id",
			identity.keyID,
		),
	)
}

// Format prevents fmt-based debug/config dumps from exposing private bytes.
func (identity *Identity) Format(
	state fmt.State,
	_ rune,
) {
	if identity == nil {
		_, _ = io.WriteString(
			state,
			"<nil>",
		)

		return
	}

	_, _ = io.WriteString(
		state,
		"webbotauth.Identity{keyid:"+
			identity.keyID+
			",private_key:<redacted>}",
	)
}

func jwkThumbprint(
	jwk PublicJWK,
) string {
	canonical :=
		`{"crv":"` + jwk.Crv +
			`","kty":"` + jwk.Kty +
			`","x":"` + jwk.X +
			`"}`

	sum := sha256.Sum256(
		[]byte(canonical),
	)

	return base64.RawURLEncoding.EncodeToString(
		sum[:],
	)
}
