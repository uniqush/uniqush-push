package common

import (
	"crypto/ecdsa"
	"errors"
	"time"

	jwt "github.com/dgrijalva/jwt-go"
)

// JWTManager interface to manage APNS JWT
type JWTManager interface {
	// GenerateToken returns a token used for APNS connection
	// Returns error if fails to serialize or sign the token
	GenerateToken() (string, error)
}

type jwtManagerImpl struct {
	privateKey *ecdsa.PrivateKey
	kid        string
	iss        string
}

// NewJWTManager creates a JWTManager to handle APNS authentication token
// Accepts keyFile as path to p8 key file, the key id, and issuer team id
// Returns error if fails to read key from the provided path
func NewJWTManager(keyFile, keyID, teamID string) (JWTManager, error) {
	key, err := LoadPKCS8Key(keyFile)
	if err != nil {
		return nil, err
	}
	switch key := key.(type) {
	case *ecdsa.PrivateKey:
		return &jwtManagerImpl{
			privateKey: key,
			kid:        keyID,
			iss:        teamID,
		}, nil
	default:
		return nil, errors.New("Unsupported key algorithm")
	}
}

func (jm *jwtManagerImpl) GenerateToken() (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": jm.iss,
		"iat": time.Now().Unix(),
	})
	token.Header["kid"] = jm.kid
	return token.SignedString(jm.privateKey)
}
