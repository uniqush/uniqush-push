package common

import (
	"testing"

	jwt "github.com/dgrijalva/jwt-go"
)

func TestGenerateToken(t *testing.T) {
	keyFile := "../apns-test/localhost.p8"
	keyID := "FD8789SD9"
	teamID := "JVNS20943"

	jwtManager, err := GetJWTManager(keyFile, keyID, teamID)
	if err != nil {
		t.Fatal("Failed loading JWT key,", err)
	}

	tokenString, err := jwtManager.GenerateToken()
	if err != nil {
		t.Fatal("Failed generating new token,", err)
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return jwtManager.(*jwtManagerImpl).privateKey.Public(), nil
	})

	if !token.Valid {
		t.Fatal("Invalid token")
	}
	if token.Header["alg"] != jwt.SigningMethodES256.Alg() {
		t.Fatalf("Wrong signing method found, expected `%s`, got `%s`", jwt.SigningMethodES256.Alg(), token.Header["alg"])
	}
	if token.Header["kid"] != keyID {
		t.Fatalf("Wrong key ID found, expected `%s`, got `%s`", keyID, token.Header["kid"])
	}
	claims := token.Claims.(jwt.MapClaims)
	if claims["iss"] != teamID {
		t.Fatalf("Wrong issuer found, expected `%s`, got `%s`", teamID, claims["iss"])
	}
}
