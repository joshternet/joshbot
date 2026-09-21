package webbotauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestValidateDirectory(t *testing.T) {
	now := time.Date(
		2026,
		time.September,
		21,
		0,
		0,
		0,
		0,
		time.UTC,
	)
	authority := "joshternet.org"
	active := newDirectoryIdentity(t)
	transition := newDirectoryIdentity(t)

	t.Run("single key", func(t *testing.T) {
		header, body := signedDirectoryResponse(
			t,
			authority,
			now,
			5*time.Minute,
			active,
		)

		report, err := ValidateDirectory(
			now,
			authority,
			http.StatusOK,
			header,
			body,
		)
		if err != nil {
			t.Fatalf(
				"ValidateDirectory() error = %v",
				err,
			)
		}

		if report.State != DirectoryStateSingleKey {
			t.Errorf(
				"state = %q, want %q",
				report.State,
				DirectoryStateSingleKey,
			)
		}

		wantID := directoryTestThumbprint(
			active.PublicJWK(),
		)
		if len(report.KeyIDs) != 1 ||
			report.KeyIDs[0] != wantID {
			t.Fatalf(
				"key IDs = %v, want [%s]",
				report.KeyIDs,
				wantID,
			)
		}
	})

	t.Run("rotation overlap", func(t *testing.T) {
		header, body := signedDirectoryResponse(
			t,
			authority,
			now,
			5*time.Minute,
			active,
			transition,
		)

		report, err := ValidateDirectory(
			now,
			authority,
			http.StatusOK,
			header,
			body,
		)
		if err != nil {
			t.Fatalf(
				"ValidateDirectory() error = %v",
				err,
			)
		}

		if report.State != DirectoryStateRotationOverlap {
			t.Errorf(
				"state = %q, want %q",
				report.State,
				DirectoryStateRotationOverlap,
			)
		}

		if len(report.KeyIDs) != 2 {
			t.Fatalf(
				"key IDs = %v, want two keys",
				report.KeyIDs,
			)
		}
	})

	t.Run("accepts the skew boundary", func(t *testing.T) {
		header, body := signedDirectoryResponse(
			t,
			authority,
			now,
			time.Minute,
			active,
		)

		_, err := ValidateDirectory(
			now.Add(2*time.Minute),
			authority,
			http.StatusOK,
			header,
			body,
		)
		if err != nil {
			t.Fatalf(
				"ValidateDirectory() error = %v",
				err,
			)
		}
	})

	validHeader, validBody := signedDirectoryResponse(
		t,
		authority,
		now,
		5*time.Minute,
		active,
	)

	tests := []struct {
		name      string
		now       time.Time
		authority string
		status    int
		header    http.Header
		body      []byte
	}{
		{
			name:      "unexpected status",
			now:       now,
			authority: authority,
			status:    http.StatusUnauthorized,
			header:    validHeader,
			body:      validBody,
		},
		{
			name:      "missing header",
			now:       now,
			authority: authority,
			status:    http.StatusOK,
			body:      validBody,
		},
		{
			name:      "content type parameter",
			now:       now,
			authority: authority,
			status:    http.StatusOK,
			header: directoryHeader(
				DirectoryContentType+"; charset=utf-8",
				validHeader.Get("Content-Digest"),
				validHeader.Get("Signature-Input"),
				validHeader.Get("Signature"),
			),
			body: validBody,
		},
		{
			name:      "empty body",
			now:       now,
			authority: authority,
			status:    http.StatusOK,
			header:    validHeader.Clone(),
			body:      nil,
		},
		{
			name:      "oversized body",
			now:       now,
			authority: authority,
			status:    http.StatusOK,
			header:    validHeader.Clone(),
			body:      make([]byte, MaxDirectoryBodySize+1),
		},
		{
			name:      "invalid JSON",
			now:       now,
			authority: authority,
			status:    http.StatusOK,
			header:    validHeader.Clone(),
			body:      []byte("{"),
		},
		{
			name:      "trailing JSON",
			now:       now,
			authority: authority,
			status:    http.StatusOK,
			header:    validHeader.Clone(),
			body:      []byte(`{"keys":[]}{}`),
		},
		{
			name:      "no keys",
			now:       now,
			authority: authority,
			status:    http.StatusOK,
			header:    validHeader.Clone(),
			body:      []byte(`{"keys":[]}`),
		},
		{
			name:      "unknown document member",
			now:       now,
			authority: authority,
			status:    http.StatusOK,
			header:    validHeader.Clone(),
			body:      []byte(`{"keys":[],"extra":true}`),
		},
		{
			name:      "key is not an object",
			now:       now,
			authority: authority,
			status:    http.StatusOK,
			header:    validHeader.Clone(),
			body:      []byte(`{"keys":["nope"]}`),
		},
		{
			name:      "unsupported key type",
			now:       now,
			authority: authority,
			status:    http.StatusOK,
			header:    validHeader.Clone(),
			body: []byte(
				`{"keys":[{"kty":"RSA","crv":"Ed25519","x":"abc"}]}`,
			),
		},
		{
			name:      "invalid public key",
			now:       now,
			authority: authority,
			status:    http.StatusOK,
			header:    validHeader.Clone(),
			body: []byte(
				`{"keys":[{"kty":"OKP","crv":"Ed25519","x":"****"}]}`,
			),
		},
		{
			name:      "short public key",
			now:       now,
			authority: authority,
			status:    http.StatusOK,
			header:    validHeader.Clone(),
			body: []byte(
				`{"keys":[{"kty":"OKP","crv":"Ed25519","x":"YQ"}]}`,
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateDirectory(
				test.now,
				test.authority,
				test.status,
				test.header,
				test.body,
			)
			if !errors.Is(err, ErrInvalidDirectory) {
				t.Fatalf(
					"ValidateDirectory() error = %v, want invalid directory",
					err,
				)
			}
		})
	}

	t.Run("private key material", func(t *testing.T) {
		const secret = "PRIVATE-MATERIAL-SENTINEL"
		jwk := active.PublicJWK()
		body := []byte(
			`{"keys":[{"kty":"OKP","crv":"Ed25519","x":"` +
				jwk.X +
				`","d":"` + secret + `"}]}`,
		)

		_, err := ValidateDirectory(
			now,
			authority,
			http.StatusOK,
			validHeader.Clone(),
			body,
		)
		if !errors.Is(err, ErrInvalidDirectory) {
			t.Fatalf(
				"ValidateDirectory() error = %v, want invalid directory",
				err,
			)
		}

		if strings.Contains(err.Error(), secret) {
			t.Fatalf(
				"error leaked private material: %v",
				err,
			)
		}
	})

	t.Run("duplicate key", func(t *testing.T) {
		jwk := active.PublicJWK()
		document := struct {
			Keys []PublicJWK `json:"keys"`
		}{
			Keys: []PublicJWK{jwk, jwk},
		}
		body, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}

		_, err = ValidateDirectory(
			now,
			authority,
			http.StatusOK,
			validHeader.Clone(),
			body,
		)
		if !errors.Is(err, ErrInvalidDirectory) {
			t.Fatalf(
				"ValidateDirectory() error = %v, want invalid directory",
				err,
			)
		}
	})

	t.Run("more than two keys", func(t *testing.T) {
		third := newDirectoryIdentity(t)
		document := struct {
			Keys []PublicJWK `json:"keys"`
		}{
			Keys: []PublicJWK{
				active.PublicJWK(),
				transition.PublicJWK(),
				third.PublicJWK(),
			},
		}
		body, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}

		_, err = ValidateDirectory(
			now,
			authority,
			http.StatusOK,
			validHeader.Clone(),
			body,
		)
		if !errors.Is(err, ErrInvalidDirectory) {
			t.Fatalf(
				"ValidateDirectory() error = %v, want invalid directory",
				err,
			)
		}
	})

	t.Run("content digest does not match", func(t *testing.T) {
		header := validHeader.Clone()
		header.Set(
			"Content-Digest",
			"sha-256=:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=:",
		)

		_, err := ValidateDirectory(
			now,
			authority,
			http.StatusOK,
			header,
			validBody,
		)
		if !errors.Is(err, ErrInvalidDirectory) {
			t.Fatalf(
				"ValidateDirectory() error = %v, want invalid directory",
				err,
			)
		}
	})

	t.Run("content digest is truncated", func(t *testing.T) {
		header := validHeader.Clone()
		header.Set("Content-Digest", "sha-256=:YQ")

		_, err := ValidateDirectory(
			now,
			authority,
			http.StatusOK,
			header,
			validBody,
		)
		if !errors.Is(err, ErrInvalidDirectory) {
			t.Fatalf(
				"ValidateDirectory() error = %v, want invalid directory",
				err,
			)
		}
	})

	t.Run("signature bytes do not verify", func(t *testing.T) {
		header := validHeader.Clone()
		encoded := strings.TrimSuffix(
			strings.TrimPrefix(
				header.Get("Signature"),
				"binding0=:",
			),
			":",
		)
		signature, err := base64.StdEncoding.DecodeString(
			encoded,
		)
		if err != nil {
			t.Fatal(err)
		}

		signature[0] ^= 0xff
		header.Set(
			"Signature",
			"binding0=:"+
				base64.StdEncoding.EncodeToString(
					signature,
				)+
				":",
		)

		_, err = ValidateDirectory(
			now,
			authority,
			http.StatusOK,
			header,
			validBody,
		)
		if !errors.Is(err, ErrInvalidDirectory) {
			t.Fatalf(
				"ValidateDirectory() error = %v, want invalid directory",
				err,
			)
		}
	})

	t.Run("signature label does not match", func(t *testing.T) {
		header := validHeader.Clone()
		header.Set(
			"Signature",
			strings.Replace(
				header.Get("Signature"),
				"binding0=",
				"binding1=",
				1,
			),
		)

		_, err := ValidateDirectory(
			now,
			authority,
			http.StatusOK,
			header,
			validBody,
		)
		if !errors.Is(err, ErrInvalidDirectory) {
			t.Fatalf(
				"ValidateDirectory() error = %v, want invalid directory",
				err,
			)
		}
	})

	rejections := []struct {
		name   string
		mutate func(http.Header)
	}{
		{
			name: "missing digest",
			mutate: func(header http.Header) {
				header.Del("Content-Digest")
			},
		},
		{
			name: "malformed digest",
			mutate: func(header http.Header) {
				header.Set(
					"Content-Digest",
					"sha-256=:****:",
				)
			},
		},
		{
			name: "empty signature",
			mutate: func(header http.Header) {
				header.Set("Signature", " ")
			},
		},
		{
			name: "duplicate signature label",
			mutate: func(header http.Header) {
				header.Set(
					"Signature",
					"binding0=:YQ==:, binding0=:YQ==:",
				)
			},
		},
		{
			name: "signature without a label",
			mutate: func(header http.Header) {
				header.Set("Signature", "=:YQ==:")
			},
		},
		{
			name: "signature is not a byte sequence",
			mutate: func(header http.Header) {
				header.Set("Signature", "binding0=YQ==")
			},
		},
		{
			name: "unterminated signature parameter",
			mutate: func(header http.Header) {
				header.Set(
					"Signature-Input",
					`binding0=("unterminated`,
				)
			},
		},
		{
			name: "unbalanced component list",
			mutate: func(header http.Header) {
				header.Set(
					"Signature-Input",
					"binding0=(",
				)
			},
		},
		{
			name: "empty dictionary member",
			mutate: func(header http.Header) {
				header.Set(
					"Signature",
					"binding0=:YQ==:,",
				)
			},
		},
		{
			name: "comma inside the component list",
			mutate: func(header http.Header) {
				header.Set(
					"Signature-Input",
					`binding0=("@authority";req "content-digest", "extra");alg="ed25519"`,
				)
			},
		},
		{
			name: "wrong algorithm",
			mutate: func(header http.Header) {
				header.Set(
					"Signature-Input",
					strings.Replace(
						header.Get("Signature-Input"),
						`alg="ed25519"`,
						`alg="rsa"`,
						1,
					),
				)
			},
		},
		{
			name: "wrong tag",
			mutate: func(header http.Header) {
				header.Set(
					"Signature-Input",
					strings.Replace(
						header.Get("Signature-Input"),
						`tag="http-message-signatures-directory"`,
						`tag="web-bot-auth"`,
						1,
					),
				)
			},
		},
		{
			name: "covered components do not match",
			mutate: func(header http.Header) {
				header.Set(
					"Signature-Input",
					strings.Replace(
						header.Get("Signature-Input"),
						`("@authority";req "content-digest")`,
						`("@authority")`,
						1,
					),
				)
			},
		},
		{
			name: "extra signature",
			mutate: func(header http.Header) {
				header.Set(
					"Signature",
					header.Get("Signature")+
						", binding1=:YQ==:",
				)
			},
		},
		{
			name: "signature list is unbalanced",
			mutate: func(header http.Header) {
				header.Set("Signature", "binding0=)")
			},
		},
		{
			name: "signature dictionary starts empty",
			mutate: func(header http.Header) {
				header.Set(
					"Signature",
					",binding0=:YQ==:",
				)
			},
		},
		{
			name: "signature bytes are truncated",
			mutate: func(header http.Header) {
				header.Set("Signature", "binding0=:YQ==:")
			},
		},
		{
			name: "missing created",
			mutate: func(header http.Header) {
				header.Set(
					"Signature-Input",
					strings.Replace(
						header.Get("Signature-Input"),
						"created="+strconv.FormatInt(
							now.Unix(),
							10,
						)+";",
						"",
						1,
					),
				)
			},
		},
	}

	for _, test := range rejections {
		t.Run(test.name, func(t *testing.T) {
			header := validHeader.Clone()
			test.mutate(header)

			_, err := ValidateDirectory(
				now,
				authority,
				http.StatusOK,
				header,
				validBody,
			)
			if !errors.Is(err, ErrInvalidDirectory) {
				t.Fatalf(
					"ValidateDirectory() error = %v, want invalid directory",
					err,
				)
			}
		})
	}

	t.Run("lifetime is not current", func(t *testing.T) {
		cases := []struct {
			name     string
			now      time.Time
			lifetime time.Duration
		}{
			{
				name:     "already expired",
				now:      now.Add(10 * time.Minute),
				lifetime: time.Minute,
			},
			{
				name:     "not yet valid",
				now:      now.Add(-3 * time.Minute),
				lifetime: time.Minute,
			},
			{
				name:     "window is too long",
				now:      now,
				lifetime: 11 * time.Minute,
			},
			{
				name:     "expires immediately",
				now:      now,
				lifetime: 0,
			},
		}

		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				header, body := signedDirectoryResponse(
					t,
					authority,
					now,
					test.lifetime,
					active,
				)

				_, err := ValidateDirectory(
					test.now,
					authority,
					http.StatusOK,
					header,
					body,
				)
				if !errors.Is(err, ErrInvalidDirectory) {
					t.Fatalf(
						"ValidateDirectory() error = %v, want invalid directory",
						err,
					)
				}
			})
		}
	})

	t.Run("authority is required", func(t *testing.T) {
		_, err := ValidateDirectory(
			now,
			"",
			http.StatusOK,
			validHeader,
			validBody,
		)
		if !errors.Is(err, ErrInvalidDirectory) {
			t.Fatalf(
				"ValidateDirectory() error = %v, want invalid directory",
				err,
			)
		}
	})
}

