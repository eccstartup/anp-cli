// Package proof signs and verifies arbitrary content with the active
// identity's Ed25519 key-1 using the ANP SDK key material.
package proof

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/ANPWorld/anp-cli/internal/identity"
	"github.com/ANPWorld/anp-cli/internal/transport"
	anp "github.com/agent-network-protocol/anp/golang"
	anpauth "github.com/agent-network-protocol/anp/golang/authentication"
)

type SignatureProof struct {
	Algorithm string `json:"algorithm"`
	SignerDID string `json:"signer_did"`
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"`
	Digest    string `json:"digest"`
	SignedAt  string `json:"signed_at"`
}

// Sign signs file bytes with the identity's Ed25519 private key.
func Sign(active *identity.Identity, data []byte) (*SignatureProof, error) {
	raw, err := os.ReadFile(active.Keys.Key1Private)
	if err != nil {
		return nil, fmt.Errorf("read identity key: %w", err)
	}
	key, err := anp.PrivateKeyFromPEM(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parse identity key: %w", err)
	}
	signature, err := key.SignMessage(data)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	return &SignatureProof{
		Algorithm: "Ed25519",
		SignerDID: active.DID,
		KeyID:     active.DID + "#" + anpauth.VMKeyAuth,
		Signature: hex.EncodeToString(signature),
		Digest:    hex.EncodeToString(digestSHA256(data)),
		SignedAt:  time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// Verify checks a hex signature over data with the signer's public key. When
// did is empty, the active identity's public key is used; when did matches the
// active identity, the local key is preferred over network resolution.
func Verify(ctx context.Context, active *identity.Identity, did string, data []byte, signatureHex string) (*VerificationResult, error) {
	signature, err := hex.DecodeString(signatureHex)
	if err != nil {
		return nil, fmt.Errorf("invalid signature hex: %w", err)
	}
	var publicKey anp.PublicKeyMaterial
	useActive := did == "" || (active != nil && active.DID == did)
	if useActive {
		if active == nil {
			return nil, fmt.Errorf("no signer specified; pass --did or run with an active identity")
		}
		raw, err := os.ReadFile(active.Keys.Key1Public)
		if err != nil {
			return nil, fmt.Errorf("read identity public key: %w", err)
		}
		publicKey, err = anp.PublicKeyFromPEM(string(raw))
		if err != nil {
			return nil, fmt.Errorf("parse identity public key: %w", err)
		}
	} else {
		doc, err := transport.ResolveDIDDocument(ctx, did)
		if err != nil {
			return nil, err
		}
		publicKey, err = extractVerificationPublicKey(doc, did)
		if err != nil {
			return nil, err
		}
	}
	valid := publicKey.VerifyMessage(data, signature) == nil
	return &VerificationResult{Valid: valid, SignerDID: did}, nil
}

type VerificationResult struct {
	Valid     bool   `json:"valid"`
	SignerDID string `json:"signer_did,omitempty"`
}

// ParseProofFile loads a signature proof written by Sign's --output option.
func ParseProofFile(path string) (*SignatureProof, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var proof SignatureProof
	if err := json.Unmarshal(raw, &proof); err != nil {
		return nil, fmt.Errorf("parse proof file: %w", err)
	}
	if proof.Signature == "" {
		return nil, fmt.Errorf("proof file has no signature field")
	}
	return &proof, nil
}

func digestSHA256(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func extractVerificationPublicKey(doc map[string]any, did string) (anp.PublicKeyMaterial, error) {
	methodID := did + "#" + anpauth.VMKeyAuth
	methods, _ := doc["verificationMethod"].([]any)
	for _, entry := range methods {
		method, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if method["id"] != methodID {
			continue
		}
		// key-1 (Ed25519) is published as a Multikey (publicKeyMultibase) in
		// e1-profile DID documents; fall back to JWK / base58 for other forms.
		if multibase, ok := method["publicKeyMultibase"].(string); ok && multibase != "" {
			decoded, err := base58Decode(stripMultibasePrefix(multibase))
			if err != nil {
				return anp.PublicKeyMaterial{}, fmt.Errorf("decode publicKeyMultibase for %s: %w", methodID, err)
			}
			if len(decoded) == 34 && decoded[0] == 0xed && decoded[1] == 0x01 {
				decoded = decoded[2:]
			}
			return anp.PublicKeyMaterial{Type: anp.KeyTypeEd25519, Bytes: decoded}, nil
		}
		if jwk, ok := method["publicKeyJwk"].(map[string]any); ok {
			return anp.PublicKeyFromJWK(jwk)
		}
		if base58, ok := method["publicKeyBase58"].(string); ok && base58 != "" {
			decoded, err := base58Decode(base58)
			if err != nil {
				return anp.PublicKeyMaterial{}, fmt.Errorf("decode publicKeyBase58 for %s: %w", methodID, err)
			}
			return anp.PublicKeyMaterial{Type: anp.KeyTypeEd25519, Bytes: decoded}, nil
		}
	}
	return anp.PublicKeyMaterial{}, fmt.Errorf("did document has no verification method %s", methodID)
}

func stripMultibasePrefix(value string) string {
	if len(value) > 0 && value[0] == 'z' {
		return value[1:]
	}
	return value
}

// base58Decode decodes Bitcoin base58 (multibase base58btc) to bytes. The ANP
// SDK keeps this decoder in an internal package, so we replicate it here.
func base58Decode(input string) ([]byte, error) {
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	var alphabetIndex [128]int
	for i := range alphabetIndex {
		alphabetIndex[i] = -1
	}
	for i, c := range alphabet {
		alphabetIndex[c] = i
	}
	if input == "" {
		return []byte{}, nil
	}
	base := big.NewInt(58)
	value := new(big.Int)
	for _, char := range input {
		if int(char) >= len(alphabetIndex) || alphabetIndex[char] < 0 {
			return nil, fmt.Errorf("invalid base58 character %q", char)
		}
		value.Mul(value, base)
		value.Add(value, big.NewInt(int64(alphabetIndex[char])))
	}
	decoded := value.Bytes()
	leadingZeros := 0
	for _, char := range input {
		if char != rune(alphabet[0]) {
			break
		}
		leadingZeros++
	}
	if leadingZeros == 0 {
		return decoded, nil
	}
	result := make([]byte, leadingZeros+len(decoded))
	copy(result[leadingZeros:], decoded)
	return result, nil
}
