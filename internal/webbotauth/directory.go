package webbotauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// DirectoryContentType is the media type Cloudflare requires for a
	// signature directory.
	DirectoryContentType = "application/http-message-signatures-directory+json"

	// MaxDirectoryBodySize is the largest directory body JoshBot will read.
	MaxDirectoryBodySize = 64 * 1024

	// DirectoryStateSingleKey is a directory with one Ed25519 identity.
	DirectoryStateSingleKey = "single-key"

	// DirectoryStateRotationOverlap is a directory with the active identity
	// and one transition identity.
	DirectoryStateRotationOverlap = "rotation-overlap"

	directorySignatureTag = "http-message-signatures-directory"
	directoryCovered      = `("@authority";req "content-digest")`
	maxDirectoryKeys      = 2
	maxDirectoryLifetime  = 10 * time.Minute
	directoryClockSkew    = time.Minute
)

// ErrInvalidDirectory reports that a signature directory response does not
// meet the Joshternet publication contract.
var ErrInvalidDirectory = errors.New(
	"web bot auth: invalid signature directory",
)

// DirectoryReport is the public result of a validated signature directory.
type DirectoryReport struct {
	State  string
	KeyIDs []string
}

type directoryKey struct {
	id        string
	publicKey ed25519.PublicKey
}

// ValidateDirectory checks one fetched signature-directory response.
//
// The caller supplies the authority of the request that retrieved the
// directory. Claimed key identifiers are ignored until they match a
// thumbprint calculated from the published public JWK.
func ValidateDirectory(
	now time.Time,
	authority string,
	status int,
	header http.Header,
	body []byte,
) (DirectoryReport, error) {
	if status != http.StatusOK {
		return DirectoryReport{}, fmt.Errorf(
			"%w: HTTP %d",
			ErrInvalidDirectory,
			status,
		)
	}

	if err := directoryContentType(header); err != nil {
		return DirectoryReport{}, err
	}

	keys, err := parseDirectoryKeys(body)
	if err != nil {
		return DirectoryReport{}, err
	}

	digest := header.Get("Content-Digest")
	if err := directoryContentDigest(
		digest,
		body,
	); err != nil {
		return DirectoryReport{}, err
	}

	if err := verifyDirectorySignatures(
		now,
		authority,
		digest,
		keys,
		header,
	); err != nil {
		return DirectoryReport{}, err
	}

	report := DirectoryReport{
		State:  DirectoryStateSingleKey,
		KeyIDs: make([]string, 0, len(keys)),
	}
	if len(keys) == maxDirectoryKeys {
		report.State = DirectoryStateRotationOverlap
	}

	for _, key := range keys {
		report.KeyIDs = append(
			report.KeyIDs,
			key.id,
		)
	}

	return report, nil
}

func directoryContentType(header http.Header) error {
	if header == nil {
		return fmt.Errorf(
			"%w: content type",
			ErrInvalidDirectory,
		)
	}

	value := strings.TrimSpace(header.Get("Content-Type"))
	if strings.Contains(value, ";") ||
		!strings.EqualFold(value, DirectoryContentType) {
		return fmt.Errorf(
			"%w: content type",
			ErrInvalidDirectory,
		)
	}

	return nil
}

func parseDirectoryKeys(body []byte) ([]directoryKey, error) {
	if len(body) == 0 || len(body) > MaxDirectoryBodySize {
		return nil, fmt.Errorf(
			"%w: body",
			ErrInvalidDirectory,
		)
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	var document struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf(
			"%w: %w",
			ErrInvalidDirectory,
			err,
		)
	}

	if decoder.More() {
		return nil, fmt.Errorf(
			"%w: body",
			ErrInvalidDirectory,
		)
	}

	if len(document.Keys) == 0 ||
		len(document.Keys) > maxDirectoryKeys {
		return nil, fmt.Errorf(
			"%w: key count",
			ErrInvalidDirectory,
		)
	}

	keys := make([]directoryKey, 0, len(document.Keys))
	seen := make(map[string]struct{}, len(document.Keys))

	for _, raw := range document.Keys {
		key, err := parseDirectoryKey(raw)
		if err != nil {
			return nil, err
		}

		if _, exists := seen[key.id]; exists {
			return nil, fmt.Errorf(
				"%w: duplicate key",
				ErrInvalidDirectory,
			)
		}

		seen[key.id] = struct{}{}
		keys = append(keys, key)
	}

	return keys, nil
}

