package common

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io/ioutil"
	"time"

	jwt "github.com/dgrijalva/jwt-go"
)

type JWTManager interface {
	GenerateToken() (string, error)
}

type jwtManagerImpl struct {
	privateKey *ecdsa.PrivateKey
	kid        string
	iss        string
}

func NewJWTManager(keyFile, keyID, teamID string) (JWTManager, error) {
	keyPEM, err := ioutil.ReadFile(keyFile)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(keyPEM)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
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
