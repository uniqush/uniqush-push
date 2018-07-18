package main

import (
	"testing"

	"github.com/uniqush/uniqush-push/test_util"
)

// TestValidateSubscribers tests valid and invalid subscriber names
func TestValidateSubscribers(t *testing.T) {
	validName := "valid_subscriber.123-AZ@_"
	err := validateSubscribers([]string{validName})
	test_util.ExpectEquals(t, nil, err, "expected valid for "+validName)

	invalidName := "invalid subscriber.123"
	expectedError := `invalid subscriber name: "invalid subscriber.123". Accepted characters: a-z, A-Z, 0-9, -, _, @ or .`
	err = validateSubscribers([]string{invalidName})
	if err == nil {
		t.Errorf("Expected error for " + invalidName)
	} else {
		test_util.ExpectEquals(t, expectedError, err.Error(), "unexpected error message")
	}

	err = validateSubscribers([]string{validName, invalidName})
	if err == nil {
		t.Errorf("Expected error for " + invalidName)
	} else {
		test_util.ExpectEquals(t, expectedError, err.Error(), "unexpected error message")
	}
}

// TestValidateSubscribers tests valid and invalid service names
func TestValidateService(t *testing.T) {
	validName := "valid_service.123"
	err := validateSubscribers([]string{validName})
	test_util.ExpectEquals(t, nil, err, "expected valid for "+validName)

	invalidName := "$invalid_service.123"
	expectedError := `invalid service name: "$invalid_service.123". Accepted characters: a-z, A-Z, 0-9, -, _, @ or .`
	err = validateService(invalidName)
	if err == nil {
		t.Errorf("Expected error for " + invalidName)
	} else {
		test_util.ExpectEquals(t, expectedError, err.Error(), "unexpected error message")
	}
}

// TestValidateLegacySubscribers tests that subscribers with the buggy regex will continue to work
func TestValidateLegacySubscribers(t *testing.T) {
	legacyName := `valid\legacy^[]`
	err := validateSubscribers([]string{legacyName})
	test_util.ExpectEquals(t, nil, err, "expected valid for "+legacyName)
}

// TestValidateLegacyService tests that services with the buggy regex will continue to work
func TestValidateLegacyService(t *testing.T) {
	legacyName := `valid\legacy^[]`
	err := validateSubscribers([]string{legacyName})
	test_util.ExpectEquals(t, nil, err, "expected valid for "+legacyName)
}
