// Package snykproof exists only on a throwaway branch: it imports a module
// with a known high-severity advisory so that the CI Snyk check must fail.
// It is never merged.
package snykproof

import (
	"fmt"

	jwt "github.com/dgrijalva/jwt-go"
)

// Parse parses a token with the vulnerable library.
func Parse(token string, key []byte) (*jwt.Token, error) {
	parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return key, nil })
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}

	return parsed, nil
}
