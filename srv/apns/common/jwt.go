package common

import (
	"crypto/ecdsa"
	"errors"
	"sync"
	"time"

	jwt "github.com/dgrijalva/jwt-go"
)

// JWTManager interface to manage APNS JWT
type JWTManager interface {
	// GenerateToken returns a token used for APNS connection
	// Returns error if fails to serialize or sign the token
	GenerateToken() (string, error)
}

var (
	jwtManagers = make(map[string]JWTManager)
	mutex       sync.Mutex
)

// SetJWTManager replaces instance of JWTManager for a certain key ID with the one specified
func SetJWTManager(keyID string, jwtManager JWTManager) {
	mutex.Lock()
	defer mutex.Unlock()

	jwtManagers[keyID] = jwtManager
}

type jwtManagerImpl struct {
	privateKey *ecdsa.PrivateKey
	kid        string
	iss        string
	token      *t
}

type t struct {
	content string
	expiry  time.Time
	mutex   sync.Mutex
}

// GetJWTManager returns a JWTManager to handle APNS authentication token
// Accepts keyFile as path to p8 key file, the key id, and issuer team id
// Every unique key ID has its own JWTManager instance
// Returns error if fails to read key from the provided path
func GetJWTManager(keyFile, keyID, teamID string) (JWTManager, error) {
	if _, ok := jwtManagers[keyID]; !ok {
		mutex.Lock()
		defer mutex.Unlock()
		if _, ok := jwtManagers[keyID]; !ok {
			jm, err := newJWTManager(keyFile, keyID, teamID)
			if err != nil {
				return nil, err
			}
			jwtManagers[keyID] = jm
		}
	}

	return jwtManagers[keyID], nil
}

func newJWTManager(keyFile, keyID, teamID string) (JWTManager, error) {
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
			token:      new(t),
		}, nil
	default:
		return nil, errors.New("Unsupported key algorithm")
	}
}

func (jm *jwtManagerImpl) GenerateToken() (string, error) {
	if !jm.token.isValid() {
		jm.token.mutex.Lock()
		defer jm.token.mutex.Unlock()
		if !jm.token.isValid() {
			jwt, expiry, err := jm.newToken()
			if err != nil {
				return "", err
			}
			jm.token.content = jwt
			jm.token.expiry = expiry
		}
	}
	return jm.token.content, nil
}

func (jm *jwtManagerImpl) newToken() (string, time.Time, error) {
	issuedAt := time.Now()

	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": jm.iss,
		"iat": issuedAt.Unix(),
	})
	token.Header["kid"] = jm.kid

	result, err := token.SignedString(jm.privateKey)
	if err != nil {
		return "", time.Time{}, err
	}
	return result, issuedAt.Add(1 * time.Hour), nil
}

func (token *t) isValid() bool {
	return token.content != "" && time.Now().Before(token.expiry)
}