func parseDirectoryKey(raw json.RawMessage) (directoryKey, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return directoryKey{}, fmt.Errorf(
			"%w: key",
			ErrInvalidDirectory,
		)
	}

	if _, private := fields["d"]; private {
		return directoryKey{}, fmt.Errorf(
			"%w: private key",
			ErrInvalidDirectory,
		)
	}

	var parsed struct {
		Kty string `json:"kty"`
		Crv string `json:"crv"`
		X   string `json:"x"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil ||
		parsed.Kty != "OKP" ||
		parsed.Crv != "Ed25519" ||
		parsed.X == "" {
		return directoryKey{}, fmt.Errorf(
			"%w: key",
			ErrInvalidDirectory,
		)
	}

	public, err := base64.RawURLEncoding.DecodeString(parsed.X)
	if err != nil || len(public) != ed25519.PublicKeySize {
		return directoryKey{}, fmt.Errorf(
			"%w: key",
			ErrInvalidDirectory,
		)
	}

	jwk := PublicJWK{
		Crv: parsed.Crv,
		Kty: parsed.Kty,
		X:   parsed.X,
	}

	return directoryKey{
		id:        jwkThumbprint(jwk),
		publicKey: public,
	}, nil
}

func directoryContentDigest(value string, body []byte) error {
	const prefix = "sha-256=:"

	start := strings.Index(value, prefix)
	if start < 0 {
		return fmt.Errorf(
			"%w: content digest",
			ErrInvalidDirectory,
		)
	}

	encoded := value[start+len(prefix):]
	end := strings.Index(encoded, ":")
	if end < 0 {
		return fmt.Errorf(
			"%w: content digest",
			ErrInvalidDirectory,
		)
	}

	digest, err := base64.StdEncoding.DecodeString(
		encoded[:end],
	)
	sum := sha256.Sum256(body)
	if err != nil || !bytes.Equal(digest, sum[:]) {
		return fmt.Errorf(
			"%w: content digest",
			ErrInvalidDirectory,
		)
	}

	return nil
}

func verifyDirectorySignatures(
	now time.Time,
	authority string,
	digest string,
	keys []directoryKey,
	header http.Header,
) error {
	if authority == "" || digest == "" {
		return fmt.Errorf(
			"%w: signature",
			ErrInvalidDirectory,
		)
	}

	inputs, err := directoryDictionary(
		header.Get("Signature-Input"),
	)
	if err != nil {
		return err
	}

	signatures, err := directoryDictionary(
		header.Get("Signature"),
	)
	if err != nil {
		return err
	}

	if len(inputs) != len(signatures) ||
		len(inputs) != len(keys) {
		return fmt.Errorf(
			"%w: signature count",
			ErrInvalidDirectory,
		)
	}

	for _, key := range keys {
		label, item, ok := directoryInputForKey(
			inputs,
			key.id,
		)
		if !ok {
			return fmt.Errorf(
				"%w: signature",
				ErrInvalidDirectory,
			)
		}

		encoded, ok := signatures[label]
		if !ok {
			return fmt.Errorf(
				"%w: signature",
				ErrInvalidDirectory,
			)
		}

		signature, err := directorySignatureBytes(encoded)
		if err != nil {
			return err
		}

		if err := directorySignatureLifetime(
			now,
			item,
		); err != nil {
			return err
		}

		base := directorySignatureBase(
			authority,
			digest,
			item,
		)
		if !ed25519.Verify(
			key.publicKey,
			[]byte(base),
			signature,
		) {
			return fmt.Errorf(
				"%w: signature",
				ErrInvalidDirectory,
			)
		}
	}

	return nil
}

func directoryDictionary(
	value string,
) (map[string]string, error) {
	items, err := splitStructuredItems(value)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: signature",
			ErrInvalidDirectory,
		)
	}

	dictionary := make(map[string]string, len(items))

	for _, item := range items {
		label, member, ok := strings.Cut(item, "=")
		if !ok || label == "" || member == "" ||
			strings.Contains(label, " ") {
			return nil, fmt.Errorf(
				"%w: signature",
				ErrInvalidDirectory,
			)
		}

		if _, exists := dictionary[label]; exists {
			return nil, fmt.Errorf(
				"%w: signature",
				ErrInvalidDirectory,
			)
		}

		dictionary[label] = member
	}

	return dictionary, nil
}

func splitStructuredItems(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("empty")
	}

	var items []string
	var current strings.Builder
	quote := false
	depth := 0

	for index := 0; index < len(value); index++ {
		char := value[index]

		if quote {
			current.WriteByte(char)
			if char == '"' {
				quote = false
			}

			continue
		}

		switch char {
		case '"':
			quote = true
			current.WriteByte(char)
		case '(':
			depth++
			current.WriteByte(char)
		case ')':
			if depth == 0 {
				return nil, errors.New("list")
			}

			depth--
			current.WriteByte(char)
		case ',':
			if depth != 0 {
				current.WriteByte(char)

				continue
			}

			item := strings.TrimSpace(current.String())
			if item == "" {
				return nil, errors.New("item")
			}

			items = append(items, item)
			current.Reset()
		default:
			current.WriteByte(char)
		}
	}

	if quote || depth != 0 {
		return nil, errors.New("item")
	}

	item := strings.TrimSpace(current.String())
	if item == "" {
		return nil, errors.New("item")
	}

	items = append(items, item)

	return items, nil
}

func directoryInputForKey(
	inputs map[string]string,
	keyID string,
) (string, string, bool) {
	for label, item := range inputs {
		if directoryKeyID(item) == keyID &&
			directoryInputAccepts(item) {
			return label, item, true
		}
	}

	return "", "", false
}

func directoryInputAccepts(item string) bool {
	if item != directoryCovered &&
		!strings.HasPrefix(item, directoryCovered+";") {
		return false
	}

	return directoryParameter(item, "alg") == "ed25519" &&
		directoryParameter(item, "tag") == directorySignatureTag
}

func directoryKeyID(item string) string {
	return directoryParameter(item, "keyid")
}

func directoryParameter(item, name string) string {
	parts := splitSignatureParameters(item)
	prefix := name + "="

	for _, part := range parts {
		if !strings.HasPrefix(part, prefix) {
			continue
		}

		value := strings.TrimPrefix(part, prefix)

		return strings.Trim(value, `"`)
	}

	return ""
}