func newDirectoryIdentity(t *testing.T) *Identity {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	identity, err := NewIdentity(privateKey)
	if err != nil {
		t.Fatal(err)
	}

	return identity
}

func signedDirectoryResponse(
	t *testing.T,
	authority string,
	created time.Time,
	lifetime time.Duration,
	identities ...*Identity,
) (http.Header, []byte) {
	t.Helper()

	keys := make([]PublicJWK, 0, len(identities))
	for _, identity := range identities {
		keys = append(keys, identity.PublicJWK())
	}

	body, err := json.Marshal(struct {
		Keys []PublicJWK `json:"keys"`
	}{
		Keys: keys,
	})
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(body)
	digest := "sha-256=:" +
		base64.StdEncoding.EncodeToString(sum[:]) +
		":"
	expires := created.Add(lifetime).Unix()

	var signatureInput []string
	var signature []string

	for index, identity := range identities {
		keyID := directoryTestThumbprint(
			identity.PublicJWK(),
		)
		parameters := fmt.Sprintf(
			`("@authority";req "content-digest");created=%d;expires=%d;keyid="%s";alg="ed25519";tag="http-message-signatures-directory"`,
			created.Unix(),
			expires,
			keyID,
		)
		base := "\"@authority\";req: " + authority + "\n" +
			"\"content-digest\": " + digest + "\n" +
			"\"@signature-params\": " + parameters
		signed := ed25519.Sign(
			identity.privateKey,
			[]byte(base),
		)
		label := fmt.Sprintf("binding%d", index)
		signatureInput = append(
			signatureInput,
			label+"="+parameters,
		)
		signature = append(
			signature,
			label+"=:"+
				base64.StdEncoding.EncodeToString(signed)+
				":",
		)
	}

	return directoryHeader(
		DirectoryContentType,
		digest,
		strings.Join(signatureInput, ", "),
		strings.Join(signature, ", "),
	), body
}

func directoryHeader(
	contentType string,
	digest string,
	signatureInput string,
	signature string,
) http.Header {
	header := make(http.Header)
	header.Set("Content-Type", contentType)
	header.Set("Content-Digest", digest)
	header.Set("Signature-Input", signatureInput)
	header.Set("Signature", signature)

	return header
}

func directoryTestThumbprint(jwk PublicJWK) string {
	canonical :=
		`{"crv":"` + jwk.Crv +
			`","kty":"` + jwk.Kty +
			`","x":"` + jwk.X +
			`"}`
	sum := sha256.Sum256([]byte(canonical))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}
