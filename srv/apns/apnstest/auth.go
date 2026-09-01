/*
 * Copyright 2013-2026 Uniqush Contributors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *	http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package apnstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Apple's bounds on a provider token, which the simulator enforces so that
// uniqush's refresh schedule can be tested without waiting an hour or an outage.
//
// https://developer.apple.com/documentation/usernotifications/establishing-a-token-based-connection-to-apns
const (
	// TokenMaxAge is how long Apple accepts a token after its iat. Past this it
	// answers 403 ExpiredProviderToken.
	TokenMaxAge = time.Hour
	// TokenMinInterval is how often Apple permits a *new* token for the same
	// key. Minting faster earns 429 TooManyProviderTokenUpdates.
	TokenMinInterval = 20 * time.Minute
)

// Reasons specific to token authentication.
const (
	ReasonExpiredProviderToken        = "ExpiredProviderToken"
	ReasonInvalidProviderToken        = "InvalidProviderToken"
	ReasonMissingProviderToken        = "MissingProviderToken"
	ReasonTooManyProviderTokenUpdates = "TooManyProviderTokenUpdates"
)

// SigningKey is a .p8 signing key for tests, and the public half the simulator
// verifies against.
type SigningKey struct {
	Private *ecdsa.PrivateKey
	KeyID   string
	TeamID  string
	// Path is the .p8 file, to be given to /addpsp as authkey.
	Path string
}

// GenerateSigningKey writes a P-256 key in the .p8 form Apple issues.
//
// Apple's file is PEM around PKCS#8, which is what this produces, so the same
// parsing path runs in tests as in production. A key generated here is of
// course not one Apple knows about -- the simulator verifies against the public
// half directly, which is what Apple does with the copy it keeps.
func GenerateSigningKey(dir, keyID, teamID string) (*SigningKey, error) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, fmt.Sprintf("AuthKey_%s.p8", keyID))
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		return nil, err
	}

	return &SigningKey{Private: private, KeyID: keyID, TeamID: teamID, Path: path}, nil
}

// RequireToken makes the simulator demand a provider token signed by this key,
// as APNs does for a team using token authentication.
func (s *Server) RequireToken(key *SigningKey) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.signingKey = key
}

// SetClock overrides the simulator's idea of the current time.
//
// The refresh schedule is the part of token auth most worth testing and the
// least testable in real time: the window between Apple's 20-minute mint floor
// and its 1-hour expiry is impossible to explore without either waiting or
// lying about the clock.
func (s *Server) SetClock(now func() time.Time) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.clock = now
}

func (s *Server) currentTime() time.Time {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// IssuedTokens returns every distinct provider token the simulator has seen, in
// the order they first arrived.
//
// The count is the assertion that matters for refresh behaviour: uniqush should
// reuse one token across many pushes, so a test that sends fifty pushes and
// sees fifty tokens has found a real bug even though every push succeeded.
func (s *Server) IssuedTokens() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]string(nil), s.tokensSeen...)
}

// checkProviderToken validates the authorization header the way APNs does.
func (s *Server) checkProviderToken(r *http.Request) (status int, reason, rule, details string) {
	s.mutex.Lock()
	key := s.signingKey
	s.mutex.Unlock()

	if key == nil {
		// This team uses certificate authentication; nothing to check.
		return 0, "", "", ""
	}

	authorization := r.Header.Get("authorization")
	if authorization == "" {
		// 401, which is what Apple documents for MissingProviderToken -- the
		// rest of the token failures are 403. uniqush only branches on the
		// reason, but the status is the one thing a test could assert on to
		// tell this apart from the 403 family, so getting it wrong would make
		// the simulator wrong on the axis it exists to be right about.
		return http.StatusUnauthorized, ReasonMissingProviderToken, "auth",
			"no authorization header; a provider using token authentication must send one on every request"
	}
	// Apple's examples use "bearer"; the scheme is case-insensitive.
	scheme, raw, found := strings.Cut(authorization, " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return http.StatusForbidden, ReasonInvalidProviderToken, "auth",
			fmt.Sprintf("authorization header %q is not a bearer token", authorization)
	}

	parsed, err := jwt.Parse(raw, func(token *jwt.Token) (interface{}, error) {
		// Refusing anything but ES256 is the point rather than pedantry: a
		// parser that accepted "none", or that accepted HS256 and verified it
		// against the public key as a shared secret, is the classic JWT
		// vulnerability. The simulator should not be forgiving where Apple is
		// not.
		if token.Method != jwt.SigningMethodES256 {
			return nil, fmt.Errorf("unexpected signing method %v", token.Header["alg"])
		}
		if kid, _ := token.Header["kid"].(string); kid != key.KeyID {
			return nil, fmt.Errorf("kid %q is not this team's key id", kid)
		}
		return &key.Private.PublicKey, nil
	}, jwt.WithoutClaimsValidation())
	if err != nil {
		return http.StatusForbidden, ReasonInvalidProviderToken, "auth",
			fmt.Sprintf("the provider token did not verify: %v", err)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return http.StatusForbidden, ReasonInvalidProviderToken, "auth", "the provider token has no claims"
	}
	if issuer, _ := claims["iss"].(string); issuer != key.TeamID {
		return http.StatusForbidden, ReasonInvalidProviderToken, "auth",
			fmt.Sprintf("iss %q is not the team id", issuer)
	}

	issuedAtSeconds, hasIat := claims["iat"].(float64)
	if !hasIat {
		return http.StatusForbidden, ReasonInvalidProviderToken, "auth",
			"the provider token has no iat claim, which APNs requires"
	}
	issuedAt := time.Unix(int64(issuedAtSeconds), 0)

	now := s.currentTime()
	if age := now.Sub(issuedAt); age > TokenMaxAge {
		return http.StatusForbidden, ReasonExpiredProviderToken, "auth",
			fmt.Sprintf("the provider token is %s old, past the %s limit; it is not being refreshed often enough",
				age.Round(time.Minute), TokenMaxAge)
	}

	if status, reason, rule, details := s.recordToken(raw, issuedAt, now); reason != "" {
		return status, reason, rule, details
	}
	return 0, "", "", ""
}

// recordToken remembers a token and enforces Apple's mint rate limit.
//
// observedAt is when the simulator saw the token, not the iat the token claims,
// and that distinction is the whole check. iat is chosen by the client, so a
// provider signing a fresh JWT for every push could space its claims twenty
// minutes apart and sail through a comparison of them -- while APNs, which can
// only observe arrivals, counts a burst of updates. Measuring against the
// server's own clock is both what Apple is able to do and the only form of this
// rule a broken client cannot talk its way out of.
func (s *Server) recordToken(raw string, issuedAt, observedAt time.Time) (status int, reason, rule, details string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, seen := s.tokenIssuedAt[raw]; seen {
		// A token uniqush is reusing, which is the expected case by a wide
		// margin: one token should serve every push for 45 minutes.
		return 0, "", "", ""
	}

	// A token never seen before is a mint. Apple counts those, not requests.
	if !s.lastMintedAt.IsZero() {
		if gap := observedAt.Sub(s.lastMintedAt); gap < TokenMinInterval {
			return http.StatusTooManyRequests, ReasonTooManyProviderTokenUpdates, "auth",
				fmt.Sprintf("a new provider token arrived %s after the previous one, inside Apple's %s limit",
					gap.Round(time.Second), TokenMinInterval)
		}
	}

	if s.tokenIssuedAt == nil {
		s.tokenIssuedAt = make(map[string]time.Time)
	}
	s.tokenIssuedAt[raw] = issuedAt
	s.tokensSeen = append(s.tokensSeen, raw)
	s.lastMintedAt = observedAt
	return 0, "", "", ""
}