func splitSignatureParameters(item string) []string {
	var parts []string
	var current strings.Builder
	quote := false

	for index := 0; index < len(item); index++ {
		char := item[index]

		if char == '"' {
			quote = !quote
			current.WriteByte(char)

			continue
		}

		if char == ';' && !quote {
			parts = append(
				parts,
				current.String(),
			)
			current.Reset()

			continue
		}

		current.WriteByte(char)
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

func directorySignatureBytes(value string) ([]byte, error) {
	if len(value) < 3 ||
		!strings.HasPrefix(value, ":") ||
		!strings.HasSuffix(value, ":") {
		return nil, fmt.Errorf(
			"%w: signature",
			ErrInvalidDirectory,
		)
	}

	signature, err := base64.StdEncoding.DecodeString(
		value[1 : len(value)-1],
	)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf(
			"%w: signature",
			ErrInvalidDirectory,
		)
	}

	return signature, nil
}

func directorySignatureLifetime(
	now time.Time,
	item string,
) error {
	created, createdErr := strconv.ParseInt(
		directoryParameter(item, "created"),
		10,
		64,
	)
	expires, expiresErr := strconv.ParseInt(
		directoryParameter(item, "expires"),
		10,
		64,
	)
	if createdErr != nil || expiresErr != nil {
		return fmt.Errorf(
			"%w: signature",
			ErrInvalidDirectory,
		)
	}

	if expires <= created {
		return fmt.Errorf(
			"%w: signature lifetime",
			ErrInvalidDirectory,
		)
	}

	createdAt := time.Unix(created, 0).UTC()
	expiresAt := time.Unix(expires, 0).UTC()
	if expiresAt.Sub(createdAt) > maxDirectoryLifetime ||
		now.Before(createdAt.Add(-directoryClockSkew)) ||
		now.After(expiresAt.Add(directoryClockSkew)) {
		return fmt.Errorf(
			"%w: signature lifetime",
			ErrInvalidDirectory,
		)
	}

	return nil
}

func directorySignatureBase(
	authority string,
	digest string,
	parameters string,
) string {
	return `"@authority";req: ` + authority + "\n" +
		`"content-digest": ` + digest + "\n" +
		`"@signature-params": ` + parameters
}
