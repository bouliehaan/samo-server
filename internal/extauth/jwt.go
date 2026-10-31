package extauth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rsa"
	_ "crypto/sha256" // crypto.SHA256 and SHA384/512 must be linked in
	_ "crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"strings"
)

// Just enough JWS (RFC 7515) to verify an ID token: compact serialization,
// the RSA, RSA-PSS, ECDSA and HMAC algorithms providers actually sign with,
// and never "none". HMAC is keyed by the client secret (OIDC Core §10.1),
// which is what authentik uses for a provider with no signing key chosen.

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwks struct {
	Keys []jwk `json:"keys"`
}

type jwsHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

func splitJWS(token string) (header jwsHeader, signingInput string, payload, signature []byte, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return header, "", nil, nil, errors.New("not a compact JWS")
	}
	rawHeader, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return header, "", nil, nil, fmt.Errorf("header: %w", err)
	}
	if err := json.Unmarshal(rawHeader, &header); err != nil {
		return header, "", nil, nil, fmt.Errorf("header: %w", err)
	}
	if payload, err = base64.RawURLEncoding.DecodeString(parts[1]); err != nil {
		return header, "", nil, nil, fmt.Errorf("payload: %w", err)
	}
	if signature, err = base64.RawURLEncoding.DecodeString(parts[2]); err != nil {
		return header, "", nil, nil, fmt.Errorf("signature: %w", err)
	}
	return header, parts[0] + "." + parts[1], payload, signature, nil
}

// needsRefresh reports whether the token names a key the set lacks.
func needsRefresh(keys *jwks, token string) bool {
	header, _, _, _, err := splitJWS(token)
	if err != nil || strings.HasPrefix(header.Alg, "HS") || header.Kid == "" {
		return false
	}
	if keys == nil {
		return true
	}
	for _, key := range keys.Keys {
		if key.Kid == header.Kid {
			return false
		}
	}
	return true
}

// verifyJWS checks the token's signature and returns its payload.
func verifyJWS(token string, keys *jwks, clientSecret string) ([]byte, error) {
	header, signingInput, payload, signature, err := splitJWS(token)
	if err != nil {
		return nil, err
	}
	hashFunc, family, err := algorithm(header.Alg)
	if err != nil {
		return nil, err
	}
	digest := hashFunc.New()
	digest.Write([]byte(signingInput))
	sum := digest.Sum(nil)

	if family == "HS" {
		if clientSecret == "" {
			return nil, errors.New("signed with the client secret, and none is configured")
		}
		mac := hmac.New(func() hash.Hash { return hashFunc.New() }, []byte(clientSecret))
		mac.Write([]byte(signingInput))
		if !hmac.Equal(mac.Sum(nil), signature) {
			return nil, errors.New("signature does not verify")
		}
		return payload, nil
	}

	if keys == nil {
		return nil, errors.New("the provider published no keys")
	}
	tried := 0
	for _, key := range keys.Keys {
		if header.Kid != "" && key.Kid != header.Kid {
			continue
		}
		if key.Use != "" && key.Use != "sig" {
			continue
		}
		if key.Alg != "" && key.Alg != header.Alg {
			continue
		}
		tried++
		switch family {
		case "RS", "PS":
			public, err := key.rsa()
			if err != nil {
				continue
			}
			if family == "RS" {
				err = rsa.VerifyPKCS1v15(public, hashFunc, sum, signature)
			} else {
				err = rsa.VerifyPSS(public, hashFunc, sum, signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
			}
			if err == nil {
				return payload, nil
			}
		case "ES":
			public, err := key.ecdsa()
			if err != nil {
				continue
			}
			size := (public.Curve.Params().BitSize + 7) / 8
			if len(signature) != 2*size {
				continue
			}
			r := new(big.Int).SetBytes(signature[:size])
			s := new(big.Int).SetBytes(signature[size:])
			if ecdsa.Verify(public, sum, r, s) {
				return payload, nil
			}
		}
	}
	if tried == 0 {
		return nil, fmt.Errorf("no published key matches kid %q and alg %s", header.Kid, header.Alg)
	}
	return nil, errors.New("signature does not verify")
}

func algorithm(alg string) (crypto.Hash, string, error) {
	if len(alg) != 5 {
		return 0, "", fmt.Errorf("unsupported signing algorithm %q", alg)
	}
	family := alg[:2]
	switch family {
	case "RS", "PS", "ES", "HS":
	default:
		return 0, "", fmt.Errorf("unsupported signing algorithm %q", alg)
	}
	switch alg[2:] {
	case "256":
		return crypto.SHA256, family, nil
	case "384":
		return crypto.SHA384, family, nil
	case "512":
		return crypto.SHA512, family, nil
	}
	return 0, "", fmt.Errorf("unsupported signing algorithm %q", alg)
}

func (k jwk) rsa() (*rsa.PublicKey, error) {
	if k.Kty != "RSA" {
		return nil, errors.New("not an RSA key")
	}
	n, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	e, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	exponent := new(big.Int).SetBytes(e)
	if !exponent.IsInt64() || exponent.Int64() < 3 || exponent.Int64() > 1<<31 {
		return nil, errors.New("RSA exponent out of range")
	}
	public := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exponent.Int64())}
	if public.N.BitLen() < 2048 {
		return nil, errors.New("RSA key shorter than 2048 bits")
	}
	return public, nil
}

func (k jwk) ecdsa() (*ecdsa.PublicKey, error) {
	if k.Kty != "EC" {
		return nil, errors.New("not an EC key")
	}
	var curve elliptic.Curve
	switch k.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("unsupported curve %q", k.Crv)
	}
	x, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, err
	}
	y, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, err
	}
	public := &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	if !curve.IsOnCurve(public.X, public.Y) {
		return nil, errors.New("EC point is not on its curve")
	}
	return public, nil
}
